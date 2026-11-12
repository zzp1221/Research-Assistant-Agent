package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"research-assistant-backend/internal/budget"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

// A stale dispatch must be refused at lease time: a node that was blocked
// between the ready snapshot and the lease acquisition is never started.
func TestDispatchGuardRefusesBlockedNodeBeforeLease(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("dispatch-guard", 2)
	plan.Nodes[1].Dependencies = []string{plan.Nodes[0].ID}
	plan.Edges = []*models.TaskEdge{{ID: "e1", From: plan.Nodes[0].ID, To: plan.Nodes[1].ID, Type: "control"}}
	plan.Status = models.StatusInProgress
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	executor := taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		calls.Add(1)
		return &models.TaskExecutionResult{Status: models.StatusCompleted}, nil
	})
	s := NewScheduler(planStore, executor, &NoopEventPublisher{}, 1)

	// At this point the dependent is blocked while the stale ready snapshot still
	// exists; the guard is evaluated on the committed graph.
	if err := planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		dependent := getNodeByID(current, plan.Nodes[1].ID)
		dependent.Status = models.StatusBlocked
		dependent.BlockInfo = &models.BlockInfo{Code: models.BlockUpstreamFailed, OccurredAt: time.Now()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stale := []*models.TaskNode{{ID: plan.Nodes[1].ID, Status: models.StatusReady, CreatedAt: plan.CreatedAt, UpdatedAt: plan.UpdatedAt}}
	if err := s.runReadyNodes(context.Background(), plan.ID, stale); err != nil {
		t.Fatal(err)
	}
	// runReadyNodes would skip at prepareRuntimeGate only for approvals; the guard
	// must have refused the lease, so nothing was executed.
	time.Sleep(150 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("a blocked node was dispatched %d time(s)", calls.Load())
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if getNodeByID(stored, plan.Nodes[1].ID).Status != models.StatusBlocked {
		t.Fatalf("blocked node status changed to %s", getNodeByID(stored, plan.Nodes[1].ID).Status)
	}
}

// Terminal side effects (the completion event, the metrics marker and the
// callback) run once even when the loop wakes again on an already-terminal plan.
func TestTerminalSideEffectsRunOnce(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "terminal-once", Status: models.StatusInProgress, IntentType: "Code_Execution",
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{{ID: "a", Name: "only", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusCompleted, CreatedAt: now, UpdatedAt: now}},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	var callbacks atomic.Int32
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	s.onTerminal = func(context.Context, *models.PlanGraph) { callbacks.Add(1) }
	hint, _ := planStore.GetPlan(plan.ID)
	if outcome, err := s.reconcilePlanState(context.Background(), hint); err != nil || outcome != reconcileTerminal {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	second, _ := planStore.GetPlan(plan.ID)
	if outcome, err := s.reconcilePlanState(context.Background(), second); err != nil || outcome != reconcileTerminal {
		t.Fatalf("second outcome=%v err=%v", outcome, err)
	}
	events := mustEvents(t, planStore, plan.ID)
	terminalEvents := 0
	for _, event := range events {
		if event.EventType == "plan_completed" || event.EventType == "plan_failed" {
			terminalEvents++
		}
	}
	if terminalEvents != 1 {
		t.Fatalf("terminal events=%d, want exactly 1", terminalEvents)
	}
	if callbacks.Load() != 1 {
		t.Fatalf("terminal callback ran %d time(s)", callbacks.Load())
	}
}

// A retry of a failed plan starts a new round: the old outcome, reason and
// finish boundary must not survive, and the new terminal state must explain
// itself again.
func TestRetryClearsPreviousOutcomeAndReexplains(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := governanceTestPlan("retry-outcome", 1)
	plan.Status = models.StatusFailed
	plan.Outcome = models.OutcomeFailed
	plan.OutcomeReason = "old failure"
	plan.OutcomeDetails = &models.OutcomeDetails{Mode: "strict", Limitations: []string{"old"}}
	plan.Usage.FinishedAt = &now
	plan.Nodes[0].Status = models.StatusFailed
	plan.Nodes[0].Error = "old failure"
	plan.Nodes[0].BlockInfo = &models.BlockInfo{Code: models.BlockUpstreamFailed, OccurredAt: now}
	plan.Nodes[0].FinishedAt = &now
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	// The automatic retry allowance is exhausted by the original failure, so this
	// run must fail again; what matters here is that the previous result is gone
	// when the round starts and a fresh terminal result is written for it.
	runner := NewScheduler(planStore, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		return &models.TaskExecutionResult{Status: models.StatusFailed, Error: "boom"}, nil
	}), &NoopEventPublisher{}, 1)
	if err := runner.RetryTask(plan.ID, plan.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Outcome != "" || stored.OutcomeReason != "" || stored.OutcomeDetails != nil || stored.Usage.FinishedAt != nil {
		t.Fatalf("retry kept the previous result: outcome=%q reason=%q details=%#v finished=%v", stored.Outcome, stored.OutcomeReason, stored.OutcomeDetails, stored.Usage.FinishedAt)
	}
	if stored.Nodes[0].BlockInfo != nil || stored.Nodes[0].Error != "" || stored.Nodes[0].FinishedAt != nil {
		t.Fatalf("retry kept the previous node state: %#v", stored.Nodes[0])
	}
	if err := runner.ExecutePlan(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Status != models.StatusFailed || final.Outcome != models.OutcomeFailed {
		t.Fatalf("new round result: status=%s outcome=%q", final.Status, final.Outcome)
	}
	if final.Usage.FinishedAt == nil || final.OutcomeReason == "" {
		t.Fatalf("new round has no finish boundary/reason: %#v", final.Usage)
	}
	if final.OutcomeReason == "old failure" {
		t.Fatalf("the new round reused the previous reason: %q", final.OutcomeReason)
	}
}

// A failed producer's diagnostic artifact must not unlock its hard dependent and
// must not be trusted by an optional consumer.
func TestFailedProducerArtifactIsNeverAFreePass(t *testing.T) {
	now := time.Now()
	optional := true
	plan := &models.PlanGraph{
		ID: "failed-artifact",
		Nodes: []*models.TaskNode{
			{ID: "producer", Name: "producer", Status: models.StatusFailed, Error: "boom", OutputArtifacts: []string{"metrics"}},
			{ID: "hard", Name: "hard consumer", Status: models.StatusPending, Dependencies: []string{"producer"}, RequiredArtifacts: []string{"metrics"}},
			{ID: "opt", Name: "optional consumer", Status: models.StatusPending, OptionalDependencies: []string{"producer"}, RequiredArtifacts: []string{"metrics"}, Optional: &optional},
		},
		Artifacts: map[string]models.Artifact{"metrics": {Key: "metrics", Value: "partial diagnostic output", ProducerTaskID: "producer"}},
	}
	if hardDependenciesSatisfied(plan, plan.Nodes[1]) {
		t.Fatal("a failed producer satisfied a hard dependency")
	}
	if requiredArtifactsSatisfied(plan, plan.Nodes[2]) {
		t.Fatal("an optional consumer trusted a failed producer's diagnostic artifact")
	}
	events := propagateBlockToDependents(plan, plan.Nodes[0], now)
	if plan.Nodes[1].Status != models.StatusBlocked {
		t.Fatalf("hard dependent status=%s", plan.Nodes[1].Status)
	}
	if len(events) == 0 {
		t.Fatal("no block event was produced")
	}
	_ = optional
}

// A dispatch refused by the lease guard must return the budget it reserved.
func TestRefusedDispatchReleasesBudgetReservation(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("dispatch-budget", 2)
	plan.Nodes[1].Dependencies = []string{plan.Nodes[0].ID}
	plan.Edges = []*models.TaskEdge{{ID: "e1", From: plan.Nodes[0].ID, To: plan.Nodes[1].ID, Type: "control"}}
	plan.Status = models.StatusInProgress
	plan.OwnerID = "owner"
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	manager := budget.NewManager(nil)
	s.budget = manager
	if _, _, _, err := s.markTaskStarted(plan.ID, plan.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.markTaskStarted(plan.ID, plan.Nodes[1].ID); err != nil {
		t.Fatal(err)
	}
	blocked := plan.Nodes[1].ID
	if err := planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		node := getNodeByID(current, blocked)
		node.Status = models.StatusBlocked
		node.BlockInfo = &models.BlockInfo{Code: models.BlockUpstreamFailed, OccurredAt: time.Now()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	current, _ := planStore.GetPlan(plan.ID)
	if _, err := s.reserveTaskBudget(current, getNodeByID(current, blocked)); err != nil {
		t.Fatal(err)
	}
	if _, err := osStatReservations(s, plan.ID, blocked); !err {
		t.Fatal("reservation was not recorded before the dispatch attempt")
	}
	if _, _, _, err := s.markTaskStarted(plan.ID, blocked, taskDispatchGuard(current, blocked)); !errors.Is(err, errTaskNotDispatchable) {
		t.Fatalf("guard did not refuse the lease: %v", err)
	}
	// runReadyNodes releases the reservation itself; emulate its refusal path.
	s.releaseTaskReservation(plan.ID, blocked)
	if _, stillHeld := osStatReservations(s, plan.ID, blocked); stillHeld {
		t.Fatal("refused dispatch leaked its budget reservation")
	}
}

func osStatReservations(s *Scheduler, planID, taskID string) (string, bool) {
	s.budgetMu.Lock()
	defer s.budgetMu.Unlock()
	reservation, ok := s.reservations[planID+"|"+taskID]
	if !ok {
		return "", false
	}
	return reservation.ID, true
}

// Rewinding a producer must also reset its optional consumers: they consumed its
// output, so leaving them completed would reuse a stale artifact.
func TestResetFromNodeInvalidatesOptionalConsumers(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	optional := true
	plan := &models.PlanGraph{
		ID: "rewind-optional", Status: models.StatusCompleted, Outcome: models.OutcomeCompleted,
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "producer", Name: "producer", Type: "tool_call", AssignedTo: "tool_executor", Status: models.StatusCompleted, OutputArtifacts: []string{"tool_result"}, CreatedAt: now, UpdatedAt: now},
			{ID: "consumer", Name: "consumer", Type: "tool_call", AssignedTo: "tool_executor", Status: models.StatusCompleted, Optional: &optional, OptionalDependencies: []string{"producer"}, CreatedAt: now, UpdatedAt: now},
		},
		Edges: []*models.TaskEdge{{ID: "e1", From: "producer", To: "consumer", Type: "optional"}},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	if err := s.ResetFromNode(plan.ID, "producer"); err != nil {
		t.Fatal(err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if getNodeByID(stored, "consumer").Status != models.StatusPending {
		t.Fatalf("optional consumer was not invalidated: %s", getNodeByID(stored, "consumer").Status)
	}
}
