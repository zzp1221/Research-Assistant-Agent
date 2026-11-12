package scheduler

import (
	"time"

	"research-assistant-backend/internal/models"
)

// BlockedRecoveryResult is what a blocked-cause refresh changed.
type BlockedRecoveryResult struct {
	// Events are the task_unblocked / task_block_reason_updated events to publish.
	Events []models.PlanEvent
	// Unblocked lists the task ids that returned to pending.
	Unblocked []string
	// StillBlocked lists the task ids that kept at least one effective cause.
	StillBlocked []string
}

// RecoverBlockedDependents re-evaluates the recorded block causes of a plan and
// releases the nodes whose terminal causes are gone. It is exported so the API
// decision endpoints (approval / question answer / retry) apply exactly the same
// rule as the scheduler instead of blanket-resetting every blocked node.
//
// Nodes blocked before block_info existed keep their state: without recorded
// evidence the source cannot be proven, so only an explicit rerun may release
// them.
func RecoverBlockedDependents(plan *models.PlanGraph, now time.Time) BlockedRecoveryResult {
	result := BlockedRecoveryResult{}
	if plan == nil {
		return result
	}
	before := map[string]models.TaskStatus{}
	for _, node := range plan.Nodes {
		if node != nil {
			before[node.ID] = node.Status
		}
	}
	result.Events = refreshBlockedNodes(plan, now)
	for _, node := range plan.Nodes {
		if node == nil || node.Status != models.StatusBlocked {
			continue
		}
		if before[node.ID] == models.StatusBlocked {
			result.StillBlocked = append(result.StillBlocked, node.ID)
		}
	}
	for _, event := range result.Events {
		if event.EventType == "task_unblocked" {
			result.Unblocked = append(result.Unblocked, event.TaskID)
		}
	}
	return result
}
