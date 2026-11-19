package api

import (
	"errors"
	"log"
	"net/http"

	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
)

// respondPlanStoreError is for direct store reads/deletes. Callback validation
// errors retain their existing domain-specific response; an unavailable store
// is never evidence that a plan does not exist.
func respondPlanStoreError(c *gin.Context, err error) {
	c.Header("Content-Type", "application/json; charset=utf-8")
	if errors.Is(err, store.ErrPlanNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found", "code": "plan_not_found"})
		return
	}
	code := "store_unavailable"
	if errors.Is(err, store.ErrCommitUnknown) {
		code = "store_commit_unknown"
	} else if errors.Is(err, store.ErrStoreRecoveryRequired) {
		code = "store_recovery_required"
	}
	log.Printf("plan store request failed code=%s: %v", code, err)
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plan store is unavailable; retry after recovery", "code": code})
}

// respondSealedPlanStoreError handles typed storage failures on mutation paths
// that otherwise distinguish callback validation errors themselves.
func isPlanStoreFailure(err error) bool {
	return errors.Is(err, store.ErrNotCommitted) || errors.Is(err, store.ErrCommitUnknown) || errors.Is(err, store.ErrStoreRecoveryRequired)
}

func (r *planRuntime) respondSealedPlanStoreError(c *gin.Context, err error) bool {
	if isPlanStoreFailure(err) {
		respondPlanStoreError(c, err)
		return true
	}
	if health, ok := r.store.(interface{ Health() error }); ok {
		if healthErr := health.Health(); healthErr != nil {
			respondPlanStoreError(c, healthErr)
			return true
		}
	}
	return false
}

func (r *planRuntime) requireHealthyPlanStore(c *gin.Context) {
	if r.respondSealedPlanStoreError(c, nil) {
		c.Abort()
		return
	}
	c.Next()
}

// A plan mutation and its event are separate commits. Keep the plan ID in the
// error response so a client can inspect the applied change without creating a
// replacement plan after its event append failed.
func appliedPlanEventFailure(planID string, err error) idempotentResponse {
	code := "event_persist_failed"
	if errors.Is(err, store.ErrCommitUnknown) {
		code = "store_commit_unknown"
	} else if errors.Is(err, store.ErrStoreRecoveryRequired) {
		code = "store_recovery_required"
	} else if errors.Is(err, store.ErrNotCommitted) {
		code = "store_unavailable"
	}
	return idempotencyError(http.StatusServiceUnavailable, code, "Plan change was saved but its event could not be confirmed; inspect this plan after storage recovery before retrying", planID)
}

func respondAppliedPlanEventFailure(c *gin.Context, planID string, err error) {
	writeIdempotentResponse(c, appliedPlanEventFailure(planID, err), false)
}
