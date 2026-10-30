package scheduler

import (
	"context"
	"os"
	"research-assistant-backend/internal/models"
	"strconv"
	"strings"
	"time"
)

func (s *Scheduler) expireStaleLeases(planID string) error {
	now := time.Now()
	current, err := s.store.GetPlan(planID)
	if err != nil {
		return err
	}
	if current.Status != models.StatusInProgress {
		return nil
	}
	hasExpired := false
	for _, task := range current.Nodes {
		if task != nil && task.Status == models.StatusInProgress && task.LeaseExpiresAt != nil && !now.Before(task.LeaseExpiresAt.Add(taskLeaseGrace)) {
			hasExpired = true
			break
		}
	}
	if !hasExpired {
		return nil
	}
	events := []models.PlanEvent{}
	err = s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status != models.StatusInProgress {
			return nil
		}
		for _, task := range plan.Nodes {
			if task == nil || task.Status != models.StatusInProgress || task.LeaseExpiresAt == nil {
				continue
			}
			if now.Before(task.LeaseExpiresAt.Add(taskLeaseGrace)) {
				continue
			}
			task.Status = models.StatusFailed
			task.Error = "execution lease expired"
			task.ExecutionID = ""
			task.LeaseOwner = ""
			task.LeaseExpiresAt = nil
			task.FinishedAt = &now
			task.UpdatedAt = now
			events = append(events, models.PlanEvent{
				PlanID:     planID,
				EventType:  "task_failed",
				TaskID:     task.ID,
				TaskStatus: string(task.Status),
				Payload:    map[string]any{"error": task.Error, "reason": "lease_expired"},
				Timestamp:  now,
			})
		}
		fillMeta(plan)
		return nil
	})
	if err != nil {
		return err
	}
	for _, event := range events {
		s.publishAndStore(planID, event)
	}
	return nil
}

func resetTaskForRetry(task *models.TaskNode, now time.Time) {
	task.Status = models.StatusPending
	task.Result = ""
	task.Error = ""
	task.ExecutionID = ""
	task.LeaseOwner = ""
	task.LeaseExpiresAt = nil
	task.RunCount = 0
	task.StartedAt = nil
	task.FinishedAt = nil
	task.UpdatedAt = now
}

func resetBlockedDependents(plan *models.PlanGraph, taskID string, now time.Time) {
	visited := map[string]bool{}
	var visit func(string)
	visit = func(currentID string) {
		for _, next := range getOutgoingNodes(plan, currentID) {
			if next == nil || visited[next.ID] {
				continue
			}
			visited[next.ID] = true
			if next.Status == models.StatusBlocked || next.Status == models.StatusCanceled {
				resetTaskForRetry(next, now)
			}
			visit(next.ID)
		}
	}
	visit(taskID)
}

// approvalTimeoutSeconds is the independent budget for waiting on a human
// decision. It never counts toward the plan execution duration budget.
func approvalTimeoutSeconds() int {
	const fallback = 1800
	raw := strings.TrimSpace(os.Getenv("APPROVAL_TIMEOUT_SECONDS"))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// effectivePlanElapsed excludes approval wait windows from the execution budget.
func effectivePlanElapsed(plan *models.PlanGraph, now time.Time) time.Duration {
	if plan == nil || plan.Usage.StartedAt == nil {
		return 0
	}
	elapsed := now.Sub(*plan.Usage.StartedAt)
	elapsed -= time.Duration(plan.Usage.ApprovalWaitSeconds) * time.Second
	if plan.Usage.ApprovalWaitingSince != nil {
		if waiting := now.Sub(*plan.Usage.ApprovalWaitingSince); waiting > 0 {
			elapsed -= waiting
		}
	}
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func budgetExceeded(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	// A plan that finished every task successfully must not be canceled just
	// because it used its final allowed attempt. Any other terminal mix
	// (failure/blocked/canceled) still means the budget was exhausted.
	if plan.Budget.MaxTaskAttempts > 0 && plan.Usage.TaskAttempts >= plan.Budget.MaxTaskAttempts && noRunning(plan) && !allSucceeded(plan) {
		return true
	}
	if plan.Budget.MaxDurationSec <= 0 || plan.Usage.StartedAt == nil {
		return false
	}
	return effectivePlanElapsed(plan, time.Now()) > time.Duration(plan.Budget.MaxDurationSec)*time.Second
}

// syncApprovalWaitWindow keeps RunUsage.ApprovalWaitingSince in sync with the
// actual awaiting-approval state so the execution budget can be paused.
func (s *Scheduler) syncApprovalWaitWindow(planID string) {
	_ = s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		now := time.Now()
		waiting := hasAwaitingApproval(plan)
		if waiting && plan.Usage.ApprovalWaitingSince == nil {
			plan.Usage.ApprovalWaitingSince = &now
			plan.UpdatedAt = now
			return nil
		}
		if !waiting && plan.Usage.ApprovalWaitingSince != nil {
			waited := now.Sub(*plan.Usage.ApprovalWaitingSince)
			if waited > 0 {
				plan.Usage.ApprovalWaitSeconds += int(waited / time.Second)
			}
			plan.Usage.ApprovalWaitingSince = nil
			plan.UpdatedAt = now
		}
		return nil
	})
}

