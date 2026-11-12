package scheduler

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

type taskExecutorFunc func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error)

func (fn taskExecutorFunc) ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
	return fn(ctx, plan, task)
}

func TestSchedulerRetriesFailedAttemptWithinLimit(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("retry-plan", 1)
	plan.Nodes[0].RetryLimit = 1
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	runner := NewScheduler(planStore, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		if attempts.Add(1) == 1 {
			return &models.TaskExecutionResult{Status: models.StatusFailed, Error: "transient"}, nil
		}
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "recovered"}, nil
	}), &NoopEventPublisher{}, 1)

	if err := runner.ExecutePlan(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusCompleted || finalPlan.Nodes[0].RunCount != 2 || finalPlan.Nodes[0].Result != "recovered" {
		t.Fatalf("automatic retry did not recover plan: %#v", finalPlan.Nodes[0])
	}
}

func TestSchedulerEnforcesTaskTimeout(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("timeout-plan", 1)
	plan.Nodes[0].TimeoutSeconds = 1
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, taskExecutorFunc(func(ctx context.Context, _ *models.PlanGraph, _ *models.TaskNode) (*models.TaskExecutionResult, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}), &NoopEventPublisher{}, 1)

	if err := runner.ExecutePlan(context.Background(), plan.ID); err != nil {
		t.Fatal(err)
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusFailed {
		t.Fatalf("timeout did not fail plan: status=%s task=%#v", finalPlan.Status, finalPlan.Nodes[0])
	}
	if !strings.Contains(finalPlan.Nodes[0].Error, "timed out") {
		t.Fatalf("timeout failure should record a useful error: %q", finalPlan.Nodes[0].Error)
	}
}

// The attempt budget stops new work; it never rewrites a plan whose tasks all
// ended. Here the only executed task failed and its dependent was blocked, so
// the committed result is failed/failed even though the attempt budget is spent.
func TestExhaustedAttemptBudgetDoesNotOverwriteTerminalSummary(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("budget-plan", 2)
	plan.Nodes[1].Dependencies = []string{plan.Nodes[0].ID}
	plan.Budget.MaxTaskAttempts = 1
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)

	if err := runner.ExecutePlan(context.Background(), plan.ID); err != nil {
		t.Fatalf("terminal summary must not report a budget error: %v", err)
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusFailed || finalPlan.Outcome != models.OutcomeFailed || finalPlan.OutcomeReason == "" {
		t.Fatalf("terminal result was overwritten by the budget: status=%s outcome=%s reason=%q", finalPlan.Status, finalPlan.Outcome, finalPlan.OutcomeReason)
	}
	if finalPlan.Usage.TaskAttempts != 1 || finalPlan.Usage.FinishedAt == nil {
		t.Fatalf("unexpected usage: %#v", finalPlan.Usage)
	}
	if blocked := finalPlan.Nodes[1]; blocked.Status != models.StatusBlocked || blocked.BlockInfo == nil || blocked.BlockInfo.Code != models.BlockUpstreamFailed {
		t.Fatalf("dependent was not blocked with upstream_failed: %#v", blocked)
	}
}

// Work that still should run but has no attempt left is a budget stop, not a
// deadlock and not a completion.
func TestExhaustedAttemptBudgetWithRunnableWorkCancels(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("budget-plan-live", 1)
	plan.Budget.MaxTaskAttempts = 1
	plan.Usage.TaskAttempts = 1
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	// A budget stop is reported to the caller, but the plan itself must be a
	// canceled plan with a recorded reason, never a deadlock or a completion.
	if err := runner.ExecutePlan(context.Background(), plan.ID); err == nil {
		t.Fatal("expected the budget stop to be reported")
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusCanceled || finalPlan.Outcome != models.OutcomeFailed {
		t.Fatalf("status=%s outcome=%s, want canceled/failed", finalPlan.Status, finalPlan.Outcome)
	}
	if !strings.Contains(finalPlan.OutcomeReason, "budget") {
		t.Fatalf("cancel reason=%q", finalPlan.OutcomeReason)
	}
}

func TestCancelPlanRejectsTerminalPlan(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("terminal-plan", 1)
	plan.Status = models.StatusCompleted
	plan.Nodes[0].Status = models.StatusCompleted
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	if err := NewScheduler(planStore, nil, nil, 1).CancelPlan(plan.ID, "too late"); err == nil {
		t.Fatal("expected terminal plan cancellation to be rejected")
	}
}

func TestCanceledPlanCannotBeResurrectedByLateTaskResult(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("cancel-race-plan", 1)
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	runner := NewScheduler(planStore, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		close(started)
		<-release
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "late result"}, nil
	}), &NoopEventPublisher{}, 1)
	done := make(chan error, 1)
	go func() { done <- runner.ExecutePlan(context.Background(), plan.ID) }()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("task did not start")
	}
	if err := runner.CancelPlan(plan.ID, "test cancellation"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scheduler did not observe cancellation")
	}
	close(release)
	time.Sleep(100 * time.Millisecond)
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusCanceled || finalPlan.Nodes[0].Status != models.StatusCanceled || finalPlan.Nodes[0].Result != "" {
		t.Fatalf("late task result resurrected canceled state: %#v", finalPlan.Nodes[0])
	}
}

func nodeStatuses(plan *models.PlanGraph) map[string]string {
	out := map[string]string{}
	for _, node := range plan.Nodes {
		if node != nil {
			out[node.ID] = string(node.Status)
		}
	}
	return out
}

func governanceTestPlan(id string, nodeCount int) *models.PlanGraph {
	now := time.Now()
	plan := &models.PlanGraph{
		ID:        id,
		TraceID:   "trace-" + id,
		Status:    models.StatusPending,
		Budget:    models.RunBudget{MaxTaskAttempts: 10, MaxDurationSec: 30},
		Artifacts: map[string]models.Artifact{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	for i := 0; i < nodeCount; i++ {
		plan.Nodes = append(plan.Nodes, &models.TaskNode{
			ID:             fmt.Sprintf("%s-task-%d", id, i+1),
			Name:           "Test task",
			Type:           "execute_code",
			AssignedTo:     "sandbox_agent",
			Status:         models.StatusPending,
			TimeoutSeconds: 5,
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
	return plan
}
