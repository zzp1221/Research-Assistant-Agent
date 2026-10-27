package scheduler

import (
	"encoding/json"
	"testing"
	"time"

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

func networkGateTestPlan() *models.PlanGraph {
	spec, _ := json.Marshal(models.ReproductionRuntimeSpec{
		Version:       "runtime.spec/v1",
		Language:      "python",
		BaseImage:     "python:3.11-bullseye",
		NetworkAccess: true,
	})
	return &models.PlanGraph{
		ID:         "plan-network-gate",
		IntentType: "Paper_Reproduction",
		Status:     models.StatusInProgress,
		RuntimePolicy: models.RuntimePolicy{
			Mode: models.RuntimePermissionConfirmImportant,
		},
		Artifacts: map[string]models.Artifact{
			"reproduction_runtime_spec": {Key: "reproduction_runtime_spec", Value: string(spec)},
		},
		Nodes: []*models.TaskNode{{
			ID:         "task-execute",
			Type:       "paper_code_execute",
			AssignedTo: "research_coding_agent",
			Status:     models.StatusReady,
		}},
	}
}

func TestNetworkAccessGateCreatedForExecutionTask(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := networkGateTestPlan()
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	action, pending, err := runner.prepareRuntimeGate(plan.ID, plan.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !pending || action == nil || action.Kind != models.RuntimeActionNetworkAccess {
		t.Fatalf("action=%#v pending=%v", action, pending)
	}
	if action.Title != "运行阶段访问网络" {
		t.Fatalf("title=%q", action.Title)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Status != models.StatusAwaitingApproval {
		t.Fatalf("plan status=%s", stored.Status)
	}
	if stored.Nodes[0].Status != models.StatusAwaitingApproval {
		t.Fatalf("task status=%s", stored.Nodes[0].Status)
	}
}

func TestRuntimeActionRejectionBlocksPlan(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := networkGateTestPlan()
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	action, _, err := runner.prepareRuntimeGate(plan.ID, plan.Nodes[0].ID)
	if err != nil || action == nil {
		t.Fatalf("prepare gate: %v", err)
	}
	if err := planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		for index := range current.RuntimeActions {
			if current.RuntimeActions[index].ID == action.ID {
				current.RuntimeActions[index].Status = models.RuntimeActionRejected
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_, pending, err := runner.prepareRuntimeGate(plan.ID, plan.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("rejected action must not keep the plan pending")
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Status != models.StatusFailed || stored.Outcome != models.OutcomeBlocked {
		t.Fatalf("plan status=%s outcome=%s", stored.Status, stored.Outcome)
	}
	if stored.Nodes[0].Status != models.StatusBlocked {
		t.Fatalf("task status=%s", stored.Nodes[0].Status)
	}
	found := false
	for _, missing := range stored.MissingRequirements {
		if missing.Kind == "user_rejected" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing user_rejected requirement: %#v", stored.MissingRequirements)
	}
}

func TestPriorNetworkApprovalAutoApprovesLaterTasks(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := networkGateTestPlan()
	now := time.Now()
	plan.RuntimeActions = []models.RuntimeActionRequest{{
		ID: "ra-prior", TaskID: "another-task", Kind: models.RuntimeActionNetworkAccess,
		Status: models.RuntimeActionApproved, RequestedAt: now, DecidedAt: &now, DecidedBy: "tester",
	}}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	action, pending, err := runner.prepareRuntimeGate(plan.ID, plan.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("a prior network approval must approve the new network action")
	}
	if action == nil || !action.AutoApproved || action.DecidedBy != "prior_approval" {
		t.Fatalf("action=%#v", action)
	}
}
