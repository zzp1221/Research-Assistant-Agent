package store

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

const crashChildPathEnv = "PLAN_STORE_CRASH_TEST_PATH"
const crashChildPointEnv = "PLAN_STORE_CRASH_TEST_POINT"

// The parent kills this process at a real filesystem boundary. Blocking on the
// parent's open stdin pipe makes the boundary deterministic without sleeps.
func awaitCrashParent() {
	fmt.Fprintln(os.Stdout, "PLAN_STORE_READY_TO_KILL")
	var one [1]byte
	_, _ = os.Stdin.Read(one[:])
	os.Exit(97)
}

func TestFilePlanStoreCrashChild(t *testing.T) {
	path := os.Getenv(crashChildPathEnv)
	if path == "" {
		t.Skip("subprocess helper")
	}
	point := os.Getenv(crashChildPointEnv)
	ops := defaultFileStoreIO()
	if strings.HasPrefix(point, "migration_") {
		replace := ops.replace
		ops.replace = func(from, to string) (bool, error) {
			if point == "migration_before_log" && to == path {
				awaitCrashParent()
			}
			applied, err := replace(from, to)
			if err == nil && ((point == "migration_after_plan" && filepath.Base(to) == "a.json") || (point == "migration_after_log" && to == path)) {
				awaitCrashParent()
			}
			return applied, err
		}
		if _, err := newFilePlanStore(path, ops); err != nil {
			t.Fatal(err)
		}
	} else {
		s, err := NewFilePlanStore(path)
		if err != nil {
			t.Fatal(err)
		}
		switch point {
		case "snapshot_before_replace", "snapshot_after_replace":
			replace := s.io.replace
			s.io.replace = func(from, to string) (bool, error) {
				if point == "snapshot_before_replace" {
					awaitCrashParent()
				}
				applied, err := replace(from, to)
				if err == nil {
					awaitCrashParent()
				}
				return applied, err
			}
			if err := s.UpdatePlan("crash", func(p *models.PlanGraph) error { p.Nodes[0].Name = "committed"; return nil }); err != nil {
				t.Fatal(err)
			}
		case "event_partial_write", "event_after_sync":
			open := s.io.openLog
			s.io.openLog = func(path string) (durableFile, error) {
				file, err := open(path)
				if err != nil {
					return nil, err
				}
				wrapped := &faultDurableFile{durableFile: file, sync: func() error {
					if err := file.Sync(); err != nil {
						return err
					}
					awaitCrashParent()
					return nil
				}}
				if point == "event_partial_write" {
					wrapped.write = func(raw []byte) (int, error) {
						n, err := file.Write(raw[:len(raw)/2])
						if err != nil {
							return n, err
						}
						if err := file.Sync(); err != nil {
							return n, err
						}
						awaitCrashParent()
						return n, nil
					}
				}
				return wrapped, nil
			}
			if err := s.AppendEvent("crash", models.PlanEvent{PlanID: "crash", EventType: "committed"}); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("unknown child boundary: %s", point)
		}
	}
	t.Fatal("child did not encounter crash boundary")
}

func killAtStoreBoundary(t *testing.T, path, point string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestFilePlanStoreCrashChild$", "-test.v")
	command.Env = append(os.Environ(), crashChildPathEnv+"="+path, crashChildPointEnv+"="+point)
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewScanner(stdout)
	ready := false
	for reader.Scan() {
		if reader.Text() == "PLAN_STORE_READY_TO_KILL" {
			ready = true
			break
		}
	}
	if !ready {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatalf("child did not reach %s (scan=%v context=%v)", point, reader.Err(), ctx.Err())
	}
	if err := command.Process.Kill(); err != nil {
		_ = command.Wait()
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil {
		t.Fatal("child exited successfully instead of being killed")
	}
}

func TestFilePlanStoreProcessDeathAtConfirmedWrite(t *testing.T) {
	for _, point := range []string{"snapshot_before_replace", "snapshot_after_replace", "event_partial_write", "event_after_sync"} {
		t.Run(point, func(t *testing.T) {
			s, path := newTestFileStore(t)
			plan := testPlan("crash")
			if err := s.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			killAtStoreBoundary(t, path, point)
			for i := 0; i < 2; i++ {
				s = reopenFileStore(t, path)
				if point == "snapshot_after_replace" {
					assertPlanName(t, s, plan.ID, "committed")
				} else if point == "snapshot_before_replace" {
					assertPlanName(t, s, plan.ID, plan.Nodes[0].Name)
				} else if point == "event_partial_write" {
					events, err := s.ListEvents(plan.ID)
					if err != nil || len(events) != 0 {
						t.Fatalf("unconfirmed partial event recovered: %+v %v", events, err)
					}
				} else {
					events, err := s.ListEvents(plan.ID)
					if err != nil || len(events) != 1 || events[0].Seq != 1 || events[0].EventType != "committed" {
						t.Fatalf("confirmed event lost: %+v %v", events, err)
					}
				}
			}
			if point == "event_partial_write" {
				event, err := s.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "retry"})
				if err != nil || event.Seq != 1 {
					t.Fatalf("tail repair prevented retry: %+v %v", event, err)
				}
				events, err := reopenFileStore(t, path).ListEvents(plan.ID)
				if err != nil || len(events) != 1 || events[0].EventType != "retry" {
					t.Fatalf("retry after crash lost: %+v %v", events, err)
				}
			}
		})
	}
}

func TestFilePlanStoreProcessDeathDuringMigration(t *testing.T) {
	for _, point := range []string{"migration_after_plan", "migration_before_log", "migration_after_log"} {
		t.Run(point, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plans.json")
			a, b := testPlan("a"), testPlan("b")
			writeTestRecords(t, path, fileLogRecord{Kind: "plan", Plan: a}, fileLogRecord{Kind: "plan", Plan: b}, fileLogRecord{Kind: "event", Event: &models.PlanEvent{PlanID: a.ID, Seq: 1}}, fileLogRecord{Kind: "event", Event: &models.PlanEvent{PlanID: b.ID, Seq: 3}})
			killAtStoreBoundary(t, path, point)
			for i := 0; i < 2; i++ {
				s := reopenFileStore(t, path)
				for _, plan := range []*models.PlanGraph{a, b} {
					assertPlanName(t, s, plan.ID, plan.Nodes[0].Name)
					events, err := s.ListEvents(plan.ID)
					if err != nil || len(events) != 1 {
						t.Fatalf("migration crash lost/duplicated events: %+v %v", events, err)
					}
				}
			}
		})
	}
}
