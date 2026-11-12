package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/recovery"
	"strings"
	"time"

	"github.com/google/uuid"
)

// dispatchGuard is an optional legality check evaluated on the committed graph
// at lease-acquisition time. A ready snapshot is only a hint: the node may have
// been blocked between the scan and this commit.
type dispatchGuard func(plan *models.PlanGraph, task *models.TaskNode) error

// errTaskNotDispatchable marks a dispatch refused because the node is no longer
// runnable. It is not an execution error and must not fail the plan.
var errTaskNotDispatchable = errors.New("task is not dispatchable")

// dispatchLegality re-validates a node right before it takes an execution
// lease: enabled, hard dependencies met, no blocking cause, an approved
// runtime action. It is shared by automatic dispatch and manual execution.
func dispatchLegality(plan *models.PlanGraph, task *models.TaskNode) error {
	if task == nil {
		return fmt.Errorf("%w: task not found", errTaskNotDispatchable)
	}
	if plan == nil {
		return fmt.Errorf("%w: plan not found", errTaskNotDispatchable)
	}
	if !task.IsEnabled() {
		return fmt.Errorf("%w: task is disabled", errTaskNotDispatchable)
	}
	switch task.Status {
	case models.StatusBlocked:
		code := "blocked"
		if task.BlockInfo != nil && task.BlockInfo.Code != "" {
			code = task.BlockInfo.Code
		}
		return fmt.Errorf("%w: task is blocked (%s)", errTaskNotDispatchable, code)
	case models.StatusCompleted, models.StatusSkipped, models.StatusCanceled:
		return fmt.Errorf("%w: task already ended as %s", errTaskNotDispatchable, task.Status)
	case models.StatusInProgress:
		// Another execution already owns this node. Checking the status on a
		// snapshot is not enough: the snapshot the caller read may predate the
		// other request's lease.
		return fmt.Errorf("%w: task is already running", errTaskNotDispatchable)
	case models.StatusAwaitingApproval, models.StatusAwaitingInput:
		// A wait means a human decision is expected first. Resuming it goes
		// through the decision endpoint, which moves the node back to pending.
		return fmt.Errorf("%w: task is waiting as %s", errTaskNotDispatchable, task.Status)
	}
	if taskHasLiveLease(task) {
		return fmt.Errorf("%w: task still holds an execution lease", errTaskNotDispatchable)
	}
	if !hardDependenciesSatisfied(plan, task) {
		return fmt.Errorf("%w: hard dependencies are not satisfied", errTaskNotDispatchable)
	}
	if !optionalDependenciesSatisfied(plan, task) {
		return fmt.Errorf("%w: optional dependencies have not ended", errTaskNotDispatchable)
	}
	if !requiredArtifactsSatisfied(plan, task) {
		return fmt.Errorf("%w: required artifacts are missing", errTaskNotDispatchable)
	}
	if action := pendingRuntimeActionFor(plan, task.ID); action != nil {
		return fmt.Errorf("%w: runtime action %s is still pending", errTaskNotDispatchable, action.ID)
	}
	if question := pendingQuestionFor(plan, task.ID); question != nil {
		return fmt.Errorf("%w: question %s is still pending", errTaskNotDispatchable, question.ID)
	}
	return nil
}

// taskHasLiveLease reports whether a node still holds an unexpired execution
// lease. A missing expiry counts as live: an execution record without a
// deadline must not be taken over silently.
func taskHasLiveLease(task *models.TaskNode) bool {
	if task == nil {
		return false
	}
	if strings.TrimSpace(task.ExecutionID) == "" && strings.TrimSpace(task.LeaseOwner) == "" {
		return false
	}
	if task.LeaseExpiresAt == nil {
		return true
	}
	return time.Now().Before(task.LeaseExpiresAt.Add(taskLeaseGrace))
}

// pendingQuestionFor returns the pending structured question of a node.
func pendingQuestionFor(plan *models.PlanGraph, taskID string) *models.PlanQuestion {
	if plan == nil {
		return nil
	}
	for i := range plan.Questions {
		question := &plan.Questions[i]
		if question.TaskID == taskID && question.Status == models.QuestionPending {
			return question
		}
	}
	return nil
}

