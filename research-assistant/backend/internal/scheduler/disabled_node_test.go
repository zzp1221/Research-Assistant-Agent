package scheduler

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestPromotePendingNodesSkipsDisabledNode(t *testing.T) {
	disabled := false
	plan := &models.PlanGraph{
		ID: "plan-skip",
		Nodes: []*models.TaskNode{
			{ID: "a", Name: "skip me", Status: models.StatusPending, Enabled: &disabled},
			{ID: "b", Name: "downstream", Status: models.StatusPending, Dependencies: []string{"a"}},
		},
	}
	scheduler := &Scheduler{}
	events := scheduler.promotePendingNodesToReady(plan)
	if plan.Nodes[0].Status != models.StatusSkipped {
		t.Fatalf("disabled node status=%s, want skipped", plan.Nodes[0].Status)
	}
	if plan.Nodes[1].Status != models.StatusReady {
		t.Fatalf("downstream status=%s, want ready", plan.Nodes[1].Status)
	}
	if len(events) != 2 {
		t.Fatalf("events=%d, want 2: %+v", len(events), events)
	}
	if events[0].EventType != "task_skipped" {
		t.Fatalf("first event=%s, want task_skipped", events[0].EventType)
	}
}

func TestFindReadyNodesExcludesDisabled(t *testing.T) {
	disabled := false
	plan := &models.PlanGraph{
		ID: "plan-skip-ready",
		Nodes: []*models.TaskNode{
			{ID: "a", Status: models.StatusReady, Enabled: &disabled},
			{ID: "b", Status: models.StatusReady},
		},
	}
	scheduler := &Scheduler{}
	ready := scheduler.findReadyNodes(plan, 4)
	if len(ready) != 1 || ready[0].ID != "b" {
		t.Fatalf("ready=%v, want only b", ready)
	}
}
