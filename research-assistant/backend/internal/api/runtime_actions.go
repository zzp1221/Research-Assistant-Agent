package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/gin-gonic/gin"
)

type runtimeDecisionPayload struct {
	Decision string `json:"decision" binding:"required"`
	Note     string `json:"note,omitempty"`
}

func (r *planRuntime) getRuntimeActions(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"actions": plan.RuntimeActions})
}

func (r *planRuntime) decideRuntimeAction(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	var payload runtimeDecisionPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if payload.Decision != "approve" && payload.Decision != "reject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decision must be approve or reject"})
		return
	}
	actionID := c.Param("actionId")
	// Replay approvals are recorded against a finished plan's audit trail; they
	// never resurrect the plan. Any other action on a terminal plan is stale.
	replayDecision := false
	for i := range plan.RuntimeActions {
		action := plan.RuntimeActions[i]
		if action.ID != actionID {
			continue
		}
		if _, ok := action.Details["replay_of"].(string); ok && action.Kind == models.RuntimeActionToolCall {
			replayDecision = true
		}
		break
	}
	if isTerminalPlanStatus(plan.Status) && !replayDecision {
		c.JSON(http.StatusConflict, gin.H{
			"error": "plan has already ended (" + string(plan.Status) + "); create a new plan or rerun it",
			"code":  "plan_already_terminal",
		})
		return
	}
	userID := resolveUserID(c)
	now := time.Now()
	r.mu.Lock()
	_, alreadyRunning := r.running[plan.ID]
	r.mu.Unlock()
	var decided *models.RuntimeActionRequest
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		if isTerminalPlanStatus(current.Status) && !replayDecision {
			return fmt.Errorf("plan has already ended (%s); create a new plan or rerun it", current.Status)
		}
		index := -1
		for i := range current.RuntimeActions {
			if current.RuntimeActions[i].ID == actionID {
				index = i
				break
			}
		}
		if index < 0 {
			return errors.New("runtime action not found")
		}
		action := &current.RuntimeActions[index]
		if action.Status != models.RuntimeActionPending {
			return fmt.Errorf("runtime action is not pending (status=%s)", action.Status)
		}
		// Expired actions cannot be approved or rejected.
		if !action.RequestedAt.IsZero() && now.Sub(action.RequestedAt) >= time.Duration(approvalTimeoutSecondsForAPI())*time.Second {
			action.Status = models.RuntimeActionExpired
			action.DecidedAt = &now
			action.DecidedBy = "system:approval_timeout"
			action.DecisionNote = "approval timed out"
			if task := findPlanTask(current, action.TaskID); task != nil {
				task.Status = models.StatusBlocked
				task.Error = "approval timed out"
				task.FinishedAt = &now
				task.UpdatedAt = now
			}
			current.Status = models.StatusFailed
			current.Outcome = models.OutcomeBlocked
			current.OutcomeReason = "approval timed out"
			current.Usage.FinishedAt = &now
			current.MissingRequirements = append(current.MissingRequirements, models.MissingRequirement{
				Kind: "approval_timeout", Name: string(action.Kind),
				Detail: "审批窗口已过期", Remediation: "重新创建计划并在 APPROVAL_TIMEOUT_SECONDS 内确认。",
			})
			decided = action
			return errApprovalExpired
		}
		task := findPlanTask(current, action.TaskID)
		if task == nil && !replayDecision {
			return errors.New("runtime action task not found")
		}
		action.DecidedAt = &now
		action.DecidedBy = userID
		action.DecisionNote = payload.Note
		if payload.Decision == "approve" {
			action.Status = models.RuntimeActionApproved
			if action.Kind == models.RuntimeActionToolCall {
				markToolCallApproved(current, action, now)
			}
			if replayDecision {
				current.UpdatedAt = now
				decided = action
				return nil
			}
			task.Status = models.StatusPending
			task.Error = ""
			task.FinishedAt = nil
			task.UpdatedAt = now
			if alreadyRunning {
				current.Status = models.StatusInProgress
			} else {
				current.Status = models.StatusPending
			}
			current.Outcome = ""
			current.OutcomeReason = ""
			current.UpdatedAt = now
		} else {
			action.Status = models.RuntimeActionRejected
			if action.Kind == models.RuntimeActionToolCall {
				markToolCallRejected(current, action, now)
			}
			if replayDecision {
				current.UpdatedAt = now
				decided = action
				return nil
			}
			task.Status = models.StatusBlocked
			task.Error = "user rejected runtime action: " + string(action.Kind)
			task.FinishedAt = &now
			task.UpdatedAt = now
			current.Status = models.StatusFailed
			current.Outcome = models.OutcomeBlocked
			current.OutcomeReason = task.Error
			current.Usage.FinishedAt = &now
			current.UpdatedAt = now
			current.MissingRequirements = append(current.MissingRequirements, models.MissingRequirement{
				Kind: "user_rejected", Name: string(action.Kind), Detail: task.Error, Remediation: "重新运行并允许该操作。",
			})
			for _, node := range current.Nodes {
				if node == nil || node.ID == task.ID {
					continue
				}
				switch node.Status {
				case models.StatusPending, models.StatusReady, models.StatusInProgress, models.StatusAwaitingApproval:
					node.Status = models.StatusCanceled
					node.Error = "plan aborted: user rejected " + string(action.Kind)
					node.ExecutionID = ""
					node.LeaseOwner = ""
					node.LeaseExpiresAt = nil
					node.FinishedAt = &now
					node.UpdatedAt = now
				}
			}
		}
		decided = action
		return nil
	}); err != nil {
		if errors.Is(err, errApprovalExpired) {
			c.JSON(http.StatusConflict, gin.H{
				"error": "approval window has expired; create a new plan or rerun it",
				"code":  "approval_expired",
			})
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		TaskID:    decided.TaskID,
		EventType: "runtime_action_resolved",
		Payload: map[string]any{
			"action":     decided,
			"decision":   payload.Decision,
			"decided_by": userID,
			"decided_at": now,
		},
		Timestamp: now,
	}
	_ = r.store.AppendEvent(plan.ID, event)
	r.events.Publish(plan.ID, event)

	if replayDecision {
		// Replay decisions only update the audit record; the plan is finished.
	} else if payload.Decision == "approve" {
		if !alreadyRunning && !r.startPlanExecution(plan.ID, plan) {
			c.JSON(http.StatusConflict, gin.H{"error": "plan is already running"})
			return
		}
	} else {
		r.mu.Lock()
		cancel := r.running[plan.ID]
		r.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		r.scheduler.CleanupPlanSandboxes(context.Background(), plan.ID)
	}
	planStatus := plan.Status
	if updated, getErr := r.store.GetPlan(plan.ID); getErr == nil && updated != nil {
		planStatus = updated.Status
	}
	c.JSON(http.StatusOK, gin.H{
		"action":      decided,
		"plan_status": planStatus,
	})
}

