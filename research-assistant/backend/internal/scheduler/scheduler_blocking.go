package scheduler

import (
	"fmt"
	"log"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

// blockCodeForRoot maps the real stop status of a node onto the stable reason
// code its hard dependents record.
func blockCodeForRoot(status models.TaskStatus) string {
	switch status {
	case models.StatusBlocked:
		return models.BlockUpstreamBlocked
	case models.StatusCanceled:
		return models.BlockUpstreamCanceled
	case models.StatusFailed:
		return models.BlockUpstreamFailed
	default:
		return ""
	}
}

func blockCodeLabel(code string) string {
	switch code {
	case models.BlockUpstreamFailed:
		return "上游执行失败"
	case models.BlockUpstreamBlocked:
		return "上游缺少可补条件"
	case models.BlockUpstreamCanceled:
		return "上游已取消"
	case models.BlockUnsatisfiedDependencies:
		return "硬依赖无法满足"
	case models.BlockMissingArtifacts:
		return "必需产物缺失"
	default:
		return code
	}
}

// shortBlockMessage keeps Task.Error honest and short; the detailed diagnostic
// stays on the root node and in MissingRequirements.
func shortBlockMessage(root *models.TaskNode, code string) string {
	name := ""
	if root != nil {
		name = strings.TrimSpace(root.Name)
		if name == "" {
			name = root.ID
		}
	}
	return "因任务 " + name + " " + blockCodeLabel(code) + "而阻断"
}

func truncateMessage(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit]) + "…"
}

// recordBlockCause merges one effective blocking cause into a node. Every valid
// cause is kept: retrying one root must not release a node still blocked by
// another.
func recordBlockCause(task *models.TaskNode, cause models.BlockCause, now time.Time) {
	if task == nil {
		return
	}
	if task.BlockInfo == nil {
		task.BlockInfo = &models.BlockInfo{Code: cause.Code, OccurredAt: now}
	}
	info := task.BlockInfo
	if strings.TrimSpace(info.Message) == "" {
		info.Message = cause.Message
	}
	// Keep the highest-priority code: a real failure is more specific than a
	// propagated block, and both are more specific than a cancellation.
	if blockCodePriority(cause.Code) < blockCodePriority(info.Code) {
		info.Code = cause.Code
	}
	for i := range info.Causes {
		existing := info.Causes[i]
		if existing.RootTaskID == cause.RootTaskID && existing.Revision == cause.Revision && existing.ExecutionEpoch == cause.ExecutionEpoch {
			info.Causes[i] = cause
			return
		}
	}
	info.Causes = append(info.Causes, cause)
}

func blockCodePriority(code string) int {
	switch code {
	case models.BlockUpstreamFailed:
		return 0
	case models.BlockUpstreamBlocked:
		return 1
	case models.BlockUpstreamCanceled:
		return 2
	case models.BlockMissingArtifacts:
		return 3
	case models.BlockUnsatisfiedDependencies:
		return 4
	default:
		return 5
	}
}

// blockCauseFor builds the cause record for one root->dependent hop.
func blockCauseFor(root *models.TaskNode, viaTaskID string, status models.TaskStatus, code string) models.BlockCause {
	cause := models.BlockCause{
		Code:      code,
		ViaTaskID: viaTaskID,
		Status:    status,
		Message:   truncateMessage(root.Error, 200),
	}
	if root != nil {
		cause.RootTaskID = root.ID
		cause.Revision = root.Revision
		cause.ExecutionEpoch = root.ExecutionEpoch
		if cause.Message == "" {
			cause.Message = shortBlockMessage(root, code)
		}
	}
	return cause
}

