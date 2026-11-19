package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"research-assistant-backend/internal/models"
)

var errInjectedStoreIO = errors.New("injected plan store I/O failure")

// Real temporary files preserve the filesystem effects while a specific syscall
// fails. Hooks are local to a store instance and never race with other tests.
type faultDurableFile struct {
	durableFile
	write    func([]byte) (int, error)
	sync     func() error
	close    func() error
	truncate func(int64) error
	seek     func(int64, int) (int64, error)
}

func (f *faultDurableFile) Write(raw []byte) (int, error) {
	if f.write != nil {
		return f.write(raw)
	}
	return f.durableFile.Write(raw)
}
func (f *faultDurableFile) Sync() error {
	if f.sync != nil {
		return f.sync()
	}
	return f.durableFile.Sync()
}
func (f *faultDurableFile) Close() error {
	if f.close != nil {
		return f.close()
	}
	return f.durableFile.Close()
}
func (f *faultDurableFile) Truncate(n int64) error {
	if f.truncate != nil {
		return f.truncate(n)
	}
	return f.durableFile.Truncate(n)
}
func (f *faultDurableFile) Seek(n int64, whence int) (int64, error) {
	if f.seek != nil {
		return f.seek(n, whence)
	}
	return f.durableFile.Seek(n, whence)
}

func readStoreFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func assertPlanName(t *testing.T, s *FilePlanStore, id, name string) {
	t.Helper()
	plan, err := s.GetPlan(id)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Nodes[0].Name != name {
		t.Fatalf("name=%q want %q", plan.Nodes[0].Name, name)
	}
}