func taskDispatchGuard(plan *models.PlanGraph, taskID string) dispatchGuard {
	return func(current *models.PlanGraph, task *models.TaskNode) error {
		if task != nil && task.ID != taskID {
			return fmt.Errorf("%w: unexpected task", errTaskNotDispatchable)
		}
		return dispatchLegality(current, task)
	}
}

func pendingRuntimeActionFor(plan *models.PlanGraph, taskID string) *models.RuntimeActionRequest {
	if plan == nil {
		return nil
	}
	for i := range plan.RuntimeActions {
		action := &plan.RuntimeActions[i]
		if action.TaskID == taskID && action.Status == models.RuntimeActionPending {
			return action
		}
	}
	return nil
}

func (s *Scheduler) markTaskStarted(planID, taskID string, guards ...dispatchGuard) (string, string, int, error) {
	now := time.Now()
	executionID := uuid.NewString()
	leaseOwner := ""
	executionEpoch := 0
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status == models.StatusCanceled {
			return ErrPlanCanceled
		}
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		for _, guard := range guards {
			if guard == nil {
				continue
			}
			if err := guard(plan, task); err != nil {
				return err
			}
		}
		// Starting a node while the plan itself is parked on a human decision
		// would run work the wait exists to prevent. The decision endpoint moves
		// the plan back to a runnable state first.
		switch plan.Status {
		case models.StatusAwaitingApproval, models.StatusAwaitingInput:
			return fmt.Errorf("%w: plan is waiting as %s", errTaskNotDispatchable, plan.Status)
		}
		if plan.Budget.MaxTaskAttempts > 0 && plan.Usage.TaskAttempts >= plan.Budget.MaxTaskAttempts {
			return fmt.Errorf("task attempt budget exceeded")
		}
		if task.Recovery != nil && (task.RunCount > 0 || task.Recovery.HTTPAttempts > 0 || task.Failure != nil) {
			state := *task.Recovery
			if !state.PendingRetry && !state.Waiting && task.Status != models.StatusAwaitingApproval && task.Status != models.StatusAwaitingInput {
				controller := recovery.New(state, nil)
				if err := controller.Reserve("explicit_task_retry", task.Failure); err != nil {
					return err
				}
				state = controller.Snapshot()
			}
			state.PendingRetry = false
			state.Waiting = false
			task.Recovery = &state
		}
		leaseOwner = task.AssignedTo
		timeout := time.Duration(task.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 10 * time.Minute
		}
		leaseExpiresAt := now.Add(timeout)
		task.Status = models.StatusInProgress
		task.ExecutionID = executionID
		task.ExecutionEpoch++
		executionEpoch = task.ExecutionEpoch
		task.LeaseOwner = leaseOwner
		task.LeaseExpiresAt = &leaseExpiresAt
		task.RunCount++
		task.NextAttemptAt = nil
		task.StartedAt = &now
		task.FinishedAt = nil
		// A restarted node must not keep the previous run's block explanation.
		task.BlockInfo = nil
		task.Error = ""
		task.UpdatedAt = now
		plan.Usage.TaskAttempts++
		fillMeta(plan)
		return nil
	}); err != nil {
		return "", "", 0, err
	}

	s.publishAndStore(planID, models.PlanEvent{
		PlanID:      planID,
		EventType:   "task_started",
		TaskID:      taskID,
		TaskStatus:  string(models.StatusInProgress),
		ExecutionID: executionID,
		Payload:     map[string]any{"lease_owner": leaseOwner, "execution_epoch": executionEpoch},
		Timestamp:   now,
	})
	return executionID, leaseOwner, executionEpoch, nil
}

