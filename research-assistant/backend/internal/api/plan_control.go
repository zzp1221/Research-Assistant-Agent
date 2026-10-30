package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/gin-gonic/gin"
)

var errPlanAlreadyRunning = errors.New("plan is already running")

// launchPlan starts a plan in the background and registers its cancel func.
func (r *planRuntime) launchPlan(planID, trigger, actor string) error {
	r.mu.Lock()
	if _, exists := r.running[planID]; exists {
		r.mu.Unlock()
		return errPlanAlreadyRunning
	}
	// The execution budget is enforced inside the scheduler so approval waits
	// can be excluded from it; the context only carries cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	r.running[planID] = cancel
	r.mu.Unlock()

	if trigger != "" {
		_ = r.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
			plan.RunTrigger = trigger
			return nil
		})
	}

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

	currentRevision := 0
	if current, err := r.store.GetPlan(planID); err == nil && current != nil {
		currentRevision = current.CurrentRevision
	}
	payload := map[string]any{"actor": actor}
	if trigger != "" {
		payload["trigger"] = trigger
	}
	_ = r.store.AppendEvent(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: "plan_execution_requested",
		Revision:  currentRevision,
		Payload:   payload,
		Timestamp: time.Now(),
	})
	return nil
}

func (r *planRuntime) pausePlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	actor := resolveUserID(c)
	if err := r.scheduler.PausePlan(plan.ID, actor); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "plan_not_pausable"})
		return
	}
	recordAuditEvent("plan_paused", actor, plan.ID, nil)
	c.JSON(http.StatusOK, gin.H{"plan_id": plan.ID, "paused": true})
}

func (r *planRuntime) resumePlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	actor := resolveUserID(c)
	if err := r.scheduler.ResumePlan(plan.ID, actor); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "plan_not_resumable"})
		return
	}
	recordAuditEvent("plan_resumed", actor, plan.ID, nil)
	c.JSON(http.StatusOK, gin.H{"plan_id": plan.ID, "paused": false})
}

// runFromNode rewinds the selected node and its descendants, then starts the
// plan again so the user does not have to rebuild the graph.
func (r *planRuntime) runFromNode(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	taskID := strings.TrimSpace(c.Param("taskId"))
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "task id is required", "code": "task_required"})
		return
	}
	if plan.Status == models.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is running; cancel or wait before rewinding", "code": "plan_running"})
		return
	}
	if err := r.scheduler.ResetFromNode(plan.ID, taskID); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "rewind_failed"})
		return
	}
	// Rewinding mutates the live graph; bind it to the latest immutable
	// revision before starting so the run keeps a verifiable snapshot.
	if _, err := r.bindLatestRevision(plan.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot bind plan revision", "code": "revision_bind_failed"})
		return
	}
	actor := resolveUserID(c)
	recordAuditEvent("plan_rewound", actor, plan.ID, map[string]any{"task_id": taskID})
	if err := r.launchPlan(plan.ID, "manual", actor); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "plan_already_running"})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"message": "Plan rewound and execution started",
		"plan_id": plan.ID,
		"task_id": taskID,
	})
}
