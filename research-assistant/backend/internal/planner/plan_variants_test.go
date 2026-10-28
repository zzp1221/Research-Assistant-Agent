package planner

import (
	"context"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestTemplateVariantsBuildValidPlans(t *testing.T) {
	codeIntent := models.IntentContext{
		RawIntent:  "用 Python 计算并画图",
		IntentType: "Code_Execution",
		Entities:   map[string]any{"needs_plot": true},
	}
	frameworkIntent := models.IntentContext{
		RawIntent:  "对比 LangChain 和 LlamaIndex",
		IntentType: "Framework_Evaluation",
		Entities:   map[string]any{"frameworks": []string{"langchain", "llamaindex"}},
	}
	paperIntent := models.IntentContext{
		RawIntent:  "复现这篇论文",
		IntentType: "Paper_Reproduction",
		Entities:   map[string]any{"paper_metadata": map[string]any{"title": "Attention Is All You Need"}},
	}
	generalIntent := models.IntentContext{
		RawIntent:  "总结 RAG 技术进展",
		IntentType: "General",
		Entities:   map[string]any{},
	}
	cases := []struct {
		name    string
		intent  models.IntentContext
		variant string
	}{
		{"code-simple", codeIntent, "simple"},
		{"code-standard", codeIntent, "standard"},
		{"code-full", codeIntent, "full"},
		{"framework-simple", frameworkIntent, "simple"},
		{"framework-standard", frameworkIntent, "standard"},
		{"framework-full", frameworkIntent, "full"},
		{"paper-light", paperIntent, "light"},
		{"paper-standard", paperIntent, "standard"},
		{"paper-full", paperIntent, "full"},
		{"general-simple", generalIntent, "simple"},
		{"general-standard", generalIntent, "standard"},
		{"general-full", generalIntent, "full"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodes, err := buildRegisteredNodesWithVariant(context.Background(), tc.intent, tc.variant)
			if err != nil {
				t.Fatal(err)
			}
			if len(nodes) == 0 {
				t.Fatal("builder returned no nodes")
			}
			normalizeArtifactContracts(nodes)
			nodes = ensureVerificationNode(nodes)
			normalizeArtifactContracts(nodes)
			plan := &models.PlanGraph{
				ID:         "variant-test",
				UserIntent: tc.intent.RawIntent,
				IntentType: tc.intent.IntentType,
				Nodes:      nodes,
				Edges:      buildEdgesFromNodes(nodes),
				Artifacts:  map[string]models.Artifact{},
			}
			fillInitialStatuses(nodes)
			if err := validatePlanGraph(plan); err != nil {
				t.Fatalf("variant %s produced invalid graph: %v", tc.name, err)
			}
		})
	}
}
