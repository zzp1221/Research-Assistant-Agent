package api

import (
	"context"
	"errors"
	"log"
	"net/http"
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
	userID := resolveUserID(c)
	now := time.Now()
	r.mu.Lock()
	_, alreadyRunning := r.running[plan.ID]
	r.mu.Unlock()
	var decided *models.RuntimeActionRequest
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
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
			return errors.New("runtime action is not pending")
		}
		task := findPlanTask(current, action.TaskID)
		if task == nil {
			return errors.New("runtime action task not found")
		}
		action.DecidedAt = &now
		action.DecidedBy = userID
		action.DecisionNote = payload.Note
		if payload.Decision == "approve" {
			action.Status = models.RuntimeActionApproved
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
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		TaskID:    decided.TaskID,
		EventType: "runtime_action_resolved",
		Payload: map[string]any{
			"action":   decided,
			"decision": payload.Decision,
		},
		Timestamp: now,
	}
	_ = r.store.AppendEvent(plan.ID, event)
	r.events.Publish(plan.ID, event)

	if payload.Decision == "approve" {
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

func (r *planRuntime) startPlanExecution(planID string, plan *models.PlanGraph) bool {
	r.mu.Lock()
	if _, exists := r.running[planID]; exists {
		r.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), planTimeout(plan))
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
