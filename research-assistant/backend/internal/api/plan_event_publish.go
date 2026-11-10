package api

import (
	"errors"
	"fmt"
	"log"

	"research-assistant-backend/internal/models"
)

// errEventPersistence marks a durable-state change whose event could not be
// written to the plan history.
var errEventPersistence = errors.New("plan event persistence failed")

// persistAndPublishPlanEvent appends the event to the durable history and
// publishes the stored copy, which carries the Seq assigned by the store.
//
// Publishing the pre-append copy would hand live SSE subscribers seq 0. Those
// events cannot advance a client's after_seq cursor, so a reconnect replays work
// the client already applied and the live and replayed streams disagree. A
// failed append is therefore never published, and the error is returned instead
// of being swallowed: nothing durable backs the event, so the caller must not
// report success on it.
func (r *planRuntime) persistAndPublishPlanEvent(planID string, event models.PlanEvent) error {
	stored, err := r.store.AppendEventReturning(planID, event)
	if err != nil {
		log.Printf("plan event persist failed plan_id=%s type=%s: %v", planID, event.EventType, err)
		return fmt.Errorf("%w: %v", errEventPersistence, err)
	}
	if r.events != nil {
		r.events.Publish(planID, stored)
	}
	return nil
}
