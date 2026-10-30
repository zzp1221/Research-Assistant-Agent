package scheduler

import (
	"context"
	"errors"
	"fmt"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/usage"
	"time"
)

func (s *Scheduler) ExecutePlan(ctx context.Context, planID string) error {
	if s.store == nil {
		return fmt.Errorf("plan store is nil")
	}

	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status == models.StatusInProgress {
			return fmt.Errorf("plan is already running")
		}
		if plan.Approval.Required && plan.Approval.Status != "approved" {
			return fmt.Errorf("plan requires approval")
		}
		if plan.Status != models.StatusPending && plan.Status != models.StatusReady {
			return fmt.Errorf("plan cannot run from status %s", plan.Status)
		}
		now := time.Now()
		plan.Status = models.StatusInProgress
		plan.Usage.StartedAt = &now
		plan.Usage.FinishedAt = nil
		plan.UpdatedAt = now
		return nil
	}); err != nil {
		return err
	}
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: "plan_started",
		Timestamp: time.Now(),
	})
	s.clearTerminalMarker(planID)
	metrics.Default().AddGauge("active_plans", 1, nil)
	s.touchActivity(planID)

executionLoop:
	for {
		select {
		case <-ctx.Done():
			if plan, err := s.store.GetPlan(planID); err == nil && plan.Status == models.StatusCanceled {
				return nil
			}
			s.finishPlanTerminal(planID, models.StatusFailed, "execution context ended: "+ctx.Err().Error())
			return ctx.Err()
		default:
		}

		if err := s.expireStaleLeases(planID); err != nil {
			return err
		}
		// Approval waits use an independent budget and never consume the plan
		// execution duration budget.
		s.syncApprovalWaitWindow(planID)
		if expired, expireErr := s.expireApprovalActions(planID); expireErr != nil {
			return expireErr
		} else if expired {
			return nil
		}
		// Questions have their own timeout budget. Expiring one either resumes
		// the task with its default answer or blocks the plan with a missing
		// requirement; it never competes with the execution duration budget.
		if expired, expireErr := s.expireQuestions(planID); expireErr != nil {
			return expireErr
		} else if expired {
			return nil
		}
		plan, err := s.store.GetPlan(planID)
		if err != nil {
			return err
		}
		if plan.Status == models.StatusCanceled {
			return nil
		}
		if plan.Status == models.StatusFailed && plan.Outcome == models.OutcomeBlocked {
			return nil
		}
		// A paused plan keeps its running tasks but dispatches nothing new. The
		// wait is interruptible so resume wakes the loop immediately.
		if plan.Paused && !allTerminal(plan) {
			s.wait(ctx, pollInterval)
			continue
		}
		// While a structured question is pending the plan must not promote or
		// dispatch new work: the answer can change what should run next.
		if plan.Status == models.StatusAwaitingInput && !allTerminal(plan) {
			s.wait(ctx, 500*time.Millisecond)
			continue
		}
		s.checkPlanStall(ctx, plan)
		if budgetExceeded(plan) {
			_ = s.CancelPlan(planID, "run budget exceeded")
			return fmt.Errorf("run budget exceeded")
		}

		readyEvents := []models.PlanEvent{}
		if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
			if current.Status == models.StatusCanceled {
				return ErrPlanCanceled
			}
			readyEvents = s.promotePendingNodesToReady(current)
			fillMeta(current)
			return nil
		}); err != nil {
			if errors.Is(err, ErrPlanCanceled) {
				return nil
			}
			return err
		}
		for _, event := range readyEvents {
			s.publishAndStore(planID, event)
		}
		plan, err = s.store.GetPlan(planID)
		if err != nil {
			return err
		}

		availableSlots := s.maxConcurrent - countInProgress(plan)
		ready := s.findReadyNodes(plan, availableSlots)
		if len(ready) == 0 {
			if hasAwaitingApproval(plan) || hasAwaitingInput(plan) {
				s.wait(ctx, 500*time.Millisecond)
				continue
			}
			if allTerminal(plan) {
				finalStatus := models.StatusCompleted
				if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
					if current.Status == models.StatusCanceled {
						return ErrPlanCanceled
					}
					if !allTerminal(current) {
						return ErrPlanStateChanged
					}
					outcome := models.OutcomeCompleted
					if hasFailure(current) {
						finalStatus = models.StatusFailed
						if hasBlockedOnly(current) {
							outcome = models.OutcomeBlocked
						} else {
							outcome = models.OutcomeFailed
						}
					}
					if s.finalizer != nil && finalStatus == models.StatusCompleted {
						if finalizeErr := s.finalizer.Finalize(ctx, current); finalizeErr != nil {
							finalStatus = models.StatusFailed
							outcome = models.OutcomeFailed
							current.OutcomeReason = finalizeErr.Error()
						} else {
							outcome = current.Outcome
							if outcome == "" {
								outcome = models.OutcomeCompleted
							}
						}
					}
					if finalStatus == models.StatusFailed && outcome == "" {
						outcome = models.OutcomeFailed
					}
					now := time.Now()
					current.Status = finalStatus
					current.Outcome = outcome
					current.Usage.FinishedAt = &now
					current.UpdatedAt = now
					fillMeta(current)
					return nil
				}); err != nil {
					if errors.Is(err, ErrPlanCanceled) {
						return nil
					}
					if errors.Is(err, ErrPlanStateChanged) {
						continue executionLoop
					}
					return err
				}

				eventType := "plan_completed"
				if finalStatus == models.StatusFailed {
					eventType = "plan_failed"
				}
				latest, _ := s.store.GetPlan(planID)
				outcome := models.OutcomeCompleted
				if latest != nil && latest.Outcome != "" {
					outcome = latest.Outcome
				}
				s.publishAndStore(planID, models.PlanEvent{
					PlanID:    planID,
					EventType: eventType,
					Payload:   map[string]any{"outcome": string(outcome)},
					Timestamp: time.Now(),
				})
				s.cleanupPlanSandboxes(context.Background(), planID)
				s.releasePlanBudget(planID)
				s.recordTerminalMetrics(planID)
				if s.onTerminal != nil {
					if latest, getErr := s.store.GetPlan(planID); getErr == nil && latest != nil {
						s.onTerminal(ctx, latest)
					} else {
						s.onTerminal(ctx, plan)
					}
				}
				return nil
			}

			if noRunning(plan) {
				// Re-check the attempt budget against the refreshed plan: a task may
				// have failed between the loop-top check and this deadlock check.
				if budgetExceeded(plan) {
					_ = s.CancelPlan(planID, "run budget exceeded")
					return fmt.Errorf("run budget exceeded")
				}
				if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
					if current.Status == models.StatusCanceled {
						return ErrPlanCanceled
					}
					if !noRunning(current) || len(s.promotePendingNodesToReady(current)) > 0 {
						return ErrPlanStateChanged
					}
					current.Status = models.StatusFailed
					current.UpdatedAt = time.Now()
					fillMeta(current)
					return nil
				}); err != nil {
					if errors.Is(err, ErrPlanCanceled) {
						return nil
					}
					if errors.Is(err, ErrPlanStateChanged) {
						continue executionLoop
					}
					return err
				}
				s.publishAndStore(planID, models.PlanEvent{
					PlanID:    planID,
					EventType: "plan_failed",
					Payload: map[string]any{
						"reason": "deadlock_or_unsatisfied_dependencies",
					},
					Timestamp: time.Now(),
				})
				s.cleanupPlanSandboxes(context.Background(), planID)
				s.releasePlanBudget(planID)
				s.recordTerminalMetrics(planID)
				if s.onTerminal != nil {
					if latest, getErr := s.store.GetPlan(planID); getErr == nil && latest != nil {
						s.onTerminal(ctx, latest)
					} else {
						s.onTerminal(ctx, plan)
					}
				}
				return nil
			}

			s.wait(ctx, pollInterval)
			continue
		}

		if err := s.runReadyNodes(ctx, planID, ready); err != nil {
			if errors.Is(err, ErrPlanCanceled) {
				return nil
			}
			s.finishPlanTerminal(planID, models.StatusFailed, err.Error())
			return err
		}

		s.wait(ctx, 250*time.Millisecond)
	}
}