// propagateBlockToDependents blocks every hard dependent of root inside the
// caller's state transition. It never overwrites completed, running or already
// waiting nodes, and it never propagates from a waiting upstream: waits keep
// dependents pending.
func propagateBlockToDependents(plan *models.PlanGraph, root *models.TaskNode, now time.Time) []models.PlanEvent {
	events := []models.PlanEvent{}
	if plan == nil || root == nil {
		return events
	}
	code := blockCodeForRoot(root.Status)
	if code == "" {
		return events
	}
	visited := map[string]bool{root.ID: true}
	type hop struct {
		taskID string
		viaID  string
	}
	queue := []hop{{taskID: root.ID}}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dependent := range hardDependents(plan, current.taskID) {
			if dependent == nil || visited[dependent.ID] {
				continue
			}
			visited[dependent.ID] = true
			switch dependent.Status {
			case models.StatusPending, models.StatusReady:
				cause := blockCauseFor(root, current.taskID, root.Status, code)
				recordBlockCause(dependent, cause, now)
				dependent.Status = models.StatusBlocked
				dependent.Error = shortBlockMessage(root, code)
				dependent.FinishedAt = &now
				dependent.UpdatedAt = now
				dependent.ExecutionID = ""
				dependent.LeaseOwner = ""
				dependent.LeaseExpiresAt = nil
				events = append(events, models.PlanEvent{
					PlanID:     plan.ID,
					EventType:  "task_blocked",
					TaskID:     dependent.ID,
					TaskStatus: string(models.StatusBlocked),
					Payload: map[string]any{
						"reason":           code,
						"upstream_task_id": current.taskID,
						"root_task_id":     root.ID,
						"block_info":       dependent.BlockInfo,
					},
					Timestamp: now,
				})
				queue = append(queue, hop{taskID: dependent.ID, viaID: current.taskID})
			case models.StatusBlocked:
				// A second root adds an independent cause; the node stays blocked
				// until every cause is gone.
				cause := blockCauseFor(root, current.taskID, root.Status, code)
				recordBlockCause(dependent, cause, now)
				dependent.UpdatedAt = now
				events = append(events, models.PlanEvent{
					PlanID:     plan.ID,
					EventType:  "task_blocked",
					TaskID:     dependent.ID,
					TaskStatus: string(models.StatusBlocked),
					Payload: map[string]any{
						"reason":           dependent.BlockInfo.Code,
						"upstream_task_id": current.taskID,
						"root_task_id":     root.ID,
						"block_info":       dependent.BlockInfo,
					},
					Timestamp: now,
				})
				queue = append(queue, hop{taskID: dependent.ID, viaID: current.taskID})
			case models.StatusInProgress, models.StatusAwaitingApproval, models.StatusAwaitingInput:
				// A node that is already running or legitimately waiting must not
				// be overwritten by an ordinary block traversal. This is a
				// consistency error worth surfacing, not a silent state change.
				log.Printf("scheduler: consistency error: %s is %s while its hard dependency %s stopped with %s", dependent.ID, dependent.Status, root.ID, root.Status)
				queue = append(queue, hop{taskID: dependent.ID, viaID: current.taskID})
			default:
				// completed/skipped/failed/canceled: the node already ended with
				// its own real status and must not be rewritten.
				queue = append(queue, hop{taskID: dependent.ID, viaID: current.taskID})
			}
		}
	}
	if len(events) > 0 {
		plan.UpdatedAt = now
	}
	return events
}

// blockCauseStillEffective reports whether a recorded cause still applies. A
// root that was retried (new revision/epoch) or no longer holds the recorded
// stop status no longer blocks anything.
func blockCauseStillEffective(plan *models.PlanGraph, cause models.BlockCause) bool {
	root := getNodeByID(plan, cause.RootTaskID)
	if root == nil {
		return false
	}
	if blockCodeForRoot(root.Status) == "" {
		return false
	}
	if cause.Revision != 0 && root.Revision != 0 && cause.Revision != root.Revision {
		return false
	}
	if cause.ExecutionEpoch != 0 && root.ExecutionEpoch != 0 && cause.ExecutionEpoch != root.ExecutionEpoch {
		return false
	}
	return true
}