func (s *Scheduler) markTaskCompleted(planID, taskID, executionID, leaseOwner string, executionEpoch int, result *models.TaskExecutionResult) error {
	now := time.Now()
	s.externalizeResult(result)
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if !executionMatches(task, executionID, leaseOwner, executionEpoch) {
			return ErrStaleExecution
		}
		task.Status = models.StatusCompleted
		task.Result = result.Result
		task.Code = result.Code
		task.StructuredData = result.StructuredData
		task.ImageBase64 = result.ImageBase64
		if result.ImageBlobRef != "" {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["image_blob_ref"] = result.ImageBlobRef
		}
		task.Error = ""
		task.BlockInfo = nil
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		if result.Recovery != nil {
			task.Recovery = result.Recovery
		}
		task.Failure = nil
		task.ExecutionID = ""
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		task.FinishedAt = &now
		task.UpdatedAt = now
		if len(result.Metadata) > 0 {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			for key, value := range result.Metadata {
				task.Metadata[key] = value
			}
		}
		// Server provenance is authoritative even if a runner returns the same
		// metadata keys. Lease cleanup never removes this completion boundary.
		task.Metadata["completed_execution_id"] = executionID
		task.Metadata["completed_execution_epoch"] = executionEpoch
		revision := task.Revision
		if revision == 0 {
			revision = plan.CurrentRevision
		}
		task.Metadata["completed_revision"] = revision
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
		if result.Verification != nil {
			task.Verification = result.Verification
			plan.Verification = result.Verification
			// Stamp every image artifact the plan has produced so the UI can show a
			// per-figure badge without joining the verification record.
			for key, artifact := range plan.Artifacts {
				annotateArtifactVerification(&artifact, result.Verification)
				plan.Artifacts[key] = artifact
			}
		}

		seenArtifacts := map[string]struct{}{}
		for _, artifact := range result.Artifacts {
			key := strings.TrimSpace(artifact.Key)
			if key == "" {
				return fmt.Errorf("task %s produced an artifact with an empty key", taskID)
			}
			if strings.TrimSpace(artifact.Value) == "" && strings.TrimSpace(artifact.Location) == "" && strings.TrimSpace(artifact.BlobRef) == "" {
				// Optional image artifacts (execution_image) may legitimately be
				// empty when a step produced no figure; downstream binding
				// treats the empty value as "no upstream image".
				if !strings.Contains(strings.ToLower(key), "image") {
					return fmt.Errorf("task %s produced empty artifact %q", taskID, key)
				}
			}
			if _, exists := seenArtifacts[key]; exists {
				return fmt.Errorf("task %s produced duplicate artifact %q", taskID, key)
			}
			if existing, exists := plan.Artifacts[key]; exists &&
				strings.TrimSpace(existing.ProducerTaskID) != "" &&
				existing.ProducerTaskID != taskID {
				return fmt.Errorf("artifact %q was already produced by task %s", key, existing.ProducerTaskID)
			}
			if result.Verification != nil {
				annotateArtifactVerification(&artifact, result.Verification)
			}
			// Stamp the revision that produced this artifact so a later diff or
			// export can tell which version of the plan generated it.
			if artifact.Revision == 0 {
				artifact.Revision = plan.CurrentRevision
			}
			// Stamp the execution that produced it. Acceptance evidence uses this
			// to prove the artifact belongs to the attempt that just succeeded: a
			// retry that does not re-produce the value keeps the old stamp and is
			// therefore never re-certified.
			if artifact.Metadata == nil {
				artifact.Metadata = map[string]any{}
			}
			artifact.Metadata["execution_id"] = executionID
			// A successfully produced artifact is evidence; a diagnostic left by a
			// failed attempt is not, even when the node later reports completed.
			artifact.Metadata["evidence"] = "verified"
			seenArtifacts[key] = struct{}{}
			plan.Artifacts[key] = artifact
		}

		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}

	if result.ToolCall != nil {
		eventType := "tool_call_completed"
		if result.ToolCall.Status != models.ToolCallCompleted {
			eventType = "tool_call_failed"
		}
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:     planID,
			TaskID:     taskID,
			EventType:  eventType,
			TaskStatus: string(models.StatusCompleted),
			Payload:    map[string]any{"tool_call": result.ToolCall},
			Timestamp:  now,
		})
	}

	if len(result.Artifacts) > 0 {
		keys := make([]string, 0, len(result.Artifacts))
		for _, artifact := range result.Artifacts {
			keys = append(keys, artifact.Key)
		}
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "artifact_created",
			TaskID:    taskID,
			Payload: map[string]any{
				"artifact_keys": keys,
				"artifacts":     summarizeArtifacts(result.Artifacts),
			},
			Timestamp: now,
		})
	}

	s.publishAndStore(planID, models.PlanEvent{
		PlanID:      planID,
		EventType:   "task_completed",
		TaskID:      taskID,
		TaskStatus:  string(models.StatusCompleted),
		ExecutionID: executionID,
		Payload: map[string]any{
			"result_summary":  result.Result,
			"result":          result.Result,
			"code":            result.Code,
			"structured_data": result.StructuredData,
			"image_base64":    result.ImageBase64,
			"image_blob_ref":  result.ImageBlobRef,
		},
		Timestamp: now,
	})
	if result.Verification != nil {
		eventType := "verification_completed"
		if result.Verification.Status != models.VerificationPass && result.Verification.Status != models.VerificationSkipped {
			eventType = "verification_failed"
		}
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:     planID,
			EventType:  eventType,
			TaskID:     taskID,
			TaskStatus: string(models.StatusCompleted),
			Payload:    verificationEventPayload(result.Verification),
			Timestamp:  now,
		})
	}
	return nil
}

