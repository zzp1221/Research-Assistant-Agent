package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

func newTestFileStore(t *testing.T) (*FilePlanStore, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")
	store, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatalf("new file store: %v", err)
	}
	return store, path
}

func TestUpdatePlanSkipsUnchangedSnapshot(t *testing.T) {
	store, path := newTestFileStore(t)
	plan := &models.PlanGraph{
		ID:        "plan-1",
		Status:    models.StatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Nodes:     []*models.TaskNode{},
		Artifacts: map[string]models.Artifact{},
	}
	if err := store.SavePlan(plan); err != nil {
		t.Fatalf("save: %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	// No-op update must not append a log record.
	if err := store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error { return nil }); err != nil {
		t.Fatalf("noop update: %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat log after: %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("no-op update wrote to the log: %d -> %d bytes", before.Size(), after.Size())
	}
}

func TestAppendEventAssignsMonotonicSeq(t *testing.T) {
	store, _ := newTestFileStore(t)
	plan := &models.PlanGraph{
		ID:        "plan-2",
		Status:    models.StatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Nodes:     []*models.TaskNode{},
		Artifacts: map[string]models.Artifact{},
	}
	if err := store.SavePlan(plan); err != nil {
		t.Fatalf("save: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := store.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "test_event"}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	events, err := store.ListEvents(plan.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 events, got %d", len(events))
	}
	for index, event := range events {
		if event.Seq != int64(index+1) {
			t.Fatalf("event %d has seq %d", index, event.Seq)
		}
	}
	incremental, err := store.ListEventsAfter(plan.ID, 1)
	if err != nil {
		t.Fatalf("list after: %v", err)
	}
	if len(incremental) != 2 || incremental[0].Seq != 2 {
		t.Fatalf("unexpected incremental events: %+v", incremental)
	}
}

func TestDeletePlanRemovesSnapshotAndEvents(t *testing.T) {
	store, _ := newTestFileStore(t)
	plan := &models.PlanGraph{
		ID:        "plan-3",
		Status:    models.StatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Nodes:     []*models.TaskNode{},
		Artifacts: map[string]models.Artifact{},
	}
	if err := store.SavePlan(plan); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "x"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := store.DeletePlan(plan.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetPlan(plan.ID); err == nil {
		t.Fatalf("deleted plan is still readable")
	}
	if _, err := store.ListEvents(plan.ID); err == nil {
		t.Fatalf("deleted plan events are still readable")
	}
}
