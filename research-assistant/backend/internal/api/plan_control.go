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

// respondPlanLaunchError renders a launch failure for the execute/run/execute
// revision endpoints. An accepted launch is reported as a conflict; a durable
// event that could not be written is reported as an event persistence failure,
// because the caller must not be told the run started when its stream record is
// missing.
func respondPlanLaunchError(c *gin.Context, err error) {
	if errors.Is(err, errPlanAlreadyRunning) {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is already running", "code": "plan_already_running", "remediation": errorRemediation("plan_already_running")})
		return
	}
	if errors.Is(err, errEventPersistence) {
		respondEventPersistFailure(c, err)
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": "plan could not be started: " + err.Error(), "code": "plan_launch_failed"})
}

// launchPlan starts a plan in the background and registers its cancel func.
//
// The run is not accepted until its numbered plan_execution_requested event is
// durable: a client that got a 2xx must be able to find the run in the event
// history. Every failure path therefore reverses the reservation it made, so a
// rejected launch leaves no cancel func behind that would make the plan look
// running forever.
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

	// cleanupFailure cancels and deletes the reservation taken above. It runs
	// under r.mu, which is a leaf lock: no store call is ever made while it is
	// held.
	cleanupFailure := func() {
		cancel()
		r.mu.Lock()
		delete(r.running, planID)
		r.mu.Unlock()
	}

	if trigger != "" {
		if err := r.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
			plan.RunTrigger = trigger
			return nil
		}); err != nil {
			cleanupFailure()
			return err
		}
	}

	// The revision is read before the event is written so the event names the
	// snapshot the run is about to execute.
	currentRevision := 0
	if current, err := r.store.GetPlan(planID); err == nil && current != nil {
		currentRevision = current.CurrentRevision
	}
	payload := map[string]any{"actor": actor}
	if trigger != "" {
		payload["trigger"] = trigger
	}
	// Persist and publish before the scheduler goroutine exists: the numbered
	// event is part of accepting the run, not a notification sent after it.
	if err := r.persistAndPublishPlanEvent(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: "plan_execution_requested",
		Revision:  currentRevision,
		Payload:   payload,
		Timestamp: time.Now(),
	}); err != nil {
		cleanupFailure()
		return err
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
	// The rewind mutates the live graph and then launches, so it is serialised
	// with the other revision mutations of this plan: two rewinds (or a rewind
	// racing an execute) must not both pass the state guards below.
	unlock := r.lockPlanRevisionMutation(plan.ID)
	defer unlock()
	// The ownership check above only decided access; the state that gets rewound
	// is re-read under the lock.
	if fresh, err := r.store.GetPlan(plan.ID); err != nil || fresh == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found", "code": "plan_not_found"})
		return
	} else {
		plan = fresh
	}
	if plan.Status == models.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is running; cancel or wait before rewinding", "code": "plan_running"})
		return
	}
	// A paused plan, an approval wait or a question wait all mean the runtime
	// still owns the plan or expects the user to answer first; resetting nodes
	// underneath that state would strand the pending work.
	if plan.Paused {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is paused; resume it before rewinding", "code": "plan_paused", "remediation": errorRemediation("plan_paused")})
		return
	}
	if plan.Status == models.StatusAwaitingApproval || plan.Status == models.StatusAwaitingInput {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is waiting; cancel or finish before rewinding", "code": "plan_running"})
		return
	}
	// An accepted launch that the store has not reflected yet must not have its
	// graph reset underneath the running scheduler goroutine.
	if r.planExecutionAccepted(plan.ID) {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is already running; cancel or wait before rewinding", "code": "plan_running"})
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
		respondPlanLaunchError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, gin.H{
		"message": "Plan rewound and execution started",
		"plan_id": plan.ID,
		"task_id": taskID,
	})
}
