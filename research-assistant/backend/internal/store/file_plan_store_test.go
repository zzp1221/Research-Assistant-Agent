package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

func TestFilePlanStorePersistsPlansAndEvents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "plans.json")
	plan := testPlan("persisted-plan")
	plan.TraceID = "trace-1"
	plan.Nodes[0].Contract = models.TaskContract{
		Version:         models.TaskContractVersion,
		InputArtifacts:  []string{"paper"},
		OutputArtifacts: []string{"report"},
	}

	first, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := first.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, TraceID: plan.TraceID, EventType: "plan_created"}); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TraceID != "trace-1" || loaded.Nodes[0].Contract.Version != models.TaskContractVersion {
		t.Fatalf("runtime metadata was not persisted: %#v", loaded)
	}
	events, err := reopened.ListEvents(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "plan_created" {
		t.Fatalf("events were not persisted: %#v", events)
	}

	loaded.Nodes[0].Name = "mutated outside store"
	again, _ := reopened.GetPlan(plan.ID)
	if again.Nodes[0].Name == loaded.Nodes[0].Name {
		t.Fatal("GetPlan must return a deep clone")
	}
}

func TestFilePlanStoreAppendsWithoutRewriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	planStore, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	plan := testPlan("append-only-plan")
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := planStore.AppendEvent(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "plan_created"}); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(second), string(first)) {
		t.Fatal("append-only store rewrote its existing prefix")
	}
	if len(second) <= len(first) {
		t.Fatal("event append did not grow the log")
	}
}

func TestFilePlanStoreIgnoresTruncatedTrailingRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	planStore, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	plan := testPlan("truncated-log-plan")
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"kind":"plan","plan":`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	reopened, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.GetPlan(plan.ID); err != nil {
		t.Fatalf("valid prefix should survive a truncated tail: %v", err)
	}
}

func TestFilePlanStoreMigratesLegacySnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	plan := testPlan("legacy-plan")
	snapshot := fileSnapshot{
		Plans: map[string]*models.PlanGraph{plan.ID: plan},
		Events: map[string][]models.PlanEvent{
			plan.ID: {{PlanID: plan.ID, EventType: "plan_created"}},
		},
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.GetPlan(plan.ID); err != nil {
		t.Fatal(err)
	}
	events, err := reopened.ListEvents(plan.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("legacy events were not migrated: %#v, %v", events, err)
	}
	matches, err := filepath.Glob(path + ".legacy-*")
	if err != nil || len(matches) != 1 {
		t.Fatalf("legacy snapshot was not rotated: %#v, %v", matches, err)
	}
}

func TestRecoverInterruptedPlansInvalidatesExecutionLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	planStore, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	plan := testPlan("interrupted-plan")
	now := time.Now()
	plan.Status = models.StatusInProgress
	plan.Nodes[0].Status = models.StatusInProgress
	plan.Nodes[0].ExecutionID = "old-execution"
	plan.Nodes[0].LeaseOwner = "coder-agent"
	plan.Nodes[0].LeaseExpiresAt = &now
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}

	if err := RecoverInterruptedPlans(planStore); err != nil {
		t.Fatal(err)
	}
	recovered, err := planStore.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != models.StatusPending || recovered.Nodes[0].Status != models.StatusPending {
		t.Fatalf("interrupted work was not made runnable: plan=%s task=%s", recovered.Status, recovered.Nodes[0].Status)
	}
	if recovered.Nodes[0].ExecutionID != "" || recovered.Nodes[0].LeaseOwner != "" || recovered.Nodes[0].LeaseExpiresAt != nil {
		t.Fatalf("stale lease was not invalidated: %#v", recovered.Nodes[0])
	}
}

func TestFilePlanStoreCompactsSnapshotsAndLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	store, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	plan := testPlan("compact-plan")
	plan.Artifacts["big"] = models.Artifact{Key: "big", Type: "text", Value: strings.Repeat("a", 1<<20)}
	if err := store.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if err := store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
			current.Nodes[0].Result = strings.Repeat("r", 256*1024)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 64*1024 {
		t.Fatalf("compact log grew to %d bytes, want <64KB", info.Size())
	}
	snapshots, err := filepath.Glob(path + ".d/*.json")
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("expected exactly one plan snapshot, got %#v (%v)", snapshots, err)
	}
	snapshotInfo, err := os.Stat(snapshots[0])
	if err != nil {
		t.Fatal(err)
	}
	if snapshotInfo.Size() < 1<<20 {
		t.Fatalf("snapshot unexpectedly small: %d", snapshotInfo.Size())
	}
	reopened, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Nodes[0].Result) != 256*1024 {
		t.Fatalf("snapshot lost node result: %d", len(loaded.Nodes[0].Result))
	}
}

func TestFilePlanStoreMigratesLegacyAppendOnlyLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plans.json")
	plan := testPlan("legacy-log-plan")
	plan.Nodes[0].Name = "before"
	first, _ := json.Marshal(fileLogRecord{Kind: "plan", Plan: plan})
	plan.Nodes[0].Name = "after"
	second, _ := json.Marshal(fileLogRecord{Kind: "plan", Plan: plan})
	if err := os.WriteFile(path, append(append(first, byte(10)), append(second, byte(10))...), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFilePlanStore(path)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Nodes[0].Name != "after" {
		t.Fatalf("latest legacy plan not migrated: %q", loaded.Nodes[0].Name)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 4096 {
		t.Fatalf("new compact log is %d bytes, want <4KB", info.Size())
	}
	matches, err := filepath.Glob(path + ".legacy-*.gz")
	if err != nil || len(matches) != 1 {
		t.Fatalf("legacy log was not compressed: %#v (%v)", matches, err)
	}
}

func testPlan(id string) *models.PlanGraph {
	now := time.Now()
	return &models.PlanGraph{
		ID:        id,
		Status:    models.StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
		Nodes: []*models.TaskNode{{
			ID:         "task-1",
			Name:       "Run experiment",
			Status:     models.StatusPending,
			AssignedTo: "coder-agent",
			CreatedAt:  now,
			UpdatedAt:  now,
		}},
		Artifacts: map[string]models.Artifact{},
	}
}