// errApprovalExpired marks a decision on an action whose approval window ended.
var errApprovalExpired = errors.New("approval window has expired")

func isTerminalPlanStatus(status models.TaskStatus) bool {
	switch status {
	case models.StatusCompleted, models.StatusFailed, models.StatusCanceled:
		return true
	default:
		return false
	}
}

func approvalTimeoutSecondsForAPI() int {
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

func (r *planRuntime) startPlanExecution(planID string, plan *models.PlanGraph) bool {
	r.mu.Lock()
	if _, exists := r.running[planID]; exists {
		r.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.running[planID] = cancel
	r.mu.Unlock()

	go func(runCtx context.Context, runCancel context.CancelFunc) {
		defer func() {
			runCancel()
			r.mu.Lock()
			delete(r.running, planID)
			r.mu.Unlock()
		}()
		if err := r.scheduler.ExecutePlan(runCtx, planID); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("plan execution failed plan_id=%s: %v", planID, err)
		}
	}(ctx, cancel)
	return true
}

// markToolCallApproved records the human decision on the pending tool call.
func markToolCallApproved(plan *models.PlanGraph, action *models.RuntimeActionRequest, now time.Time) {
	if _, replay := action.Details["replay_of"].(string); replay {
		return
	}
	callID, _ := action.Details["call_id"].(string)
	for i := range plan.ToolCalls {
		if plan.ToolCalls[i].ID == callID {
			plan.ToolCalls[i].Status = models.ToolCallApproved
			plan.ToolCalls[i].ApprovalID = action.ID
			return
		}
	}
}

// markToolCallRejected closes the pending record so the audit trail never
// leaves a rejected call looking like it is still waiting.
func markToolCallRejected(plan *models.PlanGraph, action *models.RuntimeActionRequest, now time.Time) {
	if _, replay := action.Details["replay_of"].(string); replay {
		return
	}
	callID, _ := action.Details["call_id"].(string)
	if callID == "" {
		return
	}
	for i := range plan.ToolCalls {
		if plan.ToolCalls[i].ID == callID {
			plan.ToolCalls[i].Status = models.ToolCallRejected
			plan.ToolCalls[i].ErrorCode = "tool_call_rejected"
			plan.ToolCalls[i].Error = "user rejected tool call"
			plan.ToolCalls[i].ApprovalID = action.ID
			plan.ToolCalls[i].FinishedAt = &now
			return
		}
	}
	toolID, _ := action.Details["tool_id"].(string)
	plan.ToolCalls = append(plan.ToolCalls, models.ToolCallRecord{
		ID:         callID,
		PlanID:     plan.ID,
		TaskID:     action.TaskID,
		ToolID:     toolID,
		Status:     models.ToolCallRejected,
		ErrorCode:  "tool_call_rejected",
		Error:      "user rejected tool call",
		ApprovalID: action.ID,
		FinishedAt: &now,
		StartedAt:  now,
	})
}
