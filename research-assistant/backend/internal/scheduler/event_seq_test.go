package scheduler

import (
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

type capturePublisher struct{ events []models.PlanEvent }

func (p *capturePublisher) Publish(_ string, event models.PlanEvent) {
	p.events = append(p.events, event)
}

func TestLiveEventsCarryAssignedSeq(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("live-seq-plan", 1)
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	publisher := &capturePublisher{}
	runner := NewScheduler(planStore, NewDefaultTaskExecutor(), publisher, 1)
	runner.publishAndStore(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "plan_started"})
	runner.publishAndStore(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "plan_completed"})
	if len(publisher.events) != 2 {
		t.Fatalf("published %d events", len(publisher.events))
	}
	if publisher.events[0].Seq != 1 || publisher.events[1].Seq != 2 {
		t.Fatalf("live seq = %d,%d", publisher.events[0].Seq, publisher.events[1].Seq)
	}
	persisted, _ := planStore.ListEventsAfter(plan.ID, 0)
	if len(persisted) != 2 || persisted[1].Seq != publisher.events[1].Seq {
		t.Fatalf("persisted=%#v live=%#v", persisted, publisher.events)
	}
}