// annotateArtifactVerification stamps the per-artifact verdict onto image
// artifacts so the UI can show a corner badge without joining the plan.
func annotateArtifactVerification(artifact *models.Artifact, verification *models.Verification) {
	if artifact == nil || verification == nil {
		return
	}
	key := strings.ToLower(artifact.Key)
	if !strings.Contains(key, "image") && !strings.Contains(key, "plot") {
		return
	}
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]any{}
	}
	artifact.Metadata["verification"] = map[string]any{
		"status":   verification.Status,
		"level":    verification.Level,
		"verified": verification.Verified,
		"reason":   verification.Reason,
		"issues":   verification.Issues,
	}
}

func verificationEventPayload(verification *models.Verification) map[string]any {
	if verification == nil {
		return nil
	}
	return map[string]any{
		"verification": map[string]any{
			"status":      verification.Status,
			"level":       verification.Level,
			"verified":    verification.Verified,
			"reason":      verification.Reason,
			"issues":      verification.Issues,
			"confidence":  verification.Confidence,
			"verified_by": verification.VerifiedBy,
			"checked_at":  verification.CheckedAt,
		},
	}
}

func (s *Scheduler) markTaskFailed(planID, taskID, executionID, leaseOwner string, executionEpoch int, result *models.TaskExecutionResult, allowRetry bool) (bool, error) {
	now := time.Now()
	s.externalizeResult(result)
	retrying := false
	blockedStatus := false
	propagationEvents := []models.PlanEvent{}
	var nodeBlockInfo *models.BlockInfo
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if !executionMatches(task, executionID, leaseOwner, executionEpoch) {
			return ErrStaleExecution
		}
		task.Error = result.Error
		if result.Recovery != nil {
			task.Recovery = result.Recovery
		}
		failure := result.Failure
		if failure == nil {
			failure = recovery.Classify(fmt.Errorf("%s", result.Error), "task", recovery.Hash([]any{task.Contract, task.Inputs, result.Code, plan.EnvironmentManifest}))
		}
		task.Failure = failure
		result.Failure = failure
		stopReason := failure.Code
		canRetry := allowRetry && task.RunCount <= task.RetryLimit
		state := models.RecoveryState{ChainID: recovery.Hash([]string{plan.OwnerID, plan.ID, task.ID})}
		if task.Recovery != nil {
			state = *task.Recovery
		}
		controller := recovery.New(state, nil)
		classified := !strings.EqualFold(os.Getenv("AGENT_CLASSIFIED_RECOVERY"), "false")
		if classified && failure.Class != "transient" {
			canRetry = false
		}
		if failure.Class == "canceled" || failure.Class == "budget" || failure.Class == "permission" || failure.Class == "missing_input" {
			canRetry = false
		}
		if canRetry {
			if err := controller.Reserve("task_retry", failure); err != nil {
				canRetry = false
				stopReason = err.Error()
				task.Error = result.Error + "; " + err.Error()
			}
		}
		next := controller.Snapshot()
		next.PendingRetry = canRetry
		if !canRetry {
			action := "stop"
			if failure.Class == "missing_input" || failure.Class == "permission" {
				action = "wait"
			}
			if failure.Class == "canceled" {
				action = "cancel"
			}
			next.Decisions = append(next.Decisions, models.RecoveryDecision{Action: action, Reason: stopReason, FailureFingerprint: failure.Fingerprint, Remaining: next.MaxActions - next.UsedActions, At: now})
		}
		task.Recovery = &next
		if classified && !canRetry && (failure.Class == "environment" || failure.Class == "deterministic") && next.Replans == 0 {
			task.ReplanProposal = &models.ReplanProposal{Reason: "Execution requires a reviewed input, environment or plan change", FailureFingerprint: failure.Fingerprint, BaseRevision: plan.CurrentRevision, RequiresReview: true, FrozenContractHash: recovery.Hash(task.Contract), InvalidateArtifacts: append([]string(nil), task.OutputArtifacts...)}
			task.ReplanProposal.SuggestedChanges = []string{"Review failed task " + task.ID + " and supply a compatible environment or missing dependency before rerunning affected descendants"}
			task.ReplanProposal.PermissionChanges = []string{}
			task.ReplanProposal.AcceptanceContractUnchanged = true
			next.Replans++
			next.Decisions = append(next.Decisions, models.RecoveryDecision{Action: "replan_proposal", Reason: failure.Code, FailureFingerprint: failure.Fingerprint, Remaining: next.MaxActions - next.UsedActions, At: now})
			task.Recovery = &next
		}
		if len(result.Metadata) > 0 {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			for key, value := range result.Metadata {
				task.Metadata[key] = value
			}
		}
		// Keep whatever the agent produced before failing: partial code, metrics
		// or structured evidence is still useful for debugging and the UI.
		if strings.TrimSpace(result.Result) != "" {
			task.Result = result.Result
		}
		if strings.TrimSpace(result.Code) != "" {
			task.Code = result.Code
		}
		if strings.TrimSpace(result.StructuredData) != "" {
			task.StructuredData = result.StructuredData
		}
		if strings.TrimSpace(result.ImageBase64) != "" {
			task.ImageBase64 = result.ImageBase64
		}
		if result.ImageBlobRef != "" {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["image_blob_ref"] = result.ImageBlobRef
		}
		for _, artifact := range result.Artifacts {
			key := strings.TrimSpace(artifact.Key)
			if key == "" || (strings.TrimSpace(artifact.Value) == "" && strings.TrimSpace(artifact.Location) == "") {
				continue
			}
			// Keep the completion path's producer guard: a failed attempt may
			// record its own diagnostics but must never overwrite another task's
			// output.
			if existing, exists := plan.Artifacts[key]; exists &&
				strings.TrimSpace(existing.ProducerTaskID) != "" &&
				existing.ProducerTaskID != taskID {
				continue
			}
			if artifact.Metadata == nil {
				artifact.Metadata = map[string]any{}
			}
			// Stamp the attempt that actually wrote it and label it as diagnostic.
			// Acceptance evidence then rejects it until a later attempt re-produces
			// the value, so an automatic retry that produces nothing cannot inherit
			// the failed run's output (V3-01).
			artifact.Metadata["execution_id"] = executionID
			artifact.Metadata["evidence"] = "diagnostic"
			plan.Artifacts[key] = artifact
		}
		for _, missing := range result.MissingRequirements {
			if missing.Kind == "" {
				continue
			}
			plan.MissingRequirements = appendMissingRequirement(plan.MissingRequirements, missing)
		}
		task.ExecutionID = ""
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		applyTaskUsage(task, plan, result.Usage)
		if result.ToolCall != nil {
			upsertToolCallRecord(plan, result.ToolCall)
		}
		blocked := result.Status == models.StatusBlocked
		for i := range result.ToolCalls {
			upsertToolCallRecord(plan, &result.ToolCalls[i])
		}
		if result.AgentRun != nil {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["agent_run"] = result.AgentRun
		}
		// A runtime action that arrives with a real execution exception is
		// diagnostic evidence, not a wait: the exception keeps its priority so a
		// failed execution can never be turned into a pending approval.
		if result.RuntimeAction != nil {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["discarded_runtime_action"] = result.RuntimeAction
		}
		// markTaskFailed never writes a wait: waiting transitions go through
		// applyTaskWaiting, and an execution exception always outranks a wait.
		if blocked && task.BlockInfo == nil {
			task.BlockInfo = &models.BlockInfo{
				Code:       models.BlockUpstreamBlocked,
				Message:    truncateMessage(result.Error, 200),
				OccurredAt: now,
				Causes: []models.BlockCause{{
					Code:       models.BlockUpstreamBlocked,
					RootTaskID: task.ID,
					Status:     models.StatusBlocked,
					Message:    truncateMessage(result.Error, 200),
				}},
			}
		}
		if blocked {
			task.Status = models.StatusBlocked
			blockedStatus = true
			task.FinishedAt = &now
		} else if failure.Class == "canceled" {
			task.Status = models.StatusCanceled
			task.FinishedAt = &now
		} else if failure.Class == "missing_input" || failure.Class == "permission" {
			task.Status = models.StatusBlocked
			blockedStatus = true
			task.FinishedAt = &now
		} else if canRetry {
			task.Status = models.StatusPending
			task.FinishedAt = nil
			delay := time.Duration(500*(1<<min(next.UsedActions-1, 5))) * time.Millisecond
			if suggested := time.Duration(failure.RetryAfterMS) * time.Millisecond; suggested > delay {
				delay = suggested
			}
			nextAttempt := now.Add(delay)
			task.NextAttemptAt = &nextAttempt
			retrying = true
		} else {
			task.Status = models.StatusFailed
			task.FinishedAt = &now
		}
		task.UpdatedAt = now
		nodeBlockInfo = task.BlockInfo
		// A real stop blocks its hard dependents in the same state transition so
		// the graph never exposes "upstream ended, dependents unclassified".
		// A scheduled retry and a wait never block anything.
		if !retrying && isTerminalStatus(task.Status) {
			propagationEvents = append(propagationEvents, propagateBlockToDependents(plan, task, now)...)
		}
		fillMeta(plan)
		return nil
	}); err != nil {
		return false, err
	}
	for _, event := range propagationEvents {
		s.publishAndStore(planID, event)
	}

	if result.ToolCall != nil {
		eventType := "tool_call_failed"
		if result.ToolCall.Status == models.ToolCallRejected {
			eventType = "tool_call_rejected"
		}
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:     planID,
			TaskID:     taskID,
			EventType:  eventType,
			TaskStatus: string(models.StatusFailed),
			Payload:    map[string]any{"tool_call": result.ToolCall},
			Timestamp:  now,
		})
	}
	if retrying {
		s.publishAndStore(planID, models.PlanEvent{PlanID: planID, EventType: "task_retrying", TaskID: taskID, TaskStatus: string(models.StatusPending), ExecutionID: executionID, Payload: map[string]any{"error": result.Error}, Timestamp: now})
		return true, nil
	}
	eventType := "task_failed"
	taskStatus := models.StatusFailed
	if blockedStatus {
		eventType = "task_blocked"
		taskStatus = models.StatusBlocked
	}
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:      planID,
		EventType:   eventType,
		TaskID:      taskID,
		TaskStatus:  string(taskStatus),
		ExecutionID: executionID,
		Payload: map[string]any{
			"error":                result.Error,
			"missing_requirements": result.MissingRequirements,
			"failure":              result.Failure,
			"block_info":           nodeBlockInfo,
		},
		Timestamp: now,
	})
	return false, nil
}