// ExecuteTask runs one task from a stored plan without mutating the plan
// lifecycle. It is used by the owner-scoped "run this node" endpoint, where the
// task inputs and artifacts must come from the persisted plan rather than from
// an arbitrary client payload.
func (s *Scheduler) ExecuteTask(ctx context.Context, planID, taskID string) (*models.TaskExecutionResult, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("scheduler store is nil")
	}
	if s.executor == nil {
		return nil, fmt.Errorf("scheduler executor is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := s.store.GetPlan(planID)
	if err != nil {
		return nil, err
	}
	task := getNodeByID(plan, taskID)
	if task == nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if task.Status == models.StatusInProgress {
		return nil, fmt.Errorf("task is already running")
	}
	// Single-node execution is still spend: honour the plan and user budget
	// gates and settle measured usage exactly like a scheduled task.
	if blocked, err := s.budgetGate(planID); err != nil {
		return nil, err
	} else if blocked {
		return nil, fmt.Errorf("budget exceeded")
	}
	if _, err := s.reserveTaskBudget(plan, task); err != nil {
		return nil, err
	}
	defer s.releaseTaskReservation(planID, taskID)
	executionID, leaseOwner, executionEpoch, err := s.markTaskStarted(planID, taskID)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(task.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	recorder := usage.NewRecorder()
	startedAt := time.Now()
	taskCtx, cancel := context.WithTimeout(modelresolver.WithOwner(usage.WithRecorder(ctx, recorder), plan.OwnerID), timeout)
	defer cancel()

	result, execErr := s.executor.ExecuteTask(taskCtx, plan, task)
	if execErr != nil {
		result = &models.TaskExecutionResult{
			Status: models.StatusFailed,
			Error:  execErr.Error(),
		}
	}
	if result == nil {
		result = &models.TaskExecutionResult{
			Status: models.StatusFailed,
			Error:  "task execution returned nil result",
		}
	}
	if result.Status == "" {
		result.Status = models.StatusCompleted
	}
	result.Usage = taskUsageFromSnapshot(recorder.Snapshot(), time.Since(startedAt))
	s.settleTaskBudget(planID, taskID, result.Usage)
	if result.Question != nil {
		if err := s.applyQuestionFromTask(planID, taskID, result.Question); err != nil {
			return nil, err
		}
		return result, nil
	}

	if result.Status != models.StatusCompleted {
		if result.Error == "" {
			result.Error = fmt.Sprintf("task returned non-completed status %q", result.Status)
		}
		if _, markErr := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, result, false); markErr != nil {
			return result, markErr
		}
		s.postTaskBudgetCheck(planID)
		return result, nil
	}

	if err := s.markTaskCompleted(planID, taskID, executionID, leaseOwner, executionEpoch, result); err != nil {
		failed := &models.TaskExecutionResult{
			Status: models.StatusFailed,
			Error:  err.Error(),
		}
		if _, markErr := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, failed, false); markErr != nil {
			return result, markErr
		}
		s.postTaskBudgetCheck(planID)
		return result, err
	}
	s.postTaskBudgetCheck(planID)
	return result, nil
}

