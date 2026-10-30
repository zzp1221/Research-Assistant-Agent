package scheduler

import (
	"context"
	"fmt"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/models"
	"time"
)

func (s *Scheduler) CancelPlan(planID, reason string) error {
	return s.CancelPlanWithActor(planID, reason, "")
}

// CancelPlanWithActor cancels a plan, cancels every pending runtime action so a
// stale approval can never resurrect it, and releases plan-scoped sandboxes.
func (s *Scheduler) CancelPlanWithActor(planID, reason, actor string) error {
	now := time.Now()
	if reason == "" {
		reason = "canceled by user"
	}
	canceledActions := 0
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status == models.StatusCompleted || plan.Status == models.StatusFailed || plan.Status == models.StatusCanceled {
			return fmt.Errorf("plan cannot be canceled from status %s", plan.Status)
		}
		plan.Status = models.StatusCanceled
		plan.Usage.FinishedAt = &now
		plan.UpdatedAt = now
		for _, task := range plan.Nodes {
			if task == nil {
				continue
			}
			switch task.Status {
			case models.StatusPending, models.StatusReady, models.StatusInProgress, models.StatusBlocked, models.StatusAwaitingApproval, models.StatusAwaitingInput:
				task.Status = models.StatusCanceled
				task.Error = reason
				task.ExecutionID = ""
				task.LeaseOwner = ""
				task.LeaseExpiresAt = nil
				task.FinishedAt = &now
				task.UpdatedAt = now
			}
		}
		for i := range plan.RuntimeActions {
			if plan.RuntimeActions[i].Status != models.RuntimeActionPending {
				continue
			}
			plan.RuntimeActions[i].Status = models.RuntimeActionCanceled
			plan.RuntimeActions[i].DecidedAt = &now
			plan.RuntimeActions[i].DecidedBy = actor
			plan.RuntimeActions[i].DecisionNote = "plan canceled"
			canceledActions++
		}
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}
	payload := map[string]any{"reason": reason}
	if actor != "" {
		payload["actor"] = actor
	}
	if canceledActions > 0 {
		payload["canceled_runtime_actions"] = canceledActions
	}
	s.publishAndStore(planID, models.PlanEvent{PlanID: planID, EventType: "plan_canceled", TaskStatus: string(models.StatusCanceled), Payload: payload, Timestamp: now})
	s.cleanupPlanSandboxes(context.Background(), planID)
	s.releasePlanBudget(planID)
	s.recordTerminalMetrics(planID)
	s.notifyProgress()
	return nil
}

func (s *Scheduler) RetryTask(planID, taskID string) error {
	now := time.Now()
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status == models.StatusInProgress {
			return fmt.Errorf("cannot retry a task while the plan is running")
		}
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if task.Status == models.StatusInProgress {
			return fmt.Errorf("cannot retry a task that is currently running")
		}
		if task.Status == models.StatusAwaitingApproval {
			return fmt.Errorf("task is awaiting runtime approval; decide or cancel the action first")
		}
		if task.Status != models.StatusFailed && task.Status != models.StatusBlocked && task.Status != models.StatusCanceled {
			return fmt.Errorf("task cannot be retried from status %s", task.Status)
		}
		resetTaskForRetry(task, now)
		resetBlockedDependents(plan, taskID, now)
		plan.Status = models.StatusPending
		plan.Usage.FinishedAt = nil
		plan.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}
	s.publishAndStore(planID, models.PlanEvent{PlanID: planID, EventType: "task_retry_requested", TaskID: taskID, TaskStatus: string(models.StatusPending), Timestamp: now})
	return nil
}

// PausePlan stops dispatching new tasks. Running tasks finish normally so a
// pause never leaves a half-written task result.
func (s *Scheduler) PausePlan(planID, actor string) error {
	now := time.Now()
	changed := false
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		switch plan.Status {
		case models.StatusCompleted, models.StatusFailed, models.StatusCanceled:
			return fmt.Errorf("plan cannot be paused from status %s", plan.Status)
		}
		if plan.Paused {
			return nil
		}
		plan.Paused = true
		plan.PausedAt = &now
		plan.UpdatedAt = now
		changed = true
		return nil
	}); err != nil {
		return err
	}
	if changed {
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "plan_paused",
			Payload:   map[string]any{"actor": actor},
			Timestamp: now,
		})
		metrics.Default().Inc("plan_paused_total", nil)
	}
	s.notifyProgress()
	return nil
}

