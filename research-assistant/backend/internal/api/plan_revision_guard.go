package api

import (
	"errors"
	"net/http"
	"strings"
	"sync"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/revision"

	"github.com/gin-gonic/gin"
)

// lockPlanRevisionMutation serialises revision create/review/execute for one
// plan. Each of those requests asserts "the revision I name is the latest one",
// so two of them arriving together must not both pass that assertion. The lock
// is per plan and held only around the mutation section, so unrelated plans and
// non-revision endpoints are unaffected.
//
// Lock order is always: planRevisionMutation -> plan store -> revision store,
// and r.mu is a leaf below all of them: it is only taken for a momentary
// running-map read/write and launchPlan releases r.mu before touching the plan
// store, so the order cannot invert. launchPlan may be called while the revision
// lock is held (executePlan, runFromNode and executeRevision all do), but it
// never acquires that lock itself.
func (r *planRuntime) revisionMutationLock(planID string) *sync.Mutex {
	lock, ok := r.revisionMutationLocks.Load(planID)
	if ok {
		return lock.(*sync.Mutex)
	}
	created, _ := r.revisionMutationLocks.LoadOrStore(planID, &sync.Mutex{})
	return created.(*sync.Mutex)
}

// lockPlanRevisionMutation takes the per-plan revision lock and returns the
// release function. Callers take it after the ownership check and re-read the
// plan afterwards, so the state they act on is the state the store holds.
func (r *planRuntime) lockPlanRevisionMutation(planID string) func() {
	lock := r.revisionMutationLock(planID)
	lock.Lock()
	return lock.Unlock
}

// planExecutionAccepted reports whether a launch for this plan has already been
// accepted by the runtime, i.e. a scheduler goroutine owns it. The running map
// is guarded by r.mu, so the check is a momentary read that never nests inside a
// plan store callback that could block on the same mutex.
func (r *planRuntime) planExecutionAccepted(planID string) bool {
	r.mu.Lock()
	_, running := r.running[planID]
	r.mu.Unlock()
	return running
}

// revisionGuardError is a revision mutation rejected by the authoritative state
// that store.UpdatePlan read, i.e. after the authorizedPlan snapshot was taken.
// Carrying the status and code keeps the client contract (plan_running /
// plan_paused / revision_not_latest / revision_conflict) intact without
// string-matching store errors.
type revisionGuardError struct {
	status  int
	code    string
	message string
	extra   map[string]any
}

func (e *revisionGuardError) Error() string { return e.message }

func newRevisionGuard(status int, code, message string) *revisionGuardError {
	return &revisionGuardError{status: status, code: code, message: message}
}

func (e *revisionGuardError) with(key string, value any) *revisionGuardError {
	if e.extra == nil {
		e.extra = map[string]any{}
	}
	e.extra[key] = value
	return e
}

// checkPlanRevisionEditable re-reads the authoritative plan inside
// store.UpdatePlan. The snapshot returned by authorizedPlan can already be stale:
// the scheduler or a concurrent request may have started, paused or resumed the
// plan between that ownership check and this mutation. A disabled button in the
// UI is not a concurrency control, so every revision mutation re-checks here.
func checkPlanRevisionEditable(current *models.PlanGraph, runningMessage string) error {
	if current == nil {
		return newRevisionGuard(http.StatusNotFound, "plan_not_found", "plan not found")
	}
	switch current.Status {
	case models.StatusInProgress, models.StatusAwaitingApproval, models.StatusAwaitingInput:
		return newRevisionGuard(http.StatusConflict, "plan_running", runningMessage)
	}
	if current.Paused {
		return newRevisionGuard(http.StatusConflict, "plan_paused", "plan is paused; resume it before running")
	}
	return nil
}

// isPlanNotFoundError recognises the store's "plan not found: <id>" error. The
// plan store interface has no sentinel for it, and a 404 is part of the
// documented revision contract.
func isPlanNotFoundError(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "plan not found")
}

// respondRevisionMutationError renders a revision mutation failure. Guard and
// conflict errors keep their stable code and payload so the review panel can
// explain the reason instead of showing a generic store error.
func respondRevisionMutationError(c *gin.Context, err error) {
	var guard *revisionGuardError
	if errors.As(err, &guard) {
		payload := gin.H{"error": guard.message, "code": guard.code}
		for key, value := range guard.extra {
			payload[key] = value
		}
		c.JSON(guard.status, payload)
		return
	}
	var conflict *revision.ConflictError
	if errors.As(err, &conflict) {
		c.JSON(http.StatusConflict, gin.H{
			"error":            "base revision is stale; reload and retry",
			"code":             "revision_conflict",
			"current_revision": conflict.CurrentRevision,
		})
		return
	}
	if errors.Is(err, revision.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "revision not found", "code": "revision_not_found"})
		return
	}
	if isPlanNotFoundError(err) {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error(), "code": "plan_not_found"})
		return
	}
	if errors.Is(err, errEventPersistence) {
		respondEventPersistFailure(c, err)
		return
	}
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "revision_store_unavailable"})
}

// respondEventPersistFailure reports a durable-state change whose event could
// not be written. It never returns 2xx: the audit/stream record is missing, so
// claiming success would leave clients with a state they cannot reconcile.
// Callers that already mutated the plan or revision add what did apply through
// respondRevisionMutationError's guard payload or an explicit message.
func respondEventPersistFailure(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{
		"error": "the change was applied but its event could not be persisted; refresh the plan before retrying: " + err.Error(),
		"code":  "event_persist_failed",
	})
}
