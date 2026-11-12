package scheduler

import (
	"fmt"
	"time"

	"research-assistant-backend/internal/models"
)

// applyTaskResult is the single result-transition entry point shared by the
// scheduled dispatcher and the manual single-node endpoint. It splits waiting
// from stopping, enforces the execution fence, and commits the node state (and
// the block propagation to its hard dependents) inside one UpdatePlan call.
//
// Returned values: retrying reports a scheduled retry; waiting reports an
// accepted wait transition; err is a fence or state error (ErrStaleExecution).
func (s *Scheduler) applyTaskResult(planID, taskID, executionID, leaseOwner string, executionEpoch int, result *models.TaskExecutionResult, execErr error, allowRetry bool) (retrying bool, waiting bool, err error) {
	if result == nil {
		result = &models.TaskExecutionResult{Status: models.StatusFailed, Error: "task execution returned nil result"}
	}
	class := classifyTaskResult(result, execErr)
	if class == resultUnknown {
		class = resultFailed
		result.Status = models.StatusFailed
		if result.Error == "" {
			result.Error = protocolError("unrecognized result status").Error()
		}
	}
	switch class {
	case resultWaitingApproval, resultWaitingInput:
		transitioned, waitErr := s.applyTaskWaiting(planID, taskID, executionID, leaseOwner, executionEpoch, result, class)
		if waitErr != nil {
			return false, false, waitErr
		}
		return false, transitioned, nil
	case resultCompleted:
		if err := s.markTaskCompleted(planID, taskID, executionID, leaseOwner, executionEpoch, result); err != nil {
			return false, false, err
		}
		return false, false, nil
	default:
		if result.Error == "" {
			result.Error = fmt.Sprintf("task returned non-completed status %q", result.Status)
		}
		retrying, err := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, result, allowRetry)
		return retrying, false, err
	}
}