func reopenFileStore(t *testing.T, path string) *FilePlanStore {
	t.Helper()
	s, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFilePlanStoreCommitKnownFailuresAndRetry(t *testing.T) {
	for _, operation := range []string{"create", "update", "delete"} {
		for _, point := range []string{"create_temp", "write", "short_write", "sync", "close", "replace"} {
			t.Run(operation+"/"+point, func(t *testing.T) {
				s, path := newTestFileStore(t)
				plan := testPlan("commit-failure")
				if operation != "create" {
					if err := s.SavePlan(plan); err != nil {
						t.Fatal(err)
					}
				}
				var before []byte
				if operation != "create" {
					before = readStoreFile(t, s.snapshotPath(plan.ID))
				}
				ops := defaultFileStoreIO()
				if point == "create_temp" {
					ops.createTemp = func(string, string) (durableFile, error) { return nil, errInjectedStoreIO }
				}
				if point == "replace" {
					ops.replace = func(string, string) (bool, error) { return false, errInjectedStoreIO }
				}
				if point != "create_temp" && point != "replace" {
					create := ops.createTemp
					ops.createTemp = func(dir, pattern string) (durableFile, error) {
						file, err := create(dir, pattern)
						if err != nil {
							return nil, err
						}
						wrapped := &faultDurableFile{durableFile: file}
						switch point {
						case "write":
							wrapped.write = func(raw []byte) (int, error) { n, _ := file.Write(raw[:len(raw)/2]); return n, errInjectedStoreIO }
						case "short_write":
							wrapped.write = func(raw []byte) (int, error) { return file.Write(raw[:len(raw)/2]) }
						case "sync":
							wrapped.sync = func() error { return errInjectedStoreIO }
						case "close":
							wrapped.close = func() error { _ = file.Close(); return errInjectedStoreIO }
						}
						return wrapped, nil
					}
				}
				s.io = ops
				perform := func() error {
					switch operation {
					case "create":
						return s.SavePlan(plan)
					case "delete":
						return s.DeletePlan(plan.ID)
					default:
						return s.UpdatePlan(plan.ID, func(p *models.PlanGraph) error { p.Nodes[0].Name = "updated"; return nil })
					}
				}
				if err := perform(); !errors.Is(err, ErrNotCommitted) || errors.Is(err, ErrCommitUnknown) {
					t.Fatalf("wrong failure: %v", err)
				}
				if err := s.Health(); err != nil {
					t.Fatalf("known failure sealed store: %v", err)
				}
				reopened := reopenFileStore(t, path)
				if operation == "create" {
					if _, err := s.GetPlan(plan.ID); !errors.Is(err, ErrPlanNotFound) {
						t.Fatalf("created in memory: %v", err)
					}
					if _, err := reopened.GetPlan(plan.ID); !errors.Is(err, ErrPlanNotFound) {
						t.Fatalf("created on disk: %v", err)
					}
				} else {
					assertPlanName(t, s, plan.ID, plan.Nodes[0].Name)
					assertPlanName(t, reopened, plan.ID, plan.Nodes[0].Name)
					if !bytes.Equal(before, readStoreFile(t, s.snapshotPath(plan.ID))) {
						t.Fatal("failure changed committed snapshot")
					}
				}
				s.io = defaultFileStoreIO()
				if err := perform(); err != nil {
					t.Fatalf("retry: %v", err)
				}
				reopened = reopenFileStore(t, path)
				if operation == "delete" {
					if _, err := reopened.GetPlan(plan.ID); !errors.Is(err, ErrPlanNotFound) {
						t.Fatal("deleted plan recovered")
					}
				} else {
					name := plan.Nodes[0].Name
					if operation == "update" {
						name = "updated"
					}
					assertPlanName(t, reopened, plan.ID, name)
				}
			})
		}
	}
}

func assertStoreSealed(t *testing.T, s *FilePlanStore, id string) {
	t.Helper()
	checks := []func() error{
		s.Health,
		func() error { _, err := s.GetPlan(id); return err },
		func() error { _, err := s.ListPlans(); return err },
		func() error { _, err := s.ListPlanSummaries(); return err },
		func() error { _, err := s.ListEvents(id); return err },
		func() error { _, err := s.ListEventsAfter(id, 0); return err },
		func() error { return s.SavePlan(testPlan(id)) },
		func() error {
			return s.UpdatePlan(id, func(*models.PlanGraph) error { t.Error("callback ran after seal"); return nil })
		},
		func() error { return s.DeletePlan(id) },
		func() error { return s.AppendEvent(id, models.PlanEvent{PlanID: id}) },
	}
	for i, check := range checks {
		if err := check(); !errors.Is(err, ErrStoreRecoveryRequired) {
			t.Fatalf("operation %d remains usable: %v", i, err)
		}
	}
}

func TestFilePlanStoreUncertainReplacementSealsAllAccess(t *testing.T) {
	for _, point := range []string{"replace_applied", "directory_sync"} {
		t.Run(point, func(t *testing.T) {
			s, path := newTestFileStore(t)
			plan := testPlan("uncertain")
			if err := s.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			if point == "replace_applied" {
				s.io.replace = func(from, to string) (bool, error) {
					applied, err := replacePlanStoreFile(from, to)
					if err != nil {
						return applied, err
					}
					return true, errInjectedStoreIO
				}
			} else {
				s.io.syncDir = func(string) error { return errInjectedStoreIO }
			}
			err := s.UpdatePlan(plan.ID, func(p *models.PlanGraph) error { p.Nodes[0].Name = "committed"; return nil })
			var commit *CommitError
			if !errors.Is(err, ErrCommitUnknown) || !errors.As(err, &commit) || commit.Operation != "update" || commit.PlanID != plan.ID || commit.Generation == "" {
				t.Fatalf("unknown context: %v", err)
			}
			assertStoreSealed(t, s, plan.ID)
			assertPlanName(t, reopenFileStore(t, path), plan.ID, "committed")
		})
	}
}

func TestFilePlanStoreNoopDoesNotTouchDisk(t *testing.T) {
	s, path := newTestFileStore(t)
	plan := testPlan("noop")
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	snapshot, log := readStoreFile(t, s.snapshotPath(plan.ID)), readStoreFile(t, path)
	s.io.createTemp = func(string, string) (durableFile, error) {
		t.Error("no-op created a temp file")
		return nil, errInjectedStoreIO
	}
	s.io.openLog = func(string) (durableFile, error) { t.Error("no-op opened log"); return nil, errInjectedStoreIO }
	if err := s.UpdatePlan(plan.ID, func(*models.PlanGraph) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapshot, readStoreFile(t, s.snapshotPath(plan.ID))) || !bytes.Equal(log, readStoreFile(t, path)) {
		t.Fatal("no-op changed files")
	}
}

func TestFilePlanStoreJSONEquivalentUpdatePublishesOwnedMemory(t *testing.T) {
	s, path := newTestFileStore(t)
	plan := testPlan("json-equivalent")
	plan.Nodes[0].Inputs = map[string]any{"number": int64(1)}
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	before := readStoreFile(t, s.snapshotPath(plan.ID))
	s.io.createTemp = func(string, string) (durableFile, error) {
		t.Error("JSON-equivalent update wrote snapshot")
		return nil, errInjectedStoreIO
	}
	var retained *models.PlanGraph
	if err := s.UpdatePlan(plan.ID, func(candidate *models.PlanGraph) error {
		candidate.Nodes[0].Inputs["number"] = float64(1)
		candidate.Nodes[0].RuntimeActionIDs = []string{}
		retained = candidate
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	retained.Nodes[0].Inputs["number"] = 999
	got, err := s.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Nodes[0].Inputs["number"].(float64); !ok {
		t.Fatalf("candidate type not published: %T", got.Nodes[0].Inputs["number"])
	}
	if got.Nodes[0].RuntimeActionIDs == nil {
		t.Fatal("candidate empty slice was discarded")
	}
	if !bytes.Equal(before, readStoreFile(t, s.snapshotPath(plan.ID))) {
		t.Fatal("equivalent candidate rewrote disk")
	}
	if _, err := reopenFileStore(t, path).GetPlan(plan.ID); err != nil {
		t.Fatal(err)
	}
}

func TestFilePlanStoreEventRollbackAndRetry(t *testing.T) {
	for _, point := range []string{"open", "seek", "write", "short_write", "sync"} {
		t.Run(point, func(t *testing.T) {
			s, path := newTestFileStore(t)
			plan := testPlan("event-rollback")
			if err := s.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			before := readStoreFile(t, path)
			open := s.io.openLog
			s.io.openLog = func(path string) (durableFile, error) {
				if point == "open" {
					return nil, errInjectedStoreIO
				}
				file, err := open(path)
				if err != nil {
					return nil, err
				}
				wrapped := &faultDurableFile{durableFile: file}
				switch point {
				case "seek":
					wrapped.seek = func(int64, int) (int64, error) { return 0, errInjectedStoreIO }
				case "write":
					wrapped.write = func(raw []byte) (int, error) { n, _ := file.Write(raw[:len(raw)/2]); return n, errInjectedStoreIO }
				case "short_write":
					wrapped.write = func(raw []byte) (int, error) { return file.Write(raw[:len(raw)/2]) }
				case "sync":
					calls := 0
					wrapped.sync = func() error {
						calls++
						if calls == 1 {
							return errInjectedStoreIO
						}
						return file.Sync()
					}
				}
				return wrapped, nil
			}
			if returned, err := s.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "test"}); !errors.Is(err, ErrNotCommitted) || returned.Seq != 0 {
				t.Fatalf("failed event acknowledged: %+v %v", returned, err)
			}
			if !bytes.Equal(before, readStoreFile(t, path)) {
				t.Fatal("rollback did not restore log prefix")
			}
			for _, current := range []*FilePlanStore{s, reopenFileStore(t, path)} {
				events, err := current.ListEvents(plan.ID)
				if err != nil || len(events) != 0 {
					t.Fatalf("failed event visible: %+v %v", events, err)
				}
			}
			s.io = defaultFileStoreIO()
			event, err := s.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID})
			if err != nil || event.Seq != 1 {
				t.Fatalf("retry: %+v %v", event, err)
			}
			events, err := reopenFileStore(t, path).ListEvents(plan.ID)
			if err != nil || len(events) != 1 || events[0].Seq != 1 {
				t.Fatalf("retry not durable: %+v %v", events, err)
			}
		})
	}
}

