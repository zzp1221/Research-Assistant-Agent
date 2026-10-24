package scheduler

import (
	"encoding/json"
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

func runtimeGateTestPlan(mode models.RuntimePermissionMode) *models.PlanGraph {
	spec := models.ReproductionRuntimeSpec{
		Version:            "reproduction.runtime/v1",
		Language:           "python",
		BaseImage:          "python:3.11-bullseye",
		PythonDependencies: []models.PackageSpec{{Name: "mock", Version: "2.0.0"}},
	}
	specJSON, _ := json.Marshal(spec)
	depsJSON, _ := json.Marshal([]string{"mock==2.0.0"})
	return &models.PlanGraph{
		ID:     "plan-runtime-gate",
		Status: models.StatusInProgress,
		RuntimePolicy: models.RuntimePolicy{
			Mode: mode,
		},
		Artifacts: map[string]models.Artifact{
			"reproduction_runtime_spec": {Key: "reproduction_runtime_spec", Value: string(specJSON)},
			"dependency_spec":           {Key: "dependency_spec", Value: string(depsJSON)},
		},
		Nodes: []*models.TaskNode{{
			ID:                "task-install",
			Type:              "install_dependencies",
			AssignedTo:        "sandbox_agent",
			Status:            models.StatusReady,
			RequiredArtifacts: []string{"reproduction_runtime_spec", "dependency_spec"},
			OutputArtifacts:   []string{"prepared_runtime", "dependency_install_report"},
		}},
	}
}

func TestPrepareRuntimeGateConfirmCreatesPendingAction(t *testing.T) {
	st := store.NewMemoryPlanStore()
	plan := runtimeGateTestPlan(models.RuntimePermissionConfirmImportant)
	if err := st.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(st, nil, nil, 1)
	action, pending, err := s.prepareRuntimeGate(plan.ID, "task-install")
	if err != nil {
		t.Fatal(err)
	}
	if action == nil || action.Status != models.RuntimeActionPending || !pending {
		t.Fatalf("unexpected action: %#v pending=%v", action, pending)
	}
	updated, _ := st.GetPlan(plan.ID)
	if updated.Status != models.StatusAwaitingApproval {
		t.Fatalf("plan status=%s, want awaiting_approval", updated.Status)
	}
	if updated.Nodes[0].Status != models.StatusAwaitingApproval {
		t.Fatalf("task status=%s, want awaiting_approval", updated.Nodes[0].Status)
	}
}

func TestPrepareRuntimeGateFullAccessAutoApproves(t *testing.T) {
	st := store.NewMemoryPlanStore()
	plan := runtimeGateTestPlan(models.RuntimePermissionFullAccess)
	if err := st.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(st, nil, nil, 1)
	action, pending, err := s.prepareRuntimeGate(plan.ID, "task-install")
	if err != nil {
		t.Fatal(err)
	}
	if action == nil || action.Status != models.RuntimeActionApproved || !action.AutoApproved || pending {
		t.Fatalf("unexpected action: %#v pending=%v", action, pending)
	}
	updated, _ := st.GetPlan(plan.ID)
	if updated.Status != models.StatusInProgress {
		t.Fatalf("plan status=%s, want in_progress", updated.Status)
	}
	if updated.Nodes[0].Status != models.StatusReady {
		t.Fatalf("task status=%s, want ready", updated.Nodes[0].Status)
	}
}

func TestAllTerminalTreatsAwaitingApprovalAsNonTerminal(t *testing.T) {
	plan := &models.PlanGraph{Nodes: []*models.TaskNode{{ID: "t", Status: models.StatusAwaitingApproval}}}
	if allTerminal(plan) {
		t.Fatal("awaiting_approval must not be treated as terminal")
	}
}
