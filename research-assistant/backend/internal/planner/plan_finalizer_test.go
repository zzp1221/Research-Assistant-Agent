package planner

import (
	"context"
	"encoding/json"
	"testing"

	"research-assistant-backend/internal/models"
)

func claimGraphArtifact(t *testing.T, status string, verified int, claims int) models.Artifact {
	t.Helper()
	graph := models.ClaimEvidenceGraph{
		Version:      models.ClaimEvidenceGraphVersion,
		Status:       status,
		StatusReason: "test fixture",
		Claims:       make([]models.ClaimEvidenceResult, claims),
		Summary:      models.ClaimEvidenceSummary{TotalClaims: claims, Verified: verified},
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	return models.Artifact{Key: "claim_evidence_graph", Type: "json", Value: string(raw)}
}

func smokePlanWithClaimGraph(t *testing.T, graph models.Artifact) *models.PlanGraph {
	t.Helper()
	mode, _ := json.Marshal(map[string]any{"effective_mode": "smoke"})
	return &models.PlanGraph{
		ID:         "plan-claim-test",
		IntentType: "Paper_Reproduction",
		Status:     models.StatusCompleted,
		Nodes:      []*models.TaskNode{{ID: "n1", Type: "paper_code_execute", Status: models.StatusCompleted}},
		Artifacts: map[string]models.Artifact{
			"reproduction_mode_report": {Key: "reproduction_mode_report", Value: string(mode)},
			"claim_evidence_graph":     graph,
		},
	}
}

func TestFinalizeSmokePlanWithAssessedClaimsStaysSmoke(t *testing.T) {
	plan := smokePlanWithClaimGraph(t, claimGraphArtifact(t, "assessed", 2, 2))
	if err := (&PlanFinalizer{}).Finalize(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if plan.Outcome != models.OutcomeSmoke {
		t.Fatalf("outcome=%s, want smoke", plan.Outcome)
	}
}

func TestFinalizeDegradedClaimEvidenceBecomesNeedsReview(t *testing.T) {
	plan := smokePlanWithClaimGraph(t, claimGraphArtifact(t, "degraded", 0, 3))
	if err := (&PlanFinalizer{}).Finalize(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if plan.Outcome != models.OutcomeNeedsReview {
		t.Fatalf("outcome=%s, want needs_review", plan.Outcome)
	}
}

func TestFinalizeEmptyVerifiedClaimsBecomesNeedsReview(t *testing.T) {
	plan := smokePlanWithClaimGraph(t, claimGraphArtifact(t, "assessed", 0, 4))
	if err := (&PlanFinalizer{}).Finalize(context.Background(), plan); err != nil {
		t.Fatal(err)
	}
	if plan.Outcome != models.OutcomeNeedsReview {
		t.Fatalf("outcome=%s, want needs_review", plan.Outcome)
	}
}
