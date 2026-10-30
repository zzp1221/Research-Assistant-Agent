package scheduler

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"research-assistant-backend/internal/budget"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/models"
)

// Budget thresholds are evaluated per plan and per task dispatch. The default
// per-task estimate keeps the gate conservative even before any history exists.
const (
	defaultTaskCostEstimateUSD = 0.02
	defaultTaskTokenEstimate   = 4000
)

// SetBudgetManager wires the owner cost ledger into the scheduler.
func (s *Scheduler) SetBudgetManager(manager *budget.Manager) {
	if s == nil {
		return
	}
	s.budget = manager
}

// budgetGate inspects a plan right before a task is dispatched. It returns
// (blocked, error). Soft policies pause the plan, hard policies cancel it.
func (s *Scheduler) budgetGate(planID string) (bool, error) {
	if s == nil || s.store == nil {
		return false, nil
	}
	plan, err := s.store.GetPlan(planID)
	if err != nil {
		return false, err
	}
	trigger, status := evaluatePlanBudget(plan, true)
	if trigger == "" {
		return false, nil
	}
	return s.applyBudgetTrigger(plan, trigger, status)
}

// postTaskBudgetCheck re-evaluates a plan after settled usage is folded in.
// It only uses actual spend, so a completed task cannot be pre-blocked by the
// next task's estimate.
func (s *Scheduler) postTaskBudgetCheck(planID string) {
	if s == nil || s.store == nil {
		return
	}
	plan, err := s.store.GetPlan(planID)
	if err != nil {
		return
	}
	trigger, status := evaluatePlanBudget(plan, false)
	if trigger == "" {
		return
	}
	_, _ = s.applyBudgetTrigger(plan, trigger, status)
}

func (s *Scheduler) applyBudgetTrigger(plan *models.PlanGraph, trigger, status string) (bool, error) {
	planID := plan.ID
	policy := plan.Budget.BudgetPolicyOrDefault()
	_ = s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
		current.Usage.BudgetStatus = status
		if trigger == "warning" && !containsString(current.Usage.BudgetWarnings, "80%") {
			current.Usage.BudgetWarnings = append(current.Usage.BudgetWarnings, "80%")
		}
		if status == "exceeded" && !containsString(current.Usage.BudgetWarnings, "100%") {
			current.Usage.BudgetWarnings = append(current.Usage.BudgetWarnings, "100%")
		}
		return nil
	})
	if trigger == "warning" {
		alreadyWarned := containsString(plan.Usage.BudgetWarnings, "80%")
		if alreadyWarned {
			return false, nil
		}
		metrics.RecordBudgetWarning(policy)
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "budget_warning",
			Payload: map[string]any{
				"policy":     policy,
				"threshold":  0.8,
				"spent_usd":  plan.Usage.LLMCostUSD,
				"limit_usd":  plan.Budget.MaxCostUSD,
				"tokens":     plan.Usage.LLMPromptTokens + plan.Usage.LLMCompletionTokens,
				"max_tokens": plan.Budget.MaxTokens,
			},
			Timestamp: time.Now(),
		})
		if s.budget != nil {
			s.budget.RecordAlert(budget.Alert{OwnerID: plan.OwnerID, PlanID: planID, Kind: "warning", Scope: "plan", Policy: policy, Message: "plan budget reached 80%", SpentUSD: plan.Usage.LLMCostUSD, LimitUSD: plan.Budget.MaxCostUSD})
		}
		return false, nil
	}
	// 100%: record and act according to soft/hard policy. Reaching this twice
	// only happens after a human resumes a paused plan, which is itself an
	// explicit decision to retry, so a fresh event is intentional.
	metrics.RecordBudgetExceeded(policy)
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: "budget_exceeded",
		Payload: map[string]any{
			"policy":     policy,
			"spent_usd":  plan.Usage.LLMCostUSD,
			"limit_usd":  plan.Budget.MaxCostUSD,
			"tokens":     plan.Usage.LLMPromptTokens + plan.Usage.LLMCompletionTokens,
			"max_tokens": plan.Budget.MaxTokens,
			"message":    "budget exceeded before task dispatch",
		},
		Timestamp: time.Now(),
	})
	if s.budget != nil {
		s.budget.RecordAlert(budget.Alert{OwnerID: plan.OwnerID, PlanID: planID, Kind: "exceeded", Scope: "plan", Policy: policy, Message: "plan budget exceeded", SpentUSD: plan.Usage.LLMCostUSD, LimitUSD: plan.Budget.MaxCostUSD})
	}
	// The final task may push a plan over budget after all work is already
	// terminal. Record the overage and alert, but never leave a finished plan
	// in a paused/canceled state.
	if allTerminal(plan) {
		return false, nil
	}
	if policy == "hard" {
		if err := s.CancelPlan(planID, "budget exceeded"); err != nil {
			log.Printf("scheduler: budget cancel failed for %s: %v", planID, err)
		}
		return true, fmt.Errorf("budget exceeded")
	}
	now := time.Now()
	if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
		if current.Status == models.StatusCanceled || current.Status == models.StatusCompleted || current.Status == models.StatusFailed {
			return nil
		}
		current.Paused = true
		current.PausedAt = &now
		current.Usage.BudgetStatus = "exceeded"
		current.UpdatedAt = now
		return nil
	}); err != nil {
		return true, err
	}
	s.publishAndStore(planID, models.PlanEvent{PlanID: planID, EventType: "plan_paused", Payload: map[string]any{"reason": "budget_exceeded", "policy": policy}, Timestamp: now})
	return true, nil
}

