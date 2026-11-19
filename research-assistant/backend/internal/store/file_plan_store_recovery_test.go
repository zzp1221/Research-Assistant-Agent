package store

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestFilePlanStoreTombstoneGenerationAndSequenceFloor(t *testing.T) {
	s, path := newTestFileStore(t)
	plan := testPlan("reused")
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	firstGeneration := s.envelopes[plan.ID].Generation
	if _, err := s.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID, Seq: 40}); err != nil {
		t.Fatal(err)
	}
	for cycle := 0; cycle < 2; cycle++ {
		if err := s.DeletePlan(plan.ID); err != nil {
			t.Fatal(err)
		}
		for reopen := 0; reopen < 2; reopen++ {
			s = reopenFileStore(t, path)
			if _, err := s.GetPlan(plan.ID); !errors.Is(err, ErrPlanNotFound) {
				t.Fatalf("tombstone resurrected: %v", err)
			}
			if _, err := s.ListEvents(plan.ID); !errors.Is(err, ErrPlanNotFound) {
				t.Fatalf("deleted events readable: %v", err)
			}
			if s.envelopes[plan.ID].EventSeqFloor != 40 {
				t.Fatalf("floor regressed: %+v", s.envelopes[plan.ID])
			}
		}
		if err := s.SavePlan(plan); err != nil {
			t.Fatal(err)
		}
		if s.envelopes[plan.ID].Generation == firstGeneration {
			t.Fatal("recreation reused generation")
		}
		firstGeneration = s.envelopes[plan.ID].Generation
		s = reopenFileStore(t, path)
		events, err := s.ListEvents(plan.ID)
		if err != nil || len(events) != 0 {
			t.Fatalf("inherited old generation: %+v %v", events, err)
		}
	}
	event, err := s.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID})
	if err != nil || event.Seq != 41 {
		t.Fatalf("new seq: %+v %v", event, err)
	}
	after, err := reopenFileStore(t, path).ListEventsAfter(plan.ID, 40)
	if err != nil || len(after) != 1 || after[0].Seq != 41 {
		t.Fatalf("SSE cursor skipped new lifecycle: %+v %v", after, err)
	}
}

func TestFilePlanStoreLegacySnapshotUpdatePreservesEventsOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	plan := testPlan("legacy-live")
	writeTestJSON(t, path+".d/"+safePlanFileID(plan.ID)+".json", plan)
	writeTestRecords(t, path,
		fileLogRecord{Kind: "plan_saved", PlanID: plan.ID},
		fileLogRecord{Kind: "event", Event: &models.PlanEvent{PlanID: plan.ID, Seq: 1, EventType: "legacy"}},
	)
	s := reopenFileStore(t, path)
	if err := s.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "new-before-upgrade"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdatePlan(plan.ID, func(p *models.PlanGraph) error { p.Nodes[0].Name = "upgraded"; return nil }); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s = reopenFileStore(t, path)
		assertPlanName(t, s, plan.ID, "upgraded")
		events, err := s.ListEvents(plan.ID)
		if err != nil || len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
			t.Fatalf("legacy events lost after envelope: %+v %v", events, err)
		}
		if s.envelopes[plan.ID].Generation != legacyGeneration(plan.ID) {
			t.Fatal("legacy generation changed")
		}
	}
}

func TestFilePlanStoreV2AuthorityOverridesLegacyMarkers(t *testing.T) {
	for _, state := range []string{"active", "deleted"} {
		t.Run(state, func(t *testing.T) {
			s, path := newTestFileStore(t)
			plan := testPlan("authority")
			if err := s.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			if state == "deleted" {
				if err := s.DeletePlan(plan.ID); err != nil {
					t.Fatal(err)
				}
			}
			writeTestRecords(t, path,
				fileLogRecord{Kind: "plan", Plan: plan},
				fileLogRecord{Kind: "event", Event: &models.PlanEvent{PlanID: plan.ID, Seq: 1}},
				fileLogRecord{Kind: "plan_deleted", PlanID: plan.ID},
				fileLogRecord{Kind: "plan_saved", PlanID: plan.ID},
			)
			for i := 0; i < 2; i++ {
				s = reopenFileStore(t, path)
				if state == "deleted" {
					if _, err := s.GetPlan(plan.ID); !errors.Is(err, ErrPlanNotFound) {
						t.Fatalf("legacy resurrected v2 deletion: %v", err)
					}
				} else {
					assertPlanName(t, s, plan.ID, plan.Nodes[0].Name)
					events, err := s.ListEvents(plan.ID)
					if err != nil || len(events) != 0 {
						t.Fatalf("old generation leaked: %+v %v", events, err)
					}
				}
			}
		})
	}
}