func TestFilePlanStoreEventRollbackFailureSeals(t *testing.T) {
	for _, point := range []string{"truncate", "rollback_sync"} {
		t.Run(point, func(t *testing.T) {
			s, path := newTestFileStore(t)
			plan := testPlan("event-unknown")
			if err := s.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			open := s.io.openLog
			s.io.openLog = func(path string) (durableFile, error) {
				file, err := open(path)
				if err != nil {
					return nil, err
				}
				wrapped := &faultDurableFile{durableFile: file}
				wrapped.write = func(raw []byte) (int, error) { n, _ := file.Write(raw[:len(raw)/2]); return n, errInjectedStoreIO }
				if point == "truncate" {
					wrapped.truncate = func(int64) error { return errInjectedStoreIO }
				} else {
					wrapped.sync = func() error { return errInjectedStoreIO }
				}
				return wrapped, nil
			}
			if _, err := s.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID}); !errors.Is(err, ErrCommitUnknown) {
				t.Fatalf("want unknown: %v", err)
			}
			assertStoreSealed(t, s, plan.ID)
			recovered := reopenFileStore(t, path)
			events, err := recovered.ListEvents(plan.ID)
			if err != nil || len(events) != 0 {
				t.Fatalf("recovery: %+v %v", events, err)
			}
		})
	}
}

func TestFilePlanStoreCommittedEventCloseErrorIsDiagnostic(t *testing.T) {
	s, path := newTestFileStore(t)
	plan := testPlan("close-diagnostic")
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	open := s.io.openLog
	s.io.openLog = func(path string) (durableFile, error) {
		file, err := open(path)
		if err != nil {
			return nil, err
		}
		return &faultDurableFile{durableFile: file, close: func() error { _ = file.Close(); return errInjectedStoreIO }}, nil
	}
	event, err := s.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID})
	if err != nil || event.Seq != 1 {
		t.Fatalf("committed append reported failure: %+v %v", event, err)
	}
	if err := s.Health(); err != nil {
		t.Fatal(err)
	}
	events, err := reopenFileStore(t, path).ListEvents(plan.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("committed event lost: %+v %v", events, err)
	}
}

func TestFilePlanStoreRejectsFilenameCollision(t *testing.T) {
	s, path := newTestFileStore(t)
	first := testPlan("a/b")
	if err := s.SavePlan(first); err != nil {
		t.Fatal(err)
	}
	if err := s.SavePlan(testPlan("a_b")); err == nil {
		t.Fatal("accepted colliding ids")
	}
	assertPlanName(t, reopenFileStore(t, path), first.ID, first.Nodes[0].Name)
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestRecords(t *testing.T, path string, records ...fileLogRecord) {
	t.Helper()
	var buffer bytes.Buffer
	for _, record := range records {
		raw, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		buffer.Write(raw)
		buffer.WriteByte('\n')
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Keep imports for the syscall-shaped fault hooks explicit in compilation.
var _ io.Writer = (*faultDurableFile)(nil)