// waitingPendingRecord reports whether the plan holds an actionable record for
// the requested wait. A bare awaiting_* status without a pending approval or
// question is a protocol error, not a wait.
func waitingPendingRecord(plan *models.PlanGraph, taskID string, class taskResultClass) bool {
	switch class {
	case resultWaitingApproval:
		for _, action := range plan.RuntimeActions {
			if action.TaskID == taskID && action.Status == models.RuntimeActionPending {
				return true
			}
		}
		return false
	case resultWaitingInput:
		for _, question := range plan.Questions {
			if question.TaskID == taskID && question.Status == models.QuestionPending {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// applyTaskWaiting moves one task into awaiting_approval / awaiting_input
// without failing it: no Failure, no FinishedAt, no retry charge, no
// dependent blocking. It persists usage and the recovery checkpoint, records
// the pending runtime action or question, and releases the execution lease.
func (s *Scheduler) applyTaskWaiting(planID, taskID, executionID, leaseOwner string, executionEpoch int, result *models.TaskExecutionResult, class taskResultClass) (bool, error) {
	s.externalizeResult(result)
	now := time.Now()
	transitioned := false
	events := []models.PlanEvent{}
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if !executionMatches(task, executionID, leaseOwner, executionEpoch) {
			return ErrStaleExecution
		}
		status := models.StatusAwaitingInput
		if class == resultWaitingApproval {
			status = models.StatusAwaitingApproval
		}
		// Record the pending approval first: a wait without its record is a
		// protocol error and must leave the task untouched.
		recorded := false
		if class == resultWaitingApproval {
			if result.RuntimeAction != nil {
				action := *result.RuntimeAction
				action.PlanID = plan.ID
				action.TaskID = task.ID
				if action.Status == "" {
					action.Status = models.RuntimeActionPending
				}
				if action.RequestedAt.IsZero() {
					action.RequestedAt = now
				}
				found := false
				for i := range plan.RuntimeActions {
					if plan.RuntimeActions[i].ID == action.ID {
						plan.RuntimeActions[i] = action
						found = true
						break
					}
				}
				if !found {
					plan.RuntimeActions = append(plan.RuntimeActions, action)
					task.RuntimeActionIDs = append(task.RuntimeActionIDs, action.ID)
				}
				events = append(events, models.PlanEvent{
					PlanID:     plan.ID,
					TaskID:     task.ID,
					EventType:  "runtime_action_requested",
					TaskStatus: string(models.StatusAwaitingApproval),
					Payload:    map[string]any{"action": action},
					Timestamp:  now,
				})
			}
			recorded = waitingPendingRecord(plan, task.ID, class)
		} else {
			if result.Question != nil {
				question := *result.Question
				if question.ID == "" {
					question.ID = "q-" + fmt.Sprintf("%d", now.UnixNano())
				}
				question.PlanID = plan.ID
				question.TaskID = task.ID
				question.NodeType = task.Type
				question.Revision = task.Revision
				question.Status = models.QuestionPending
				if question.RequestedAt.IsZero() {
					question.RequestedAt = now
				}
				if question.ExpiresAt == nil {
					expires := now.Add(time.Duration(questionTimeoutSeconds()) * time.Second)
					question.ExpiresAt = &expires
				}
				plan.Questions = append(plan.Questions, question)
				events = append(events, models.PlanEvent{
					PlanID:    plan.ID,
					TaskID:    task.ID,
					EventType: "question_requested",
					Revision:  task.Revision,
					Payload: map[string]any{
						"question":    question,
						"question_id": question.ID,
						"header":      question.Header,
						"prompt":      question.Prompt,
					},
					Timestamp: now,
				})
			}
			recorded = waitingPendingRecord(plan, task.ID, class)
		}
		if !recorded {
			return protocolError(fmt.Sprintf("task %s reported %s without a pending approval or question record", taskID, status))
		}

		// A wait is not a failure: keep the error empty and the finish boundary
		// unset, mark the recovery checkpoint as waiting, and never charge a
		// retry. TaskAttempts stays the real number of started executions.
		state := models.RecoveryState{ChainID: recoveryChainID(plan, task)}
		if task.Recovery != nil {
			state = *task.Recovery
		}
		state.Waiting = true
		state.PendingRetry = false
		task.Recovery = &state
		task.Status = status
		task.Error = ""
		task.FinishedAt = nil
		task.NextAttemptAt = nil
		task.ExecutionID = ""
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		task.UpdatedAt = now
		if result.Result != "" {
			task.Result = result.Result
		}
		if result.Code != "" {
			task.Code = result.Code
		}
		if result.StructuredData != "" {
			task.StructuredData = result.StructuredData
		}
		if result.Metadata != nil {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			for key, value := range result.Metadata {
				task.Metadata[key] = value
			}
		}
		applyTaskUsage(task, plan, result.Usage)
		if result.ToolCall != nil {
			upsertToolCallRecord(plan, result.ToolCall)
		}
		for i := range result.ToolCalls {
			upsertToolCallRecord(plan, &result.ToolCalls[i])
		}
		if result.AgentRun != nil {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["agent_run"] = result.AgentRun
		}
		plan.Status = status
		plan.Outcome = ""
		plan.OutcomeReason = ""
		plan.UpdatedAt = now
		transitioned = true
		fillMeta(plan)
		return nil
	})
	if err != nil {
		return false, err
	}
	for _, event := range events {
		s.publishAndStore(planID, event)
	}
	return transitioned, nil
}

// applyQuestionFromTask is the compatibility wrapper used by callers that only
// have a question. It routes through the same waiting transition.
func (s *Scheduler) applyQuestionFromTask(planID, taskID string, question *models.PlanQuestion) error {
	return s.applyQuestionWaiting(planID, taskID, "", "", 0, question)
}

// applyQuestionWaiting records a structured question. When the caller holds an
// execution fence (taskID from a running execution), pass its identifiers; the
// envelope is validated exactly like every other result.
func (s *Scheduler) applyQuestionWaiting(planID, taskID, executionID, leaseOwner string, executionEpoch int, question *models.PlanQuestion) error {
	if question == nil {
		return fmt.Errorf("question is nil")
	}
	now := time.Now().UTC()
	events := []models.PlanEvent{}
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		// The question is subject to the same execution fence as any other
		// result: a stale execution cannot pause the task it no longer owns.
		if executionID != "" || leaseOwner != "" || executionEpoch != 0 {
			if !executionMatches(task, executionID, leaseOwner, executionEpoch) {
				return ErrStaleExecution
			}
		}
		record := *question
		if record.ID == "" {
			record.ID = fmt.Sprintf("q-%d", now.UnixNano())
		}
		record.PlanID = planID
		record.TaskID = taskID
		record.NodeType = task.Type
		record.Revision = task.Revision
		record.Status = models.QuestionPending
		if record.RequestedAt.IsZero() {
			record.RequestedAt = now
		}
		if record.ExpiresAt == nil {
			expires := now.Add(time.Duration(questionTimeoutSeconds()) * time.Second)
			record.ExpiresAt = &expires
		}
		plan.Questions = append(plan.Questions, record)
		task.Status = models.StatusAwaitingInput
		task.Error = ""
		task.FinishedAt = nil
		task.ExecutionID = ""
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		task.UpdatedAt = now
		state := models.RecoveryState{ChainID: recoveryChainID(plan, task)}
		if task.Recovery != nil {
			state = *task.Recovery
		}
		state.Waiting = true
		state.PendingRetry = false
		task.Recovery = &state
		plan.Status = models.StatusAwaitingInput
		plan.Outcome = ""
		plan.OutcomeReason = ""
		plan.UpdatedAt = now
		fillMeta(plan)
		events = append(events, models.PlanEvent{
			PlanID:    planID,
			TaskID:    taskID,
			EventType: "question_requested",
			Revision:  task.Revision,
			Payload: map[string]any{
				"question":    record,
				"question_id": record.ID,
				"header":      record.Header,
				"prompt":      record.Prompt,
			},
			Timestamp: now,
		})
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
