package planner

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func graphWith(optional bool, nodes ...*models.TaskNode) *models.PlanGraph {
	for _, node := range nodes {
		if optional && node.Type == "optional" {
			node.SetOptional(true)
		}
	}
	plan := &models.PlanGraph{ID: "graph", Nodes: nodes}
	plan.Edges = buildEdgesFromNodes(nodes)
	return plan
}

func node(id string, deps []string, optionalDeps []string, outputs []string, required []string) *models.TaskNode {
	return &models.TaskNode{ID: id, Type: "generate_code", AssignedTo: "coder_agent", Dependencies: deps, OptionalDependencies: optionalDeps, OutputArtifacts: outputs, RequiredArtifacts: required}
}

func TestValidatePlanGraphRejectsDependencyDeclaredBothWays(t *testing.T) {
	a := node("a", nil, nil, []string{"x"}, nil)
	b := node("b", []string{"a"}, []string{"a"}, nil, nil)
	if err := validatePlanGraph(graphWith(false, a, b)); err == nil {
		t.Fatal("a dependency declared both hard and optional was accepted")
	}
}

func TestValidatePlanGraphRejectsOptionalCrossCycle(t *testing.T) {
	a := node("a", []string{"b"}, nil, nil, nil)
	b := node("b", nil, []string{"a"}, nil, nil)
	if err := validatePlanGraph(graphWith(false, a, b)); err == nil {
		t.Fatal("a cycle hidden across hard and optional relations was accepted")
	}
}

func TestValidatePlanGraphRejectsOptionalProducerOfRequiredArtifact(t *testing.T) {
	producer := node("b", nil, nil, []string{"metric"}, nil)
	producer.SetOptional(true)
	consumer := node("c", nil, []string{"b"}, nil, []string{"metric"})
	if err := validatePlanGraph(graphWith(false, producer, consumer)); err == nil {
		t.Fatal("a required artifact from an optional producer was accepted")
	}
}

func TestValidatePlanGraphAcceptsOptionalContract(t *testing.T) {
	core := node("a", nil, nil, []string{"report"}, nil)
	optional := node("b", nil, nil, []string{"metric"}, nil)
	optional.SetOptional(true)
	consumer := node("c", []string{"a"}, []string{"b"}, nil, nil)
	plan := graphWith(false, core, optional, consumer)
	plan.Acceptance = &models.AcceptanceContract{
		Goals:             []models.AcceptanceGoal{{ID: "g1", Statement: "core reproduced", EvidenceRefs: []string{"report"}}},
		RequiredArtifacts: []string{"report"},
	}
	if err := validatePlanGraph(plan); err != nil {
		t.Fatalf("valid optional contract rejected: %v", err)
	}
	edges := map[string]string{}
	for _, edge := range plan.Edges {
		edges[edge.From+"->"+edge.To] = edge.Type
	}
	if edges["b->c"] != "optional" {
		t.Fatalf("optional relation missing from the graph: %#v", edges)
	}
}

func TestValidatePlanGraphRejectsAcceptanceWithoutCoreWork(t *testing.T) {
	a := node("a", nil, nil, []string{"report"}, nil)
	a.SetOptional(true)
	plan := graphWith(false, a)
	plan.Acceptance = &models.AcceptanceContract{
		Goals: []models.AcceptanceGoal{{ID: "g1", Statement: "anything", EvidenceRefs: []string{"report"}}},
	}
	if err := validatePlanGraph(plan); err == nil {
		t.Fatal("an all-optional plan with an acceptance contract was accepted")
	}
}

func TestValidatePlanGraphRejectsAcceptanceWithoutEvidence(t *testing.T) {
	a := node("a", nil, nil, []string{"report"}, nil)
	plan := graphWith(false, a)
	plan.Acceptance = &models.AcceptanceContract{
		Goals: []models.AcceptanceGoal{{ID: "g1", Statement: "it works"}},
	}
	if err := validatePlanGraph(plan); err == nil {
		t.Fatal("an acceptance goal with no verifiable evidence was accepted")
	}
}

func TestFillInitialStatusesHonoursOptionalDependencies(t *testing.T) {
	producer := node("b", nil, nil, nil, nil)
	consumer := node("c", nil, []string{"b"}, nil, nil)
	nodes := []*models.TaskNode{producer, consumer}
	fillInitialStatuses(nodes)
	if consumer.Status != models.StatusPending {
		t.Fatalf("consumer with an optional dependency started ready: %s", consumer.Status)
	}
	if producer.Status != models.StatusReady {
		t.Fatalf("root node status=%s", producer.Status)
	}
}
