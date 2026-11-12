package api

import (
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/scheduler"
)

// TestApproveReleasesOnlyProvenBlockedDependents pins the recovery rule shared
// by the API decision endpoints: approving one action releases the waiting task
// and only the nodes whose recorded causes are actually gone. A node blocked
// with no structured reason (legacy) stays blocked instead of being flown open.
func TestApproveReleasesOnlyProvenBlockedDependents(t *testing.T) {
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "recovery-plan",
		Nodes: []*models.TaskNode{
			{ID: "a", Status: models.StatusBlocked, BlockInfo: &models.BlockInfo{
				Code: models.BlockUpstreamBlocked, OccurredAt: now,
				Causes: []models.BlockCause{{Code: models.BlockUpstreamBlocked, RootTaskID: "r1", Status: models.StatusBlocked}},
			}},
			{ID: "b", Status: models.StatusBlocked},
			{ID: "r1", Status: models.StatusCompleted},
		},
	}
	result := scheduler.RecoverBlockedDependents(plan, now)
	if getAPINode(plan, "a").Status != models.StatusPending {
		t.Fatalf("proven cause was not released: %#v", getAPINode(plan, "a"))
	}
	if getAPINode(plan, "b").Status != models.StatusBlocked {
		t.Fatalf("legacy block was released without proof: %#v", getAPINode(plan, "b"))
	}
	if len(result.Unblocked) != 1 || result.Unblocked[0] != "a" {
		t.Fatalf("unblocked=%v", result.Unblocked)
	}
	if len(result.Events) != 1 || result.Events[0].EventType != "task_unblocked" {
		t.Fatalf("events=%#v", result.Events)
	}
}

func getAPINode(plan *models.PlanGraph, id string) *models.TaskNode {
	for _, node := range plan.Nodes {
		if node != nil && node.ID == id {
			return node
		}
	}
	return nil
}
