package planner

import (
	"context"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestLiteratureDiscoveryIntentBuildsSearchPlan(t *testing.T) {
	t.Setenv("PLANNER_AGENT_ENABLED", "false")
	intent := models.IntentContext{
		RawIntent:  "帮我找 5 篇关于 graph neural network 的综述并总结",
		IntentType: "General",
		Entities:   map[string]any{},
	}
	plan, err := NewPlanner().BuildPlan(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		types = append(types, node.Type)
	}
	want := []string{"literature_search", "general_research", "general_synthesis"}
	if len(types) != len(want) {
		t.Fatalf("types=%v want=%v", types, want)
	}
	for index := range want {
		if types[index] != want[index] {
			t.Fatalf("types=%v want=%v", types, want)
		}
	}
	search := plan.Nodes[0]
	if search.AssignedTo != "librarian_agent" {
		t.Fatalf("assigned=%q", search.AssignedTo)
	}
	if got := search.Inputs["query"]; got != intent.RawIntent {
		t.Fatalf("query=%v", got)
	}
}

func TestPaperAnalysisIsNotLiteratureDiscovery(t *testing.T) {
	t.Setenv("PLANNER_AGENT_ENABLED", "false")
	intent := models.IntentContext{
		RawIntent:  "分析这篇论文的主要创新点",
		IntentType: "General",
		Entities:   map[string]any{},
	}
	plan, err := NewPlanner().BuildPlan(context.Background(), intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range plan.Nodes {
		if node.Type == "literature_search" {
			t.Fatalf("paper analysis must not add literature_search: %#v", plan.Nodes)
		}
	}
}
