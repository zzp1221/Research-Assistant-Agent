package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

// TestPlanLaunchAppendFailureReleasesReservation guards the launch contract: a
// run whose numbered plan_execution_requested event cannot be persisted must be
// reported as a persistence failure, and the reservation it took must be undone
// so the plan is not left looking running forever.
func TestPlanLaunchAppendFailureReleasesReservation(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionedPlan(t, f, "owner-a", "plan-launch-appendfail")

	f.useStore(t, &failingEventStore{PlanStore: f.planBase})

	response := revisionRequest(f.router, http.MethodPost, "/api/plans/"+plan.ID+"/execute", "owner-a", map[string]any{})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on launch event persist failure, got %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if payload.Code != "event_persist_failed" {
		t.Fatalf("code=%q, want event_persist_failed (body=%s)", payload.Code, response.Body.String())
	}

	f.runtime.mu.Lock()
	_, reserved := f.runtime.running[plan.ID]
	f.runtime.mu.Unlock()
	if reserved {
		t.Fatalf("rejected launch left a cancel func registered for %s: %v", plan.ID, reserved)
	}

	history, err := f.planBase.ListEvents(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range history {
		if event.EventType == "plan_execution_requested" || event.EventType == "plan_started" {
			t.Fatalf("rejected launch persisted %q: %+v", event.EventType, history)
		}
	}

	stored, err := f.planBase.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Usage.StartedAt != nil {
		t.Fatalf("rejected launch reached the scheduler: Usage.StartedAt=%v", stored.Usage.StartedAt)
	}
}

// TestPlanLaunchRequestedPrecedesStarted pins the ordering the event stream
// relies on: the run is accepted by writing plan_execution_requested, so the
// scheduler's plan_started must always carry a later seq. A client that sees
// plan_started can therefore always find its request in the history.
func TestPlanLaunchRequestedPrecedesStarted(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionedPlan(t, f, "owner-a", "plan-launch-order")

	t.Cleanup(func() {
		f.runtime.mu.Lock()
		cancel := f.runtime.running[plan.ID]
		f.runtime.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	})

	if err := f.runtime.launchPlan(plan.ID, "manual", "owner-a"); err != nil {
		t.Fatalf("launch: %v", err)
	}

	// The scheduler runs in a goroutine and may fail fast; only the ordering of
	// the two events is under test, so wait for plan_started with a bound.
	deadline := time.Now().Add(2 * time.Second)
	var requested, started *models.PlanEvent
	for time.Now().Before(deadline) {
		history, err := f.planBase.ListEvents(plan.ID)
		if err != nil {
			t.Fatal(err)
		}
		requested, started = nil, nil
		for i := range history {
			switch history[i].EventType {
			case "plan_execution_requested":
				requested = &history[i]
			case "plan_started":
				started = &history[i]
			}
		}
		if started != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if requested == nil {
		t.Fatal("launch never persisted plan_execution_requested")
	}
	if started == nil {
		t.Fatal("scheduler never persisted plan_started within 2s")
	}
	if requested.Seq <= 0 {
		t.Fatalf("plan_execution_requested has no seq: %+v", *requested)
	}
	if started.Seq <= 0 {
		t.Fatalf("plan_started has no seq: %+v", *started)
	}
	if requested.Seq >= started.Seq {
		t.Fatalf("plan_execution_requested seq=%d must precede plan_started seq=%d", requested.Seq, started.Seq)
	}
}
