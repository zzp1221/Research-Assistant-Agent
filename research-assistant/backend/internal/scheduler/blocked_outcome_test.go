package scheduler

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestHasBlockedOnlyDistinguishesBlockedFromFailed(t *testing.T) {
	blocked := &models.PlanGraph{Nodes: []*models.TaskNode{
		{ID: "a", Status: models.StatusCompleted},
		{ID: "b", Status: models.StatusBlocked},
	}}
	if !hasBlockedOnly(blocked) {
		t.Fatal("blocked-only plan must be detected")
	}
	failed := &models.PlanGraph{Nodes: []*models.TaskNode{
		{ID: "a", Status: models.StatusBlocked},
		{ID: "b", Status: models.StatusFailed},
	}}
	if hasBlockedOnly(failed) {
		t.Fatal("a real failure must not be reported as blocked")
	}
}

func TestAppendMissingRequirementDeduplicates(t *testing.T) {
	list := appendMissingRequirement(nil, models.MissingRequirement{Kind: "dataset", Name: "x"})
	list = appendMissingRequirement(list, models.MissingRequirement{Kind: "dataset", Name: "x"})
	if len(list) != 1 {
		t.Fatalf("duplicate missing requirement: %#v", list)
	}
}