// taskAllowedRetry decides whether an automatic retry may be scheduled for
// this node. It replaces the former parameter-ignoring policy switch: a wait
// never returns here, a canceled plan never retries, and an explicit per-node
// disable is honoured instead of being silently ignored.
func taskAllowedRetry(task *models.TaskNode) bool {
	if task == nil {
		return false
	}
	return task.IsEnabled()
}

func summarizeTaskInputs(plan *models.PlanGraph, node *models.TaskNode) []map[string]any {
	if plan == nil || node == nil {
		return nil
	}

	inputs := []map[string]any{}
	for _, key := range node.RequiredArtifacts {
		artifact, ok := plan.Artifacts[key]
		if !ok {
			continue
		}
		inputs = append(inputs, map[string]any{
			"key":              key,
			"type":             artifact.Type,
			"producer_task_id": artifact.ProducerTaskID,
			"value_preview":    previewValue(artifact.Value),
		})
	}
	return inputs
}

func summarizeArtifacts(artifacts []models.Artifact) []map[string]any {
	out := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, map[string]any{
			"key":              artifact.Key,
			"type":             artifact.Type,
			"producer_task_id": artifact.ProducerTaskID,
			"blob_ref":         artifact.BlobRef,
			"location":         artifact.Location,
			"value_preview":    previewValue(artifact.Value),
		})
	}
	return out
}