// expireApprovalActions turns every pending runtime action that exceeded
// APPROVAL_TIMEOUT_SECONDS into an expired action, blocks its task and moves the
// plan to a blocked terminal state with an actionable missing requirement.
func (s *Scheduler) expireApprovalActions(planID string) (bool, error) {
	timeout := time.Duration(approvalTimeoutSeconds()) * time.Second
	expired := false
	events := []models.PlanEvent{}
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		now := time.Now()
		for i := range plan.RuntimeActions {
			action := &plan.RuntimeActions[i]
			if action.Status != models.RuntimeActionPending {
				continue
			}
			if action.RequestedAt.IsZero() || now.Sub(action.RequestedAt) < timeout {
				continue
			}
			action.Status = models.RuntimeActionExpired
			action.DecidedAt = &now
			action.DecidedBy = "system:approval_timeout"
			action.DecisionNote = "approval timed out"
			expired = true
			task := getNodeByID(plan, action.TaskID)
			if task != nil {
				task.Status = models.StatusBlocked
				task.Error = "approval timed out after " + strconv.Itoa(approvalTimeoutSeconds()) + "s"
				task.FinishedAt = &now
				task.UpdatedAt = now
			}
			plan.MissingRequirements = appendMissingRequirement(plan.MissingRequirements, models.MissingRequirement{
				Kind:        "approval_timeout",
				Name:        string(action.Kind),
				Detail:      "等待人工审批超过 APPROVAL_TIMEOUT_SECONDS，动作已过期",
				Remediation: "重新创建计划并在审批窗口内确认该操作。",
			})
			events = append(events, models.PlanEvent{
				PlanID:    plan.ID,
				TaskID:    action.TaskID,
				EventType: "runtime_action_expired",
				Payload:   map[string]any{"action": action, "reason": "approval_timeout"},
				Timestamp: now,
			})
		}
		if expired {
			plan.Status = models.StatusFailed
			plan.Outcome = models.OutcomeBlocked
			plan.OutcomeReason = "approval timed out"
			plan.Usage.FinishedAt = &now
			plan.Usage.ApprovalWaitingSince = nil
			plan.UpdatedAt = now
			for _, task := range plan.Nodes {
				if task == nil {
					continue
				}
				switch task.Status {
				case models.StatusPending, models.StatusReady, models.StatusInProgress, models.StatusAwaitingApproval:
					task.Status = models.StatusCanceled
					task.Error = "plan aborted: approval timed out"
					task.ExecutionID = ""
					task.LeaseOwner = ""
					task.LeaseExpiresAt = nil
					task.FinishedAt = &now
					task.UpdatedAt = now
				}
			}
			fillMeta(plan)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	for _, event := range events {
		s.publishAndStore(planID, event)
	}
	if expired {
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "plan_failed",
			Payload:   map[string]any{"outcome": string(models.OutcomeBlocked), "reason": "approval_timeout"},
			Timestamp: time.Now(),
		})
		s.cleanupPlanSandboxes(context.Background(), planID)
		s.recordTerminalMetrics(planID)
	}
	s.notifyProgress()
	return expired, nil
}

func (s *Scheduler) publishDiscardedResult(planID, taskID, executionID, leaseOwner string) {
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:      planID,
		EventType:   "task_result_discarded",
		TaskID:      taskID,
		ExecutionID: executionID,
		Payload: map[string]any{
			"reason":      "stale_execution_lease",
			"lease_owner": leaseOwner,
		},
		Timestamp: time.Now(),
	})
}

func (s *Scheduler) blockDependents(planID, taskID string) error {
	events := []models.PlanEvent{}
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		visited := map[string]bool{}
		var visit func(string)
		visit = func(currentID string) {
			for _, next := range getOutgoingNodes(plan, currentID) {
				if next == nil || visited[next.ID] {
					continue
				}
				visited[next.ID] = true
				if next.Status == models.StatusPending || next.Status == models.StatusReady {
					next.Status = models.StatusBlocked
					next.UpdatedAt = time.Now()
					events = append(events, models.PlanEvent{
						PlanID:     planID,
						EventType:  "task_blocked",
						TaskID:     next.ID,
						TaskStatus: string(models.StatusBlocked),
						Payload: map[string]any{
							"reason":           "upstream_failed",
							"upstream_task_id": currentID,
						},
						Timestamp: time.Now(),
					})
				}
				visit(next.ID)
			}
		}
		visit(taskID)
		fillMeta(plan)
		return nil
	})
	if err != nil {
		return err
	}
	if len(events) > 0 {
		for _, event := range events {
			s.publishAndStore(planID, event)
		}
	}
	return nil
}
