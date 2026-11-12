package scheduler

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

// TestApprovedWaitResumesAndCompletes executes the full wait -> approve ->
// continue path: the upstream pauses on a tool approval, the user approves it,
// and both the paused node and its dependent complete. It also proves the
// blocked dependents rule: nothing was blocked while the node waited.
func TestApprovedWaitResumesAndCompletes(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "approve-chain", IntentType: "General", Status: models.StatusPending, OwnerID: "owner",
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "n1", Name: "tool", Type: "tool_call", AssignedTo: "general_agent", Status: models.StatusPending, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5, Inputs: map[string]any{"tool_id": "demo"}},
			{ID: "n2", Name: "report", Type: "general_synthesis", AssignedTo: "general_agent", Status: models.StatusPending, Dependencies: []string{"n1"}, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5},
		},
		Edges: []*models.TaskEdge{{ID: "e1", From: "n1", To: "n2", Type: "control"}},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	// The first run pauses on an approval; the second runs after it is approved.
	waitOnce := true
	runner := NewScheduler(planStore, taskExecutorFunc(func(_ context.Context, current *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
		if task.ID == "n1" && waitOnce {
			waitOnce = false
			return &models.TaskExecutionResult{
				Status:        models.StatusAwaitingApproval,
				RuntimeAction: &models.RuntimeActionRequest{ID: "ra-tool", Kind: models.RuntimeActionToolCall, Title: "Approve tool demo", Status: models.RuntimeActionPending},
			}, nil
		}
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "ok:" + task.ID}, nil
	}), &NoopEventPublisher{}, 1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.ExecutePlan(ctx, plan.ID) }()

	// Wait until the plan is parked on the approval.
	var paused *models.PlanGraph
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		stored, err := planStore.GetPlan(plan.ID)
		if err == nil && stored.Status == models.StatusAwaitingApproval && len(stored.RuntimeActions) == 1 {
			paused = stored
			break
		}
	}
	if paused == nil {
		t.Fatal("scheduler never paused on the approval")
	}
	if paused.Nodes[0].Status != models.StatusAwaitingApproval {
		t.Fatalf("upstream status=%s", paused.Nodes[0].Status)
	}
	if paused.Nodes[1].Status != models.StatusPending || paused.Nodes[1].BlockInfo != nil {
		t.Fatalf("dependent of a waiting task was stopped: %#v", paused.Nodes[1])
	}
	if paused.Nodes[0].Failure != nil || paused.Nodes[0].FinishedAt != nil {
		t.Fatalf("wait recorded a failure: %#v", paused.Nodes[0])
	}

	// Approve the action exactly like the API decision endpoint does.
	if err := planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.RuntimeActions[0].Status = models.RuntimeActionApproved
		current.RuntimeActions[0].DecidedBy = "tester"
		task := getNodeByID(current, "n1")
		task.Status = models.StatusPending
		task.Error = ""
		task.FinishedAt = nil
		current.Status = models.StatusPending
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner.notifyProgress()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execution after approval: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("plan did not finish after the approval")
	}
	final, _ := planStore.GetPlan(plan.ID)
	if getNodeByID(final, "n1").Status != models.StatusCompleted || getNodeByID(final, "n2").Status != models.StatusCompleted {
		t.Fatalf("approved run did not complete: %v", nodeStatuses(final))
	}
	if final.Status != models.StatusCompleted || final.Outcome == "" {
		t.Fatalf("plan status=%s outcome=%q", final.Status, final.Outcome)
	}
	for _, event := range mustEvents(t, planStore, plan.ID) {
		if event.EventType == "task_blocked" {
			t.Fatalf("a wait produced a blocked event: %#v", event)
		}
	}
}

// TestStaleResultCannotTouchWaitingRecords proves the execution fence covers
// questions and approvals: an old execution cannot add either.
func TestStaleResultCannotTouchWaitingRecords(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan, executionID, owner, epoch := waitPlan(t, planStore, "stale-records")
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	approval := &models.TaskExecutionResult{
		Status:        models.StatusAwaitingApproval,
		RuntimeAction: &models.RuntimeActionRequest{ID: "ra-stale", Status: models.RuntimeActionPending},
	}
	if _, _, err := s.applyTaskResult(plan.ID, plan.Nodes[0].ID, executionID, owner, epoch+1, approval, nil, true); err == nil {
		t.Fatal("stale approval accepted")
	}
	if err := s.applyQuestionWaiting(plan.ID, plan.Nodes[0].ID, executionID, owner, epoch+1, &models.PlanQuestion{Prompt: "stale"}); err == nil {
		t.Fatal("stale question accepted")
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if len(stored.RuntimeActions) != 0 || len(stored.Questions) != 0 {
		t.Fatalf("stale records persisted: actions=%#v questions=%#v", stored.RuntimeActions, stored.Questions)
	}
	if stored.Nodes[0].Status != models.StatusInProgress {
		t.Fatalf("stale result changed the node status: %s", stored.Nodes[0].Status)
	}
}

// TestRetryDoesNotBlockIndependentBranch keeps the propagation scope honest: a
// failure only stops the nodes that hard-depend on it.
func TestRetryDoesNotBlockIndependentBranch(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "independent-branch", Status: models.StatusPending, OwnerID: "owner",
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "a", Name: "fails", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusPending, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5},
			{ID: "b", Name: "depends", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusPending, Dependencies: []string{"a"}, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5},
			{ID: "c", Name: "independent", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusPending, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5},
		},
		Edges: []*models.TaskEdge{{ID: "e1", From: "a", To: "b", Type: "control"}},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, taskExecutorFunc(func(_ context.Context, _ *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
		if task.ID == "a" {
			return &models.TaskExecutionResult{Status: models.StatusFailed, Error: "ModuleNotFoundError: torch"}, nil
		}
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "ok"}, nil
	}), &NoopEventPublisher{}, 1)
	if err := runner.ExecutePlan(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if getNodeByID(final, "c").Status != models.StatusCompleted {
		t.Fatalf("independent branch did not run: %s", getNodeByID(final, "c").Status)
	}
	if getNodeByID(final, "b").Status != models.StatusBlocked {
		t.Fatalf("hard dependent status=%s", getNodeByID(final, "b").Status)
	}
}
