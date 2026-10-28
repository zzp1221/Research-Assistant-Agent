package planner

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestEnsureVerificationNodeAppendsForImagePlans(t *testing.T) {
	plot := newNode("Render Plot", "render_plot", "coder_agent", nil, nil, []string{"plot_image"}, true, "x")
	summary := newNode("Report", "general_synthesis", "answer_agent", []string{plot.ID}, []string{"plot_image"}, []string{"general_response"}, false, "x")
	nodes := ensureVerificationNode([]*models.TaskNode{plot, summary})
	found := false
	for _, node := range nodes {
		if node.Type == "verify_result" {
			found = true
			if len(node.RequiredArtifacts) == 0 || !containsArtifact(node.RequiredArtifacts, "plot_image") {
				t.Fatalf("verify node must consume the produced image: %#v", node.RequiredArtifacts)
			}
		}
	}
	if !found {
		t.Fatalf("image plan without verification must receive a verify_result node")
	}
}

func TestEnsureVerificationNodeIsIdempotent(t *testing.T) {
	plot := newNode("Render Plot", "render_plot", "coder_agent", nil, nil, []string{"plot_image"}, true, "x")
	verify := newNode("Verify", "verify_result", "data_agent", []string{plot.ID}, []string{"plot_image"}, []string{"verification_report"}, true, "x")
	nodes := ensureVerificationNode([]*models.TaskNode{plot, verify})
	count := 0
	for _, node := range nodes {
		if node.Type == "verify_result" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("verify_result count = %d, want 1", count)
	}
}

func TestEnsureVerificationNodeSkipsTextOnlyPlans(t *testing.T) {
	research := newNode("Research", "general_research", "librarian_agent", nil, nil, []string{"background_context"}, true, "x")
	nodes := ensureVerificationNode([]*models.TaskNode{research})
	for _, node := range nodes {
		if node.Type == "verify_result" {
			t.Fatalf("text-only plan must not get an image verification node")
		}
	}
}