func TestFilePlanStoreRepairsLogTailBeforeNextAppend(t *testing.T) {
	for _, tail := range []string{"missing_newline", "incomplete_json"} {
		t.Run(tail, func(t *testing.T) {
			s, path := newTestFileStore(t)
			plan := testPlan("tail")
			if err := s.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			if err := s.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "first"}); err != nil {
				t.Fatal(err)
			}
			raw := readStoreFile(t, path)
			if tail == "missing_newline" {
				raw = bytes.TrimSuffix(raw, []byte{'\n'})
			} else {
				raw = append(raw, []byte(`{"kind":"event","event":`)...)
			}
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			s = reopenFileStore(t, path)
			if !bytes.HasSuffix(readStoreFile(t, path), []byte{'\n'}) {
				t.Fatal("tail not repaired")
			}
			if err := s.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "second"}); err != nil {
				t.Fatal(err)
			}
			events, err := reopenFileStore(t, path).ListEvents(plan.ID)
			if err != nil || len(events) != 2 || events[1].Seq != 2 {
				t.Fatalf("tail repair lost append: %+v %v", events, err)
			}
		})
	}
}

func TestFilePlanStoreRejectsCorruptRecordsWithoutFallback(t *testing.T) {
	for _, point := range []string{"unknown_snapshot_version", "missing_snapshot_version", "bad_envelope_id", "invalid_generation", "bad_middle", "unknown_log_version", "event_id_mismatch", "nonmonotonic"} {
		t.Run(point, func(t *testing.T) {
			s, path := newTestFileStore(t)
			plan := testPlan("corrupt")
			if err := s.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			meta := s.envelopes[plan.ID]
			switch point {
			case "unknown_snapshot_version":
				meta.Format = 99
				writeTestJSON(t, s.snapshotPath(plan.ID), meta)
			case "missing_snapshot_version":
				raw := map[string]any{"plan_id": plan.ID, "generation": meta.Generation, "state": "active", "plan": plan}
				writeTestJSON(t, s.snapshotPath(plan.ID), raw)
			case "bad_envelope_id":
				meta.PlanID = "other"
				writeTestJSON(t, s.snapshotPath(plan.ID), meta)
			case "invalid_generation":
				meta.Generation = ""
				writeTestJSON(t, s.snapshotPath(plan.ID), meta)
			case "bad_middle":
				if err := os.WriteFile(path, []byte("broken\n{\"kind\":\"event\"}\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unknown_log_version":
				writeTestRecords(t, path, fileLogRecord{Format: 99, Kind: "event", PlanID: plan.ID, Generation: meta.Generation, Event: &models.PlanEvent{PlanID: plan.ID, Seq: 1}})
			case "event_id_mismatch":
				writeTestRecords(t, path, fileLogRecord{Format: 2, Kind: "event", PlanID: plan.ID, Generation: meta.Generation, Event: &models.PlanEvent{PlanID: "other", Seq: 1}})
			case "nonmonotonic":
				record := fileLogRecord{Format: 2, Kind: "event", PlanID: plan.ID, Generation: meta.Generation, Event: &models.PlanEvent{PlanID: plan.ID, Seq: 1}}
				writeTestRecords(t, path, record, record)
			}
			if _, err := NewFilePlanStore(path); err == nil {
				t.Fatal("opened corrupt data")
			}
		})
	}
}

func TestFilePlanStoreMigrationFailureReopensWithoutLoss(t *testing.T) {
	for _, point := range []string{"second_snapshot", "log_replace"} {
		t.Run(point, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "plans.json")
			a, b := testPlan("a"), testPlan("b")
			writeTestRecords(t, path, fileLogRecord{Kind: "plan", Plan: a}, fileLogRecord{Kind: "plan", Plan: b}, fileLogRecord{Kind: "event", Event: &models.PlanEvent{PlanID: a.ID, Seq: 1}}, fileLogRecord{Kind: "event", Event: &models.PlanEvent{PlanID: b.ID, Seq: 2}})
			original := readStoreFile(t, path)
			ops := defaultFileStoreIO()
			replace := ops.replace
			ops.replace = func(from, to string) (bool, error) {
				if (point == "second_snapshot" && filepath.Base(to) == "b.json") || (point == "log_replace" && to == path) {
					return false, errInjectedStoreIO
				}
				return replace(from, to)
			}
			if _, err := newFilePlanStore(path, ops); !errors.Is(err, ErrNotCommitted) {
				t.Fatalf("expected interrupted migration: %v", err)
			}
			if !bytes.Equal(original, readStoreFile(t, path)) {
				t.Fatal("failed migration discarded source log")
			}
			for i := 0; i < 2; i++ {
				s := reopenFileStore(t, path)
				for _, p := range []*models.PlanGraph{a, b} {
					assertPlanName(t, s, p.ID, p.Nodes[0].Name)
					events, err := s.ListEvents(p.ID)
					if err != nil || len(events) != 1 {
						t.Fatalf("partial migration lost events: %+v %v", events, err)
					}
				}
			}
		})
	}
}

