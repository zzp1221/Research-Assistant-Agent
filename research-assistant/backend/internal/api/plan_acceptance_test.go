package api

import (
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

func acceptanceTestPlan() *models.PlanGraph {
	now := time.Now()
	optional := true
	plan := &models.PlanGraph{
		ID: "acceptance-plan", Status: models.StatusPending, CreatedAt: now, UpdatedAt: now,
		Artifacts: map[string]models.Artifact{},
		Nodes: []*models.TaskNode{
			{ID: "a", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusPending, OutputArtifacts: []string{"generated_code"}, CreatedAt: now, UpdatedAt: now},
			{ID: "b", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusPending, Optional: &optional, OutputArtifacts: []string{"metric"}, CreatedAt: now, UpdatedAt: now},
			{ID: "c", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusPending, Dependencies: []string{"a"}, OptionalDependencies: []string{"b"}, RequiredArtifacts: []string{"generated_code"}, CreatedAt: now, UpdatedAt: now},
		},
		Edges: []*models.TaskEdge{
			{ID: "e1", From: "a", To: "c", Type: "control"},
			{ID: "e2", From: "b", To: "c", Type: "optional"},
		},
		Acceptance: &models.AcceptanceContract{
			Goals:             []models.AcceptanceGoal{{ID: "g1", Statement: "core code executed", EvidenceRefs: []string{"generated_code"}}},
			RequiredArtifacts: []string{"generated_code"},
		},
	}
	return plan
}

func TestValidatePlanGraphAcceptsOptionalContract(t *testing.T) {
	result := validatePlanGraph(acceptanceTestPlan())
	if !result.Valid {
		t.Fatalf("valid optional plan rejected: %#v", result.Errors)
	}
}

func TestValidatePlanGraphRejectsMixedRelation(t *testing.T) {
	plan := acceptanceTestPlan()
	plan.Nodes[2].Dependencies = []string{"a", "b"}
	result := validatePlanGraph(plan)
	if result.Valid {
		t.Fatal("a dependency declared as both hard and optional was accepted")
	}
	found := false
	for _, item := range result.Errors {
		if item.Field == "optional_dependencies" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no optional_dependencies error: %#v", result.Errors)
	}
}

func TestValidatePlanGraphRejectsOptionalCycle(t *testing.T) {
	plan := acceptanceTestPlan()
	plan.Nodes[0].OptionalDependencies = []string{"c"}
	result := validatePlanGraph(plan)
	if result.Valid {
		t.Fatal("a cycle across hard and optional relations was accepted")
	}
}

func TestValidatePlanGraphRejectsAcceptanceWithoutCore(t *testing.T) {
	plan := acceptanceTestPlan()
	for _, node := range plan.Nodes {
		node.SetOptional(true)
	}
	result := validatePlanGraph(plan)
	if result.Valid {
		t.Fatal("an all-optional plan with an acceptance contract was accepted")
	}
}

func TestValidatePlanGraphRejectsAcceptanceReferencingOptionalTask(t *testing.T) {
	plan := acceptanceTestPlan()
	plan.Acceptance.RequiredTaskIDs = []string{"b"}
	result := validatePlanGraph(plan)
	if result.Valid {
		t.Fatal("acceptance requiring an optional task was accepted")
	}
}

func TestPlanDiffReportsOptionalAndAcceptanceChanges(t *testing.T) {
	before := acceptanceTestPlan()
	after := acceptanceTestPlan()
	after.Nodes[1].SetOptional(false)
	after.Acceptance.Goals[0].Statement = "core code executed and verified"
	diff := diffPlanGraphs(before, after)
	foundOptional := false
	foundAcceptance := false
	for _, entry := range diff.Diff {
		if entry.Kind == "node_modified" && entry.NodeID == "b" && entry.Field == "optional" {
			foundOptional = true
		}
		if entry.Kind == "acceptance_changed" {
			foundAcceptance = true
		}
	}
	if !foundOptional || !foundAcceptance {
		t.Fatalf("diff missed optional/acceptance changes: %#v", diff.Diff)
	}
	summary := diffSummary(diff)
	joined := ""
	for _, item := range summary {
		joined += item + ";"
	}
	if !contains(joined, "验收合同") {
		t.Fatalf("diff summary=%v", summary)
	}
}

func contains(value, needle string) bool {
	return len(value) >= len(needle) && indexOfString(value, needle) >= 0
}

func indexOfString(value, needle string) int {
	for i := 0; i+len(needle) <= len(value); i++ {
		if value[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