// evaluatePlanBudget returns "warning" at 80% and "exceeded" at 100% of any
// configured plan limit. When includeEstimate is true, the next task's
// conservative estimate is added first so the gate blocks before overspending.
func evaluatePlanBudget(plan *models.PlanGraph, includeEstimate bool) (string, string) {
	if plan == nil {
		return "", "ok"
	}
	estimateUSD, estimateTokens := 0.0, 0
	if includeEstimate {
		estimateUSD, estimateTokens = estimateTaskCost(plan)
	}
	type limitCheck struct {
		configured bool
		used       float64
		limit      float64
	}
	checks := []limitCheck{
		{configured: plan.Budget.MaxCostUSD > 0, used: plan.Usage.LLMCostUSD + estimateUSD, limit: plan.Budget.MaxCostUSD},
		{configured: plan.Budget.MaxTokens > 0, used: float64(plan.Usage.LLMPromptTokens + plan.Usage.LLMCompletionTokens + estimateTokens), limit: float64(plan.Budget.MaxTokens)},
		{configured: plan.Budget.MaxSandboxSeconds > 0, used: plan.Usage.SandboxSeconds, limit: plan.Budget.MaxSandboxSeconds},
	}
	warning := false
	for _, check := range checks {
		if !check.configured || check.limit <= 0 {
			continue
		}
		if check.used >= check.limit {
			return "exceeded", "exceeded"
		}
		if check.used >= check.limit*0.8 {
			warning = true
		}
	}
	if warning {
		return "warning", "warning"
	}
	return "", "ok"
}

// estimateTaskCost returns a conservative pre-dispatch estimate from the plan's
// observed per-task average, falling back to a fixed default.
func estimateTaskCost(plan *models.PlanGraph) (float64, int) {
	estimateUSD := defaultTaskCostEstimateUSD
	estimateTokens := defaultTaskTokenEstimate
	if plan == nil {
		return estimateUSD, estimateTokens
	}
	completed := 0
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusCompleted && node.Usage != nil {
			completed++
		}
	}
	if completed > 0 && plan.Usage.LLMCostUSD > 0 {
		if average := plan.Usage.LLMCostUSD / float64(completed); average > estimateUSD {
			estimateUSD = average
		}
	}
	if completed > 0 {
		totalTokens := plan.Usage.LLMPromptTokens + plan.Usage.LLMCompletionTokens
		if average := totalTokens / completed; average > estimateTokens {
			estimateTokens = average
		}
	}
	return estimateUSD, estimateTokens
}

// reserveTaskBudget holds an estimated amount before dispatch. A nil manager or
// no configured user budget never blocks; the reservation still counts against
// concurrent tasks so parallel workers cannot oversubscribe the same budget.
func (s *Scheduler) reserveTaskBudget(plan *models.PlanGraph, task *models.TaskNode) (budget.Reservation, error) {
	if s == nil || s.budget == nil || plan == nil || task == nil {
		return budget.Reservation{}, nil
	}
	estimateUSD, estimateTokens := estimateTaskCost(plan)
	reservation, err := s.budget.Reserve(plan.OwnerID, plan.ID, task.ID, estimateUSD, estimateTokens)
	if err != nil {
		if errors.Is(err, budget.ErrBudgetExceeded) {
			s.handleUserBudgetExceeded(plan, err)
		}
		return budget.Reservation{}, err
	}
	s.budgetMu.Lock()
	if s.reservations == nil {
		s.reservations = map[string]budget.Reservation{}
	}
	s.reservations[plan.ID+"|"+task.ID] = reservation
	s.budgetMu.Unlock()
	return reservation, nil
}

