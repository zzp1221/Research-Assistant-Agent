package scheduler

import (
	"context"
	"errors"
	"fmt"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/recovery"
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
		// A new run must not inherit the previous terminal result or its
		// explanation; the history stays in the event log.
		plan.Outcome = ""
		plan.OutcomeReason = ""
		plan.OutcomeDetails = nil
		// A new execution round owns its own terminal transition.
		plan.TerminalCommittedAt = nil
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
			// Same for an approval timeout: the decision is committed by the sweep,
			// the terminal pipeline is not.
			s.finishExternalTerminal(ctx, planID)
			return nil
		}
		// Questions have their own timeout budget. Expiring one either resumes
		// the task with its default answer or blocks the plan with a missing
		// requirement; it never competes with the execution duration budget.
		if expired, expireErr := s.expireQuestions(planID); expireErr != nil {
			return expireErr
		} else if expired {
			// The timeout already committed failed/blocked; the terminal event,
			// cleanup, budget release and callback still have to run once.
			s.finishExternalTerminal(ctx, planID)
			return nil
		}
		plan, err := s.store.GetPlan(planID)
		if err != nil {
			return err
		}
		if plan.Status == models.StatusCanceled {
			s.finishExternalTerminal(ctx, planID)
			return nil
		}
		if plan.Status == models.StatusFailed {
			// A stop decided outside this loop (approval rejected, approval or
			// question timeout, terminal guard) still needs its event, cleanup and
			// metrics published exactly once.
			s.finishExternalTerminal(ctx, planID)
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
		// The attempt budget only stops new work; it never rewrites a plan whose
		// tasks already ended. reconcilePlanState re-checks this on the committed
		// state, so a snapshot cannot decide the terminal result.
		if budgetExceeded(plan) {
			s.cancelForBudget(planID)
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
			outcome, reconcileErr := s.reconcilePlanState(ctx, plan)
			if reconcileErr != nil {
				if errors.Is(reconcileErr, ErrPlanCanceled) {
					return nil
				}
				return reconcileErr
			}
			switch outcome {
			case reconcileTerminal:
				return nil
			case reconcileContinue:
				// Runnable work exists but was not dispatchable (for example the
				// global slots are saturated). Wait for a wake-up instead of
				// spinning, and never write a terminal status for it.
				s.wait(ctx, 100*time.Millisecond)
				continue executionLoop
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
	// The manual entry point uses the same legality predicate as automatic
	// dispatch: a node whose hard dependencies are not satisfied, that is blocked
	// on a real cause, or that still has a pending approval/question must not be
	// started behind the scheduler's back.
	if err := dispatchLegality(plan, task); err != nil {
		return nil, err
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
	// The legality predicate is re-checked inside the same UpdatePlan that takes
	// the lease, so a decision made between the snapshot read above and this
	// commit cannot start an already-stopped node.
	executionID, leaseOwner, executionEpoch, err := s.markTaskStarted(planID, taskID, taskDispatchGuard(plan, taskID))
	if err != nil {
		s.releaseTaskReservation(planID, taskID)
		return nil, err
	}

	timeout := time.Duration(task.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	recorder := usage.NewRecorder()
	startedAt := time.Now()
	taskCtx, cancel := context.WithTimeout(modelresolver.WithOwner(usage.WithRecorder(ctx, recorder), plan.OwnerID), timeout)
	taskCtx = s.compactionBudgetContext(taskCtx, plan)
	defer cancel()
	plan, err = s.store.GetPlan(planID)
	if err != nil {
		return nil, err
	}
	task = getNodeByID(plan, taskID)
	taskCtx, err = s.recoveryContext(taskCtx, plan, task)
	if err != nil {
		return nil, err
	}

	result, execErr := s.executor.ExecuteTask(taskCtx, plan, task)
	if result != nil && result.Status == "" && execErr == nil {
		result.Status = models.StatusCompleted
	}
	if result == nil {
		result = &models.TaskExecutionResult{
			Status: models.StatusFailed,
			Error:  "task execution returned nil result",
		}
	}
	if execErr != nil && result.Status != models.StatusCanceled {
		result.Status, result.Error = models.StatusFailed, execErr.Error()
	}
	result.Usage = taskUsageFromSnapshot(recorder.Snapshot(), time.Since(startedAt))
	if controller := recovery.FromContext(taskCtx); controller != nil {
		snapshot := controller.Snapshot()
		result.Recovery = &snapshot
	}
	s.settleTaskBudget(planID, taskID, result.Usage)
	if _, _, transitionErr := s.applyTaskResult(planID, taskID, executionID, leaseOwner, executionEpoch, result, execErr, false); transitionErr != nil {
		// A non-stale protocol/validation error still means this execution is over.
		// Committing the real failure releases the lease and records the reason;
		// only a stale execution keeps its result discarded.
		if !errors.Is(transitionErr, ErrStaleExecution) {
			failed := &models.TaskExecutionResult{Status: models.StatusFailed, Error: transitionErr.Error()}
			if _, markErr := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, failed, false); markErr != nil && !errors.Is(markErr, ErrStaleExecution) {
				return result, markErr
			}
		}
		s.postTaskBudgetCheck(planID)
		return result, transitionErr
	}
	s.postTaskBudgetCheck(planID)
	return result, nil
}

func (s *Scheduler) promotePendingNodesToReady(plan *models.PlanGraph) []models.PlanEvent {
	events := []models.PlanEvent{}
	now := time.Now()
	for _, node := range plan.Nodes {
		if node == nil || node.Status != models.StatusPending {
			continue
		}
		// A disabled node is a deliberate skip, not a runnable task. Mark it
		// skipped so dependents see a satisfied dependency and the plan can
		// continue without executing the node.
		if !node.IsEnabled() {
			node.Status = models.StatusSkipped
			node.UpdatedAt = now
			events = append(events, models.PlanEvent{
				PlanID:     plan.ID,
				EventType:  "task_skipped",
				TaskID:     node.ID,
				TaskStatus: string(node.Status),
				Payload:    map[string]any{"name": node.Name, "reason": "disabled"},
				Timestamp:  now,
			})
			continue
		}
		if hardDependenciesSatisfied(plan, node) && optionalDependenciesSatisfied(plan, node) && requiredArtifactsSatisfied(plan, node) {
			node.Status = models.StatusReady
			node.UpdatedAt = now
			events = append(events, models.PlanEvent{
				PlanID:     plan.ID,
				EventType:  "task_ready",
				TaskID:     node.ID,
				TaskStatus: string(node.Status),
				Payload: map[string]any{
					"name":   node.Name,
					"inputs": summarizeTaskInputs(plan, node),
				},
				Timestamp: now,
			})
		}
	}
	if len(events) > 0 {
		plan.UpdatedAt = now
	}
	return events
}
