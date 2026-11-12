package scheduler

import (
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

// A contract whose required artifact comes from a failed core task cannot be
// accepted: the key exists only as a diagnostic.
func TestFailedProducerNeverSatisfiesRequiredArtifact(t *testing.T) {
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "required-artifact-failed", Status: models.StatusInProgress, IntentType: "Code_Execution",
		CurrentRevision: 3,
		Artifacts: map[string]models.Artifact{
			"metrics": {Key: "metrics", Value: "diagnostic", ProducerTaskID: "producer", Revision: 3},
		},
		CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "producer", Name: "producer", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusFailed, Error: "boom", OutputArtifacts: []string{"metrics"}, CreatedAt: now, UpdatedAt: now},
		},
		Acceptance: &models.AcceptanceContract{
			Goals:             []models.AcceptanceGoal{{ID: "g1", Statement: "metrics produced", EvidenceRefs: []string{"metrics"}}},
			RequiredArtifacts: []string{"metrics"},
			RequiredTaskIDs:   []string{"producer"},
		},
	}
	got := finishWithFinalizer(t, plan)
	if got.Outcome == models.OutcomeCompleted || got.Outcome == models.OutcomePartial {
		t.Fatalf("failed producer was accepted: %s/%s", got.Status, got.Outcome)
	}
}

// Evidence from an older revision does not describe the current run, even when
// its producer is currently completed.
func TestSupersededRevisionEvidenceIsNotAccepted(t *testing.T) {
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "stale-revision-evidence", Status: models.StatusInProgress, IntentType: "Code_Execution",
		CurrentRevision: 5,
		Artifacts: map[string]models.Artifact{
			"report": {Key: "report", Value: "old run", ProducerTaskID: "core", Revision: 2},
		},
		CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "core", Name: "core", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusCompleted, OutputArtifacts: []string{"report"}, CreatedAt: now, UpdatedAt: now},
		},
		Acceptance: &models.AcceptanceContract{
			Goals: []models.AcceptanceGoal{{ID: "g1", Statement: "report produced", EvidenceRefs: []string{"report"}}},
		},
	}
	got := finishWithFinalizer(t, plan)
	if got.Outcome == models.OutcomeCompleted || got.Outcome == models.OutcomePartial {
		t.Fatalf("superseded evidence was accepted: %s/%s", got.Status, got.Outcome)
	}
}

// A declared contract with a proven goal and a lost optional task stays partial.
func TestProvenContractWithOptionalLossStaysPartial(t *testing.T) {
	now := time.Now()
	optional := true
	plan := &models.PlanGraph{
		ID: "partial-positive", Status: models.StatusInProgress, IntentType: "Code_Execution",
		CurrentRevision: 1,
		Artifacts: map[string]models.Artifact{
			"report": {Key: "report", Value: "verified", ProducerTaskID: "core", Revision: 1},
		},
		CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "core", Name: "core", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusCompleted, OutputArtifacts: []string{"report"}, CreatedAt: now, UpdatedAt: now},
			{ID: "optional", Name: "optional", Type: "generate_code", AssignedTo: "coder_agent", Optional: &optional, Status: models.StatusFailed, Error: "metric failed", CreatedAt: now, UpdatedAt: now},
		},
		Acceptance: &models.AcceptanceContract{
			Goals: []models.AcceptanceGoal{{ID: "g1", Statement: "report produced", EvidenceRefs: []string{"report"}}},
		},
	}
	got := finishWithFinalizer(t, plan)
	if got.Status != models.StatusCompleted || got.Outcome != models.OutcomePartial {
		t.Fatalf("expected completed/partial, got %s/%s (%s)", got.Status, got.Outcome, got.OutcomeReason)
	}
	if got.OutcomeDetails == nil || len(got.OutcomeDetails.OptionalLosses) != 1 {
		t.Fatalf("partial details missing the loss: %#v", got.OutcomeDetails)
	}
}

func finishWithFinalizer(t *testing.T, plan *models.PlanGraph) *models.PlanGraph {
	t.Helper()
	return reviewBoundaryReconcile(t, plan)
}

// A plan that never declared a contract but fully succeeded is still completed.
func TestStrictPlanWithoutContractCanStillComplete(t *testing.T) {
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "strict-complete", Status: models.StatusInProgress, IntentType: "Code_Execution",
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "core", Name: "core", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusCompleted, CreatedAt: now, UpdatedAt: now},
		},
	}
	got := finishWithFinalizer(t, plan)
	if got.Status != models.StatusCompleted || got.Outcome != models.OutcomeCompleted {
		t.Fatalf("strict success regressed: %s/%s", got.Status, got.Outcome)
	}
}
