package scheduler

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/budget"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
	"research-assistant-backend/internal/usage"
)

// costExecutor reports a fixed USD cost on every task it completes by writing
// through the context recorder, which is the only path the scheduler reads.
func costExecutor(cost float64, tokens int) TaskExecutor {
	return taskExecutorFunc(func(ctx context.Context, _ *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
		if recorder := usage.FromContext(ctx); recorder != nil && tokens > 0 {
			recorder.RecordLLMWithPrice("test-model", tokens, 0, false, usage.Price{PromptPer1K: cost / float64(tokens) * 1000})
		}
		return &models.TaskExecutionResult{
			Status: models.StatusCompleted,
			Result: "done",
		}, nil
	})
}

func TestSchedulerPausesPlanBeforeCostBudgetOverspend(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("cost-budget-plan", 2)
	plan.Nodes[1].Dependencies = []string{plan.Nodes[0].ID}
	plan.Budget.MaxCostUSD = 0.01
	plan.Budget.Policy = "soft"
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, costExecutor(0.02, 100), &NoopEventPublisher{}, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = runner.ExecutePlan(ctx, plan.ID)
	finalPlan, err := planStore.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finalPlan.Status == models.StatusCompleted {
		t.Fatalf("plan completed despite budget: usage=%#v", finalPlan.Usage)
	}
	// The first task may run (the pre-dispatch estimate is below the cap), but
	// the second must never start because actual spend already exceeds the cap.
	if finalPlan.Nodes[1].RunCount != 0 {
		t.Fatalf("second task ran after budget was exhausted: %#v", finalPlan.Nodes[1])
	}
	if finalPlan.Usage.BudgetStatus != "exceeded" {
		t.Fatalf("budget status=%q, want exceeded", finalPlan.Usage.BudgetStatus)
	}
	if !finalPlan.Paused && finalPlan.Status != models.StatusCanceled && finalPlan.Status != models.StatusFailed {
		t.Fatalf("plan neither paused nor terminated: status=%s paused=%v", finalPlan.Status, finalPlan.Paused)
	}
}

func TestSchedulerHardBudgetCancelsPlan(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("hard-budget-plan", 2)
	plan.Nodes[1].Dependencies = []string{plan.Nodes[0].ID}
	plan.Budget.MaxCostUSD = 0.01
	plan.Budget.Policy = "hard"
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, costExecutor(0.02, 100), &NoopEventPublisher{}, 1)
	_ = runner.ExecutePlan(context.Background(), plan.ID)
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusCanceled {
		t.Fatalf("hard budget should cancel plan, got status=%s", finalPlan.Status)
	}
	if finalPlan.Nodes[1].RunCount != 0 {
		t.Fatalf("second task ran after hard cancel")
	}
}

func TestSchedulerBudgetWarningAtEightyPercent(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("warning-budget-plan", 1)
	plan.Budget.MaxCostUSD = 0.024
	plan.Budget.Policy = "soft"
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	// The pre-dispatch estimate (0.02) is 83% of the limit, so the gate records
	// a warning before the task; actual cost 0.022 is 92% and keeps it warned.
	runner := NewScheduler(planStore, costExecutor(0.022, 100), &NoopEventPublisher{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = runner.ExecutePlan(ctx, plan.ID)
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Usage.BudgetStatus != "warning" && finalPlan.Usage.BudgetStatus != "exceeded" {
		t.Fatalf("budget status=%q", finalPlan.Usage.BudgetStatus)
	}
	if len(finalPlan.Usage.BudgetWarnings) == 0 {
		t.Fatalf("expected a budget warning")
	}
	events, _ := planStore.ListEvents(plan.ID)
	found := false
	for _, event := range events {
		if event.EventType == "budget_warning" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing budget_warning event")
	}
}

func TestSchedulerUserBudgetReservationBlocksDispatch(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("user-budget-plan", 1)
	plan.OwnerID = "owner-budget"
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	manager := budget.NewManager(budget.NewMemoryStore())
	if _, err := manager.SetBudget("owner-budget", "day", 0.001, "soft"); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, costExecutor(0.02, 100), &NoopEventPublisher{}, 1)
	runner.SetBudgetManager(manager)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = runner.ExecutePlan(ctx, plan.ID)
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Nodes[0].RunCount != 0 {
		t.Fatalf("task ran despite user budget")
	}
	if !finalPlan.Paused {
		t.Fatalf("plan should be paused by user budget, status=%s", finalPlan.Status)
	}
}

func TestEvaluatePlanBudgetThresholds(t *testing.T) {
	plan := &models.PlanGraph{Budget: models.RunBudget{MaxCostUSD: 1.0}, Usage: models.RunUsage{LLMCostUSD: 0.5}}
	if trigger, status := evaluatePlanBudget(plan, false); trigger != "" || status != "ok" {
		t.Fatalf("50%% should be ok: %s %s", trigger, status)
	}
	plan.Usage.LLMCostUSD = 0.8
	if trigger, _ := evaluatePlanBudget(plan, false); trigger != "warning" {
		t.Fatalf("80%% should warn, got %q", trigger)
	}
	plan.Usage.LLMCostUSD = 1.0
	if trigger, _ := evaluatePlanBudget(plan, false); trigger != "exceeded" {
		t.Fatalf("100%% should exceed, got %q", trigger)
	}
	plan.Budget.MaxCostUSD = 0
	if trigger, _ := evaluatePlanBudget(plan, false); trigger != "" {
		t.Fatalf("unconfigured budget must not trigger")
	}
}

func TestSchedulerSingleTaskHonorsBudgetAndRecordsUsage(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("single-task-budget", 1)
	plan.OwnerID = "owner-single"
	plan.Budget.MaxCostUSD = 1
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	manager := budget.NewManager(budget.NewMemoryStore())
	runner := NewScheduler(planStore, costExecutor(0.02, 100), &NoopEventPublisher{}, 1)
	runner.SetBudgetManager(manager)
	result, err := runner.ExecuteTask(context.Background(), plan.ID, plan.Nodes[0].ID)
	if err != nil {
		t.Fatalf("execute task: %v", err)
	}
	if result == nil || result.Usage == nil || result.Usage.LLMCostUSD == 0 {
		t.Fatalf("task usage missing: %#v", result)
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Usage.LLMCostUSD == 0 {
		t.Fatalf("single task usage was not folded into plan: %#v", finalPlan.Usage)
	}
	if !manager.HasPlanEntry(plan.ID) {
		t.Fatalf("single task cost was not settled in the ledger")
	}
}

func TestSchedulerSingleTaskBlocksWhenBudgetExceeded(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("single-task-blocked", 1)
	plan.Budget.MaxCostUSD = 0.01
	plan.Budget.Policy = "soft"
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, costExecutor(0.02, 100), &NoopEventPublisher{}, 1)
	if _, err := runner.ExecuteTask(context.Background(), plan.ID, plan.Nodes[0].ID); err == nil {
		t.Fatalf("expected budget gate to block single task")
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Nodes[0].RunCount != 0 || finalPlan.Nodes[0].Status != models.StatusPending {
		t.Fatalf("task ran despite budget: %#v", finalPlan.Nodes[0])
	}
}

// ensure time import is used even if the file is edited later
var _ = time.Now