func (s *Scheduler) handleUserBudgetExceeded(plan *models.PlanGraph, cause error) {
	policy := plan.Budget.BudgetPolicyOrDefault()
	if s.budget != nil {
		if userBudget, ok := s.budget.Budget(plan.OwnerID); ok && userBudget.Policy != "" {
			policy = userBudget.Policy
			if policy != "hard" {
				policy = "soft"
			}
		}
	}
	metrics.RecordBudgetExceeded(policy)
	s.publishAndStore(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "budget_exceeded", Payload: map[string]any{"scope": "user", "policy": policy, "message": cause.Error()}, Timestamp: time.Now()})
	if s.budget != nil {
		s.budget.RecordAlert(budget.Alert{OwnerID: plan.OwnerID, PlanID: plan.ID, Kind: "exceeded", Scope: "user", Policy: policy, Message: cause.Error()})
	}
	if policy == "hard" {
		_ = s.CancelPlan(plan.ID, "user budget exceeded")
		return
	}
	now := time.Now()
	_ = s.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		if current.Status == models.StatusCanceled || current.Status == models.StatusCompleted || current.Status == models.StatusFailed {
			return nil
		}
		current.Paused = true
		current.PausedAt = &now
		current.Usage.BudgetStatus = "exceeded"
		current.UpdatedAt = now
		return nil
	})
	s.publishAndStore(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "plan_paused", Payload: map[string]any{"reason": "budget_exceeded", "scope": "user", "policy": policy}, Timestamp: now})
}

// settleTaskBudget converts one reservation into a settled ledger entry.
func (s *Scheduler) settleTaskBudget(planID, taskID string, usageRecord *models.TaskUsage) {
	if s == nil || s.budget == nil {
		return
	}
	s.budgetMu.Lock()
	reservation, ok := s.reservations[planID+"|"+taskID]
	if ok {
		delete(s.reservations, planID+"|"+taskID)
	}
	s.budgetMu.Unlock()
	if !ok {
		// A task that never held a reservation still contributes to the ledger so
		// the cost console is complete; it is recorded as a settled entry.
		if usageRecord != nil {
			s.budget.Record(budget.Entry{
				PlanID: planID, TaskID: taskID, Model: dominantModel(usageRecord.Models),
				Prompt: usageRecord.LLMPromptTokens, Completion: usageRecord.LLMCompletionTokens,
				CostUSD: usageRecord.LLMCostUSD, Estimated: usageRecord.LLMEstimated,
				CostSource: usageRecord.CostSource, At: time.Now().UTC(),
			})
		}
		return
	}
	if usageRecord == nil {
		s.budget.Release(reservation)
		return
	}
	s.budget.Settle(reservation, budget.Entry{
		OwnerID:    reservation.OwnerID,
		PlanID:     planID,
		TaskID:     taskID,
		Model:      dominantModel(usageRecord.Models),
		Prompt:     usageRecord.LLMPromptTokens,
		Completion: usageRecord.LLMCompletionTokens,
		CostUSD:    usageRecord.LLMCostUSD,
		Estimated:  usageRecord.LLMEstimated,
		CostSource: usageRecord.CostSource,
		At:         time.Now().UTC(),
	})
}

// releaseTaskReservation releases a reservation that never reached settlement
// (for example a task that panicked or exited before usage was recorded).
func (s *Scheduler) releaseTaskReservation(planID, taskID string) {
	if s == nil || s.budget == nil {
		return
	}
	s.budgetMu.Lock()
	reservation, ok := s.reservations[planID+"|"+taskID]
	if ok {
		delete(s.reservations, planID+"|"+taskID)
	}
	s.budgetMu.Unlock()
	if ok {
		s.budget.Release(reservation)
	}
}

func (s *Scheduler) releasePlanBudget(planID string) {
	if s == nil || s.budget == nil {
		return
	}
	s.budget.ReleasePlan(planID)
	s.budgetMu.Lock()
	for key := range s.reservations {
		if strings.HasPrefix(key, planID+"|") {
			delete(s.reservations, key)
		}
	}
	s.budgetMu.Unlock()
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func dominantModel(models map[string]int) string {
	best := ""
	bestCount := -1
	for model, count := range models {
		if count > bestCount || (count == bestCount && strings.Compare(model, best) < 0) {
			best = model
			bestCount = count
		}
	}
	return best
}