func (s *Scheduler) promotePendingNodesToReady(plan *models.PlanGraph) []models.PlanEvent {
	events := []models.PlanEvent{}
	for _, node := range plan.Nodes {
		if node == nil || node.Status != models.StatusPending {
			continue
		}
		// A disabled node is a deliberate skip, not a runnable task. Mark it
		// skipped so dependents see a satisfied dependency and the plan can
		// continue without executing the node.
		if !node.IsEnabled() {
			node.Status = models.StatusSkipped
			node.UpdatedAt = time.Now()
			events = append(events, models.PlanEvent{
				PlanID:     plan.ID,
				EventType:  "task_skipped",
				TaskID:     node.ID,
				TaskStatus: string(node.Status),
				Payload:    map[string]any{"name": node.Name, "reason": "disabled"},
				Timestamp:  time.Now(),
			})
			continue
		}
		if allDependenciesCompleted(plan, node) && artifactsSatisfied(plan, node) {
			node.Status = models.StatusReady
			node.UpdatedAt = time.Now()
			events = append(events, models.PlanEvent{
				PlanID:     plan.ID,
				EventType:  "task_ready",
				TaskID:     node.ID,
				TaskStatus: string(node.Status),
				Payload: map[string]any{
					"name":   node.Name,
					"inputs": summarizeTaskInputs(plan, node),
				},
				Timestamp: time.Now(),
			})
		}
	}
	if len(events) > 0 {
		plan.UpdatedAt = time.Now()
	}
	return events
}
