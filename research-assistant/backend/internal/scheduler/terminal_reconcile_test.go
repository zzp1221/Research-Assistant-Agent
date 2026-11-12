package scheduler

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/store"
)

func terminalTestPlan(id string) *models.PlanGraph {
	now := time.Now()
	return &models.PlanGraph{
		ID: id, TraceID: "trace-" + id, Status: models.StatusPending,
		Budget:    models.RunBudget{MaxTaskAttempts: 10, MaxDurationSec: 30},
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
	}
}

func TestTerminalRaceWritesOutcomeAfterSnapshotGap(t *testing.T) {
	// A blocked upstream and a pending dependent. The scheduler's snapshot still
	// shows the dependent pending; the dependent is blocked before the decision
	// is committed. The decision must therefore re-read the committed graph,
	// recognize that everything ended, and still produce an outcome.
	planStore := store.NewMemoryPlanStore()
	plan := terminalTestPlan("terminal-race")
	now := time.Now()
	plan.Nodes = []*models.TaskNode{
		{ID: "a", Name: "upstream", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusBlocked, Error: "no repository", BlockInfo: &models.BlockInfo{Code: models.BlockUpstreamBlocked, OccurredAt: now, Causes: []models.BlockCause{{Code: models.BlockUpstreamBlocked, RootTaskID: "a", Status: models.StatusBlocked}}}, FinishedAt: &now, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5},
		{ID: "b", Name: "dependent", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusPending, Dependencies: []string{"a"}, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5},
	}
	plan.Edges = []*models.TaskEdge{{ID: "e1", From: "a", To: "b", Type: "control"}}
	plan.Status = models.StatusInProgress
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	// Simulate the worker finishing the dependent between the snapshot and the
	// decision: the committed graph already has every node terminal.
	if err := planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		dependent := getNodeByID(current, "b")
		dependent.Status = models.StatusBlocked
		dependent.BlockInfo = &models.BlockInfo{Code: models.BlockUpstreamBlocked, OccurredAt: now, Causes: []models.BlockCause{{Code: models.BlockUpstreamBlocked, RootTaskID: "a", ViaTaskID: "a", Status: models.StatusBlocked}}}
		dependent.Error = "blocked"
		dependent.FinishedAt = &now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hint, _ := planStore.GetPlan(plan.ID)
	outcome, err := s.reconcilePlanState(context.Background(), hint)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != reconcileTerminal {
		t.Fatalf("outcome=%v, want terminal", outcome)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Status != models.StatusFailed || final.Outcome != models.OutcomeBlocked {
		t.Fatalf("status=%s outcome=%s reason=%q", final.Status, final.Outcome, final.OutcomeReason)
	}
	if final.OutcomeReason == "" || final.Usage.FinishedAt == nil {
		t.Fatalf("terminal result has no reason/finish time: %#v", final.Usage)
	}
	completedEvents := 0
	for _, event := range mustEvents(t, planStore, plan.ID) {
		if event.EventType == "plan_failed" || event.EventType == "plan_completed" {
			completedEvents++
			if event.Payload["outcome"] == "" {
				t.Fatalf("terminal event has no outcome: %#v", event.Payload)
			}
		}
	}
	if completedEvents != 1 {
		t.Fatalf("terminal events=%d, want exactly 1", completedEvents)
	}
}

func TestFailedUpstreamStillProducesTerminalOutcome(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := terminalTestPlan("terminal-failed")
	plan.Nodes = []*models.TaskNode{
		{ID: "a", Name: "upstream", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusFailed, Error: "boom", FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
		{ID: "b", Name: "dependent", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusBlocked, Dependencies: []string{"a"}, BlockInfo: &models.BlockInfo{Code: models.BlockUpstreamFailed, OccurredAt: now, Causes: []models.BlockCause{{Code: models.BlockUpstreamFailed, RootTaskID: "a", Status: models.StatusFailed}}}, FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
	}
	plan.Edges = []*models.TaskEdge{{ID: "e1", From: "a", To: "b", Type: "control"}}
	plan.Status = models.StatusInProgress
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	hint, _ := planStore.GetPlan(plan.ID)
	if outcome, err := s.reconcilePlanState(context.Background(), hint); err != nil || outcome != reconcileTerminal {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Status != models.StatusFailed || final.Outcome != models.OutcomeFailed {
		t.Fatalf("status=%s outcome=%s", final.Status, final.Outcome)
	}
}

func TestRealDeadlockRecordsUnsatisfiedNodes(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := terminalTestPlan("terminal-deadlock")
	plan.Nodes = []*models.TaskNode{
		{ID: "a", Name: "upstream", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusFailed, Error: "boom", FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
		// Deliberately left pending with an unsatisfied hard dependency, which is
		// a dependency deadlock, not a propagated block.
		{ID: "b", Name: "orphan", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusPending, Dependencies: []string{"a"}, CreatedAt: now, UpdatedAt: now},
	}
	plan.Edges = []*models.TaskEdge{{ID: "e1", From: "a", To: "b", Type: "control"}}
	plan.Status = models.StatusInProgress
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	hint, _ := planStore.GetPlan(plan.ID)
	if outcome, err := s.reconcilePlanState(context.Background(), hint); err != nil || outcome != reconcileTerminal {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	orphan := getNodeByID(final, "b")
	if orphan.Status != models.StatusBlocked || orphan.BlockInfo == nil || orphan.BlockInfo.Code != models.BlockUnsatisfiedDependencies {
		t.Fatalf("orphan=%#v", orphan)
	}
	if final.Status != models.StatusFailed || final.Outcome == "" {
		t.Fatalf("status=%s outcome=%q", final.Status, final.Outcome)
	}
	if !containsReason(final.OutcomeReason, "b") {
		t.Fatalf("reason=%q does not name the unsatisfied node", final.OutcomeReason)
	}
}

func containsReason(value, needle string) bool {
	return len(value) > 0 && len(needle) > 0 && indexOf(value, needle) >= 0
}

func indexOf(value, needle string) int {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func TestAwaitingPlanIsNotDeadlock(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := terminalTestPlan("terminal-wait")
	plan.Nodes = []*models.TaskNode{
		{ID: "a", Name: "waiting", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusAwaitingApproval, CreatedAt: now, UpdatedAt: now},
	}
	plan.Status = models.StatusAwaitingApproval
	plan.RuntimeActions = []models.RuntimeActionRequest{{ID: "ra-1", TaskID: "a", Kind: models.RuntimeActionToolCall, Status: models.RuntimeActionPending, RequestedAt: now}}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	hint, _ := planStore.GetPlan(plan.ID)
	if outcome, err := s.reconcilePlanState(context.Background(), hint); err != nil || outcome != reconcileWait {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Status != models.StatusAwaitingApproval || final.Outcome != "" {
		t.Fatalf("status=%s outcome=%q", final.Status, final.Outcome)
	}
}

func TestCancelledStopReasonIsPreserved(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := terminalTestPlan("terminal-canceled")
	plan.Nodes = []*models.TaskNode{{ID: "a", Status: models.StatusCanceled, FinishedAt: &now, CreatedAt: now, UpdatedAt: now}}
	plan.Status = models.StatusCanceled
	plan.Outcome = models.OutcomeFailed
	plan.OutcomeReason = "user canceled"
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	hint, _ := planStore.GetPlan(plan.ID)
	if outcome, err := s.reconcilePlanState(context.Background(), hint); err != nil || outcome != reconcileTerminal {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Outcome != models.OutcomeFailed || final.OutcomeReason != "user canceled" {
		t.Fatalf("stop reason overwritten: %q", final.OutcomeReason)
	}
}

func TestPlanFinalizerIsInvokedForBlockedGraphs(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := terminalTestPlan("terminal-finalizer")
	plan.Status = models.StatusInProgress
	plan.Nodes = []*models.TaskNode{
		{ID: "a", Status: models.StatusBlocked, Error: "missing dataset", BlockInfo: &models.BlockInfo{Code: models.BlockUpstreamBlocked, OccurredAt: now}, FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	finalizer := &countingFinalizer{inner: &planner.PlanFinalizer{}}
	s.SetFinalizer(finalizer)
	hint, _ := planStore.GetPlan(plan.ID)
	if outcome, err := s.reconcilePlanState(context.Background(), hint); err != nil || outcome != reconcileTerminal {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	if finalizer.calls != 1 {
		t.Fatalf("finalizer calls=%d, want 1", finalizer.calls)
	}
	final, _ := planStore.GetPlan(plan.ID)
	// A blocked graph must never be reported as completed by the finalizer.
	if final.Status != models.StatusFailed || final.Outcome != models.OutcomeBlocked {
		t.Fatalf("status=%s outcome=%s", final.Status, final.Outcome)
	}
}

type countingFinalizer struct {
	inner PlanFinalizer
	calls int
}

func (c *countingFinalizer) Finalize(ctx context.Context, plan *models.PlanGraph) error {
	c.calls++
	return c.inner.Finalize(ctx, plan)
}

func TestRunnableButNotDispatchedIsNotATerminalState(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := terminalTestPlan("terminal-runnable")
	plan.Status = models.StatusInProgress
	plan.Nodes = []*models.TaskNode{
		{ID: "a", Name: "ready", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusReady, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	hint, _ := planStore.GetPlan(plan.ID)
	if outcome, err := s.reconcilePlanState(context.Background(), hint); err != nil || outcome != reconcileContinue {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Status != models.StatusInProgress || final.Outcome != "" {
		t.Fatalf("status=%s outcome=%q", final.Status, final.Outcome)
	}
}