// ResumePlan clears the pause flag and wakes the execution loop.
func (s *Scheduler) ResumePlan(planID, actor string) error {
	now := time.Now()
	changed := false
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		switch plan.Status {
		case models.StatusCompleted, models.StatusFailed, models.StatusCanceled:
			return fmt.Errorf("plan cannot be resumed from status %s", plan.Status)
		}
		if !plan.Paused {
			return nil
		}
		plan.Paused = false
		plan.PausedAt = nil
		plan.UpdatedAt = now
		changed = true
		return nil
	}); err != nil {
		return err
	}
	if changed {
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "plan_resumed",
			Payload:   map[string]any{"actor": actor},
			Timestamp: now,
		})
	}
	s.notifyProgress()
	return nil
}

// ResetFromNode rewinds one task and every descendant so a failed or
// unsatisfactory node can be re-run without rebuilding the whole plan.
func (s *Scheduler) ResetFromNode(planID, taskID string) error {
	now := time.Now()
	resetIDs := []string{}
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		target := getNodeByID(plan, taskID)
		if target == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		for _, node := range plan.Nodes {
			if node != nil && node.Status == models.StatusInProgress {
				return fmt.Errorf("plan has running tasks; cancel or wait before rewinding")
			}
		}
		affected := map[string]bool{target.ID: true}
		queue := []string{target.ID}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, next := range getOutgoingNodes(plan, current) {
				if next == nil || affected[next.ID] {
					continue
				}
				affected[next.ID] = true
				queue = append(queue, next.ID)
			}
		}
		for _, node := range plan.Nodes {
			if node == nil || !affected[node.ID] {
				continue
			}
			resetTaskForRetry(node, now)
			node.Usage = nil
			resetIDs = append(resetIDs, node.ID)
		}
		for key, artifact := range plan.Artifacts {
			if affected[artifact.ProducerTaskID] {
				delete(plan.Artifacts, key)
			}
		}
		for i := range plan.RuntimeActions {
			action := &plan.RuntimeActions[i]
			if affected[action.TaskID] && action.Status == models.RuntimeActionPending {
				action.Status = models.RuntimeActionCanceled
				action.DecidedAt = &now
				action.DecidedBy = "rewind"
				action.DecisionNote = "plan rewound from node " + taskID
			}
		}
		plan.Status = models.StatusPending
		plan.Outcome = ""
		plan.OutcomeReason = ""
		plan.MissingRequirements = nil
		plan.Paused = false
		plan.PausedAt = nil
		plan.Usage.FinishedAt = nil
		plan.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}
	s.clearTerminalMarker(planID)
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: "plan_rewound",
		TaskID:    taskID,
		Payload:   map[string]any{"reset_tasks": resetIDs},
		Timestamp: now,
	})
	s.notifyProgress()
	return nil
}

func (s *Scheduler) ReassignTask(planID, taskID, assignedTo string) error {
	now := time.Now()
	if assignedTo == "" {
		return fmt.Errorf("assigned agent is required")
	}
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if task.Status == models.StatusCompleted || task.Status == models.StatusSkipped {
			return fmt.Errorf("completed task cannot be reassigned")
		}
		task.AssignedTo = assignedTo
		resetTaskForRetry(task, now)
		resetBlockedDependents(plan, taskID, now)
		plan.Status = models.StatusPending
		plan.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}
	s.publishAndStore(planID, models.PlanEvent{PlanID: planID, EventType: "task_reassigned", TaskID: taskID, TaskStatus: string(models.StatusPending), Payload: map[string]any{"assigned_to": assignedTo}, Timestamp: now})
	return nil
}
