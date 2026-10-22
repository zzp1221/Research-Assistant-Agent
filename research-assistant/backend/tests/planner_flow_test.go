package tests

import (
	"context"
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
)

func TestBuildPlan_FrameworkEvaluationExample(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	p := planner.NewPlanner()
	intent := "比较 LangChain 和 LlamaIndex 在同一个 RAG 场景下的最小可运行例子，并给出客观对比"
	plan, err := p.BuildPlan(context.Background(), models.IntentContext{
		RawIntent:   intent,
		IntentType:  "Framework_Evaluation",
		Entities:    map[string]any{},
		Constraints: map[string]any{},
		Metadata:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("BuildPlan returned error: %v", err)
	}
	if len(plan.Nodes) < 7 {
		t.Fatalf("expected a multi-step framework plan, got %d nodes", len(plan.Nodes))
	}
	var librarianCount, coderCount, sandboxCount, dataCount int
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.AssignedTo {
		case "librarian_agent":
			librarianCount++
		case "coder_agent":
			coderCount++
		case "sandbox_agent":
			sandboxCount++
		case "data_agent":
			dataCount++
		}
	}
	if librarianCount == 0 || coderCount < 2 || sandboxCount < 2 || dataCount == 0 {
		t.Fatalf("unexpected task distribution: librarian=%d coder=%d sandbox=%d data=%d", librarianCount, coderCount, sandboxCount, dataCount)
	}
	if len(plan.Edges) == 0 {
		t.Fatal("expected a connected plan graph")
	}
}
