package scheduler

import (
	"context"
	"fmt"
	"log"
	"research-assistant-backend/internal/models"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Scheduler) markTaskStarted(planID, taskID string) (string, string, int, error) {
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
		if plan.Budget.MaxTaskAttempts > 0 && plan.Usage.TaskAttempts >= plan.Budget.MaxTaskAttempts {
			return fmt.Errorf("task attempt budget exceeded")
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
		task.StartedAt = &now
		task.FinishedAt = nil
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
		applyTaskUsage(task, plan, result.Usage)
		if result.ToolCall != nil {
			upsertToolCallRecord(plan, result.ToolCall)
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
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if !executionMatches(task, executionID, leaseOwner, executionEpoch) {
			return ErrStaleExecution
		}
		task.Error = result.Error
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
		if blocked {
			task.Status = models.StatusBlocked
			blockedStatus = true
			task.FinishedAt = &now
		} else if allowRetry && task.RunCount <= task.RetryLimit {
			task.Status = models.StatusPending
			task.FinishedAt = nil
			retrying = true
		} else {
			task.Status = models.StatusFailed
			task.FinishedAt = &now
		}
		task.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return false, err
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
		},
		Timestamp: now,
	})
	return false, nil
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
		if status == models.StatusFailed {
			plan.Outcome = models.OutcomeFailed
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
		return
	}
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