func previewValue(value string) string {
	const limit = 1000
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func executionMatches(task *models.TaskNode, executionID, leaseOwner string, executionEpoch int) bool {
	return task != nil &&
		task.Status == models.StatusInProgress &&
		task.ExecutionID == executionID &&
		task.ExecutionEpoch == executionEpoch &&
		task.LeaseOwner == leaseOwner &&
		task.AssignedTo == leaseOwner &&
		(task.LeaseExpiresAt == nil || time.Now().Before(task.LeaseExpiresAt.Add(taskLeaseGrace)))
}

func (s *Scheduler) finishPlanTerminal(planID string, status models.TaskStatus, reason string) {
	now := time.Now()
	changed := false
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		switch plan.Status {
		case models.StatusCompleted, models.StatusFailed, models.StatusCanceled:
			return nil
		}
		changed = true
		plan.Status = status
		// Every new terminal status must explain itself: a failed plan without an
		// outcome is exactly the empty-OUTCOME bug this transition prevents.
		switch status {
		case models.StatusFailed:
			if plan.Outcome == "" {
				plan.Outcome = models.OutcomeFailed
			}
		case models.StatusCanceled:
			plan.Outcome = models.OutcomeFailed
		}
		if plan.OutcomeReason == "" {
			plan.OutcomeReason = reason
		}
		if plan.OutcomeDetails == nil && plan.Outcome != "" {
			plan.OutcomeDetails = &models.OutcomeDetails{Mode: "stop", Limitations: []string{reason}}
		}
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
		fillMeta(plan)
		return nil
	})
	if err != nil {
		log.Printf("scheduler: finalize plan %s failed: %v", planID, err)
		return
	}
	if !changed {
		// The plan was already terminal. Still run the shared pipeline: another
		// path may have committed the status and stopped before its side effects,
		// and the persisted claim keeps this from repeating them.
		s.completeTerminalOnce(context.Background(), planID)
		return
	}
	// Publish the committed status and run the terminal side effects through the
	// same one-shot claim the reconciliation loop uses.
	eventType := "plan_failed"
	if status == models.StatusCanceled {
		eventType = "plan_canceled"
	}
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: eventType,
		Payload:   map[string]any{"reason": reason},
		Timestamp: now,
	})
	s.claimTerminalCommit(planID)
	s.cleanupPlanSandboxes(context.Background(), planID)
	s.recordTerminalMetrics(planID)
	s.notifyProgress()
}

// upsertToolCallRecord merges a tool call audit record into the plan inside
// the same state transition that changes the task status, so a concurrent
// plan write cannot lose it.
func upsertToolCallRecord(plan *models.PlanGraph, record *models.ToolCallRecord) {
	if plan == nil || record == nil || strings.TrimSpace(record.ID) == "" {
		return
	}
	for i := range plan.ToolCalls {
		if plan.ToolCalls[i].ID == record.ID {
			plan.ToolCalls[i] = *record
			return
		}
	}
	plan.ToolCalls = append(plan.ToolCalls, *record)
}
