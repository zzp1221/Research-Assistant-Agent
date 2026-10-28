package scheduler

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/events"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

// verificationExecutor reports a completed execution whose verification failed
// — the advisory semantics from design D3.
type verificationExecutor struct{}

func (verificationExecutor) ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
	return &models.TaskExecutionResult{
		Status: models.StatusCompleted,
		Result: "figure produced",
		Verification: &models.Verification{
			Status:     models.VerificationFail,
			Level:      models.VerificationLevelVisual,
			Verified:   false,
			Issues:     []string{"axis label missing"},
			Reason:     "vision model reported a problem",
			CheckedAt:  time.Now(),
			VerifiedBy: &models.VerifiedBy{ProfileID: "p1", Model: "vision", Mode: "main"},
		},
		Artifacts: []models.Artifact{
			{Key: "plot_image", Type: "image_base64", Kind: "plot", Value: "iVBORw0KGgo"},
		},
	}, nil
}

func TestVerificationFailKeepsTaskAndPlanCompleted(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	bus := events.NewBus()
	runner := NewScheduler(planStore, verificationExecutor{}, bus, 1)
	now := time.Now()
	plan := &models.PlanGraph{
		ID:         "plan-verify-1",
		UserIntent: "draw a figure",
		IntentType: "Code_Execution",
		Status:     models.StatusInProgress,
		OwnerID:    "owner",
		Nodes: []*models.TaskNode{
			{
				ID:              "verify",
				Name:            "Verify",
				Type:            "verify_result",
				AssignedTo:      "data_agent",
				Status:          models.StatusReady,
				OutputArtifacts: []string{"verification_report"},
				CreatedAt:       now,
				UpdatedAt:       now,
			},
		},
		Artifacts: map[string]models.Artifact{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatalf("save plan: %v", err)
	}
	executionID, leaseOwner, epoch, err := runner.markTaskStarted(plan.ID, "verify")
	if err != nil {
		t.Fatalf("mark started: %v", err)
	}
	result := &models.TaskExecutionResult{
		Status: models.StatusCompleted,
		Verification: &models.Verification{
			Status:    models.VerificationFail,
			Level:     models.VerificationLevelVisual,
			Issues:    []string{"axis label missing"},
			CheckedAt: time.Now(),
		},
		Artifacts: []models.Artifact{{Key: "plot_image", Type: "image_base64", Kind: "plot", Value: "iVBORw0KGgo"}},
	}
	if err := runner.markTaskCompleted(plan.ID, "verify", executionID, leaseOwner, epoch, result); err != nil {
		t.Fatalf("mark completed: %v", err)
	}
	stored, err := planStore.GetPlan(plan.ID)
	if err != nil {
		t.Fatalf("get plan: %v", err)
	}
	node := stored.Nodes[0]
	if node.Status != models.StatusCompleted {
		t.Fatalf("node status = %s, must stay completed when verification fails", node.Status)
	}
	if node.Verification == nil || node.Verification.Status != models.VerificationFail {
		t.Fatalf("node verification = %#v", node.Verification)
	}
	if stored.Verification == nil || stored.Verification.Status != models.VerificationFail {
		t.Fatalf("plan verification = %#v", stored.Verification)
	}
	artifact := stored.Artifacts["plot_image"]
	verificationMeta, ok := artifact.Metadata["verification"].(map[string]any)
	if !ok {
		t.Fatalf("artifact verification metadata missing: %#v", artifact.Metadata)
	}
	if verificationMeta["status"] != models.VerificationFail {
		t.Fatalf("artifact verification status = %v", verificationMeta["status"])
	}
	planEvents, err := planStore.ListEventsAfter(plan.ID, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	found := false
	for _, event := range planEvents {
		if event.EventType == "verification_failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("verification_failed event missing: %#v", planEvents)
	}
}