// refreshBlockedNodes recomputes the effective blocking causes of every blocked
// node after a retry/reset and releases the nodes whose causes are gone. It runs
// inside the caller's state transition.
func refreshBlockedNodes(plan *models.PlanGraph, now time.Time) []models.PlanEvent {
	events := []models.PlanEvent{}
	if plan == nil {
		return events
	}
	for _, node := range plan.Nodes {
		if node == nil || node.Status != models.StatusBlocked || node.BlockInfo == nil {
			continue
		}
		kept := make([]models.BlockCause, 0, len(node.BlockInfo.Causes))
		for _, cause := range node.BlockInfo.Causes {
			if blockCauseStillEffective(plan, cause) {
				kept = append(kept, cause)
			}
		}
		if len(kept) == len(node.BlockInfo.Causes) {
			continue
		}
		if len(kept) > 0 {
			node.BlockInfo.Causes = kept
			node.BlockInfo.Code = kept[0].Code
			node.UpdatedAt = now
			events = append(events, models.PlanEvent{
				PlanID:     plan.ID,
				EventType:  "task_block_reason_updated",
				TaskID:     node.ID,
				TaskStatus: string(models.StatusBlocked),
				Payload:    map[string]any{"block_info": node.BlockInfo},
				Timestamp:  now,
			})
			continue
		}
		// Every terminal cause is gone: the node returns to pending and the
		// normal dependency/artifact predicate decides whether it is ready.
		node.BlockInfo = nil
		node.Error = ""
		node.FinishedAt = nil
		node.Status = models.StatusPending
		node.UpdatedAt = now
		events = append(events, models.PlanEvent{
			PlanID:     plan.ID,
			EventType:  "task_unblocked",
			TaskID:     node.ID,
			TaskStatus: string(models.StatusPending),
			Payload:    map[string]any{"reason": "blocking_causes_cleared"},
			Timestamp:  now,
		})
	}
	if len(events) > 0 {
		plan.UpdatedAt = now
	}
	return events
}

// invalidateTaskArtifacts removes the artifacts a node produced. It runs when the
// node is rewound or retried: a completed retry must re-produce its outputs to be
// trusted again, and a diagnostic left by the failed attempt must not survive as
// if the new attempt had produced it.
func invalidateTaskArtifacts(plan *models.PlanGraph, taskID string) []string {
	if plan == nil || strings.TrimSpace(taskID) == "" {
		return nil
	}
	removed := []string{}
	for key, artifact := range plan.Artifacts {
		if artifact.ProducerTaskID == taskID {
			delete(plan.Artifacts, key)
			removed = append(removed, key)
		}
	}
	return removed
}

// resetTaskForRetry clears every per-run field of a node, including the block
// reason and the finished boundary, so a rerun cannot inherit stale state.
func resetTaskForRetry(task *models.TaskNode, now time.Time) {
	task.Status = models.StatusPending
	task.Result = ""
	task.Error = ""
	task.BlockInfo = nil
	task.ExecutionID = ""
	task.LeaseOwner = ""
	task.LeaseExpiresAt = nil
	task.RunCount = 0
	task.StartedAt = nil
	task.FinishedAt = nil
	task.UpdatedAt = now
}

// resetBlockedDependents re-evaluates the dependents of a node that is being
// retried. It never blanket-releases the subtree: a dependent keeps every cause
// whose root is still stopped, and a node blocked before structured reasons
// existed stays blocked because its source cannot be proven.
func resetBlockedDependents(plan *models.PlanGraph, taskID string, now time.Time) []models.PlanEvent {
	if plan == nil {
		return nil
	}
	return refreshBlockedNodes(plan, now)
}

// blockInfoSummary renders a stable, bounded machine-readable description of
// every effective cause, used by terminal evaluation and diagnostics.
func blockInfoSummary(task *models.TaskNode) string {
	if task == nil || task.BlockInfo == nil {
		return ""
	}
	parts := make([]string, 0, len(task.BlockInfo.Causes))
	for _, cause := range task.BlockInfo.Causes {
		parts = append(parts, fmt.Sprintf("%s:%s@%s", cause.Code, cause.RootTaskID, cause.ViaTaskID))
	}
	if len(parts) == 0 {
		return string(task.BlockInfo.Code)
	}
	return strings.Join(parts, ",")
}
