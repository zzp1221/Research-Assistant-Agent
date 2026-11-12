package planner

import (
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

func binderPlan(optionalStatus models.TaskStatus) *models.PlanGraph {
	now := time.Now()
	optional := true
	return &models.PlanGraph{
		ID: "binder-plan", CreatedAt: now, UpdatedAt: now,
		Artifacts: map[string]models.Artifact{
			"metric": {Key: "metric", Value: "diagnostic", ProducerTaskID: "opt"},
		},
		Nodes: []*models.TaskNode{
			{ID: "opt", Name: "optional metric", Type: "generate_code", AssignedTo: "coder_agent", Status: optionalStatus, Optional: &optional, OutputArtifacts: []string{"metric"}},
			{ID: "report", Name: "report", Type: "general_synthesis", AssignedTo: "answer_agent", Status: models.StatusPending, OptionalDependencies: []string{"opt"}, RequiredArtifacts: []string{"metric"}},
		},
	}
}

func TestContextBinderRefusesOptionalOnlyArtifact(t *testing.T) {
	binder := &ContextBinder{}
	plan := binderPlan(models.StatusFailed)
	_, err := binder.BuildInputs(plan, plan.Nodes[1])
	if err == nil {
		t.Fatal("an artifact produced only by an optional dependency was bound as a trusted input")
	}
	if !strings.Contains(err.Error(), "optional") {
		t.Fatalf("error=%q does not explain the optional producer", err)
	}
}

func TestContextBinderBindsCompletedOptionalArtifact(t *testing.T) {
	binder := &ContextBinder{}
	plan := binderPlan(models.StatusCompleted)
	// A required artifact with an optional-only producer still refuses binding;
	// the consumer is supposed to declare such a value as an optional input.
	_, err := binder.BuildInputs(plan, plan.Nodes[1])
	if err == nil {
		t.Fatal("required artifact from an optional producer was accepted")
	}

	// Without the hard requirement the value reaches the task only through the
	// optional-input pass, and only while the producer actually succeeded.
	plan.Nodes[1].RequiredArtifacts = nil
	inputs, err := binder.BuildInputs(plan, plan.Nodes[1])
	if err != nil {
		t.Fatal(err)
	}
	if inputs["metric"] != "diagnostic" {
		t.Fatalf("completed optional producer value missing: %#v", inputs)
	}

	plan.Nodes[0].Status = models.StatusFailed
	inputs, err = binder.BuildInputs(plan, plan.Nodes[1])
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := inputs["metric"]; ok {
		t.Fatalf("failed optional producer value was trusted: %#v", inputs)
	}
}