func TestFilePlanStoreRejectsSequenceAndIdentityWithoutChanges(t *testing.T) {
	s, path := newTestFileStore(t)
	plan := testPlan("sequence")
	if err := s.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, Seq: math.MaxInt64}); err != nil {
		t.Fatal(err)
	}
	before := readStoreFile(t, path)
	for _, event := range []models.PlanEvent{{PlanID: plan.ID}, {PlanID: plan.ID, Seq: -1}, {PlanID: plan.ID, Seq: math.MaxInt64}, {PlanID: "other", Seq: 1}} {
		if _, err := s.AppendEventReturning(plan.ID, event); err == nil {
			t.Fatalf("accepted invalid event: %+v", event)
		}
	}
	if !bytes.Equal(before, readStoreFile(t, path)) {
		t.Fatal("invalid event changed log")
	}
	events, err := reopenFileStore(t, path).ListEvents(plan.ID)
	if err != nil || len(events) != 1 || events[0].Seq != math.MaxInt64 {
		t.Fatalf("recovered invalid events: %+v %v", events, err)
	}
}

func TestFilePlanStoreLegacyEventOnlySnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	plan := testPlan("event-only-snapshot")
	writeTestJSON(t, path+".d/"+safePlanFileID(plan.ID)+".json", plan)
	writeTestJSON(t, path, map[string]any{"events": map[string][]models.PlanEvent{plan.ID: {{PlanID: plan.ID, Seq: 1}}}})
	s := reopenFileStore(t, path)
	events, err := s.ListEvents(plan.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("legacy events-only snapshot lost: %+v %v", events, err)
	}
}

func TestFilePlanStoreLegacyUnsequencedEventsPreservePositiveCursors(t *testing.T) {
	for _, format := range []string{"whole_snapshot", "append_only", "snapshot_log"} {
		for _, sequences := range [][]int64{{0, 1}, {0, 0, 1}, {0, 1, 3}, {1, 0, 2}} {
			t.Run(fmt.Sprintf("%s/%v", format, sequences), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "plans.json")
				plan := testPlan("legacy-cursors")
				events := make([]models.PlanEvent, len(sequences))
				for index, seq := range sequences {
					events[index] = models.PlanEvent{PlanID: plan.ID, Seq: seq, EventType: fmt.Sprintf("original-%d", index)}
				}
				switch format {
				case "whole_snapshot":
					writeTestJSON(t, path, fileSnapshot{Plans: map[string]*models.PlanGraph{plan.ID: plan}, Events: map[string][]models.PlanEvent{plan.ID: events}})
				default:
					first := fileLogRecord{Kind: "plan", Plan: plan}
					if format == "snapshot_log" {
						writeTestJSON(t, path+".d/"+safePlanFileID(plan.ID)+".json", plan)
						first = fileLogRecord{Kind: "plan_saved", PlanID: plan.ID}
					}
					records := []fileLogRecord{first}
					for index := range events {
						records = append(records, fileLogRecord{Kind: "event", Event: &events[index]})
					}
					writeTestRecords(t, path, records...)
				}
				store := reopenFileStore(t, path)
				if err := store.UpdatePlan(plan.ID, func(*models.PlanGraph) error { return nil }); err != nil {
					t.Fatal(err)
				}
				for reopen := 0; reopen < 2; reopen++ {
					store = reopenFileStore(t, path)
					got, err := store.ListEvents(plan.ID)
					if err != nil || len(got) != len(events) {
						t.Fatalf("legacy events changed: %+v %v", got, err)
					}
					for index := range events {
						if got[index].Seq != events[index].Seq || got[index].EventType != events[index].EventType {
							t.Fatalf("renumbered or reordered legacy event %d: %+v", index, got[index])
						}
					}
					positive, err := store.ListEventsAfter(plan.ID, 0)
					if err != nil {
						t.Fatal(err)
					}
					var expected []int64
					for _, seq := range sequences {
						if seq > 0 {
							expected = append(expected, seq)
						}
					}
					if len(positive) != len(expected) {
						t.Fatalf("cursor zero changed: %+v", positive)
					}
					for index, seq := range expected {
						if positive[index].Seq != seq {
							t.Fatalf("positive cursor changed: %+v", positive)
						}
					}
				}
				last := sequences[len(sequences)-1]
				appended, err := store.AppendEventReturning(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "new"})
				if err != nil || appended.Seq != last+1 {
					t.Fatalf("new event ignored existing positive cursor: %+v %v", appended, err)
				}
				after, err := reopenFileStore(t, path).ListEventsAfter(plan.ID, last)
				if err != nil || len(after) != 1 || after[0].Seq != last+1 {
					t.Fatalf("reopened positive cursor changed: %+v %v", after, err)
				}
			})
		}
	}
}
