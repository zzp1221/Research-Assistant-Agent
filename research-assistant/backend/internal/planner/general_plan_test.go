package planner

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestClassifyGeneralPlanShape(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	cases := []struct {
		name       string
		intent     models.IntentContext
		wantShape  generalPlanShape
		wantNodes  int
		wantTypes  []string
	}{
		{
			name:      "greeting is direct",
			intent:    models.IntentContext{RawIntent: "你好", IntentType: "General", Entities: map[string]any{}},
			wantShape: generalPlanDirect,
			wantNodes: 1,
			wantTypes: []string{"general_synthesis"},
		},
		{
			name:      "explain transformer is direct",
			intent:    models.IntentContext{RawIntent: "解释一下 Transformer 的多头注意力", IntentType: "General", Entities: map[string]any{}},
			wantShape: generalPlanDirect,
			wantNodes: 1,
			wantTypes: []string{"general_synthesis"},
		},
		{
			name:      "explain rag is not research",
			intent:    models.IntentContext{RawIntent: "解释一下 RAG 是什么", IntentType: "General", Entities: map[string]any{}},
			wantShape: generalPlanDirect,
			wantNodes: 1,
			wantTypes: []string{"general_synthesis"},
		},
		{
			name:      "summary is research",
			intent:    models.IntentContext{RawIntent: "总结 RAG 技术的最新进展", IntentType: "General", Entities: map[string]any{}},
			wantShape: generalPlanResearch,
			wantNodes: 2,
			wantTypes: []string{"general_research", "general_synthesis"},
		},
		{
			name:      "survey report is research",
			intent:    models.IntentContext{RawIntent: "帮我调研一下向量数据库选型并写报告", IntentType: "General", Entities: map[string]any{}},
			wantShape: generalPlanResearch,
			wantNodes: 2,
			wantTypes: []string{"general_research", "general_synthesis"},
		},
		{
			name: "paper analysis keeps four nodes",
			intent: models.IntentContext{
				RawIntent:  "分析这篇论文的创新点和局限",
				IntentType: "General",
				Entities:   map[string]any{"paper_metadata": map[string]any{"title": "Attention Is All You Need"}},
			},
			wantShape: generalPlanPaper,
			wantNodes: 4,
			wantTypes: []string{"paper_parse", "general_research", "verify_result", "general_synthesis"},
		},
		{
			name:      "literature discovery keeps three nodes",
			intent:    models.IntentContext{RawIntent: "找一下 RAG 相关论文", IntentType: "General", Entities: map[string]any{}},
			wantShape: generalPlanLiterature,
			wantNodes: 3,
			wantTypes: []string{"literature_search", "general_research", "general_synthesis"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyGeneralPlanShape(tc.intent); got != tc.wantShape {
				t.Fatalf("classifyGeneralPlanShape=%q, want %q", got, tc.wantShape)
			}
			plan, err := NewPlanner().BuildPlan(t.Context(), tc.intent)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Nodes) != tc.wantNodes {
				t.Fatalf("nodes=%d, want %d (%#v)", len(plan.Nodes), tc.wantNodes, plan.Nodes)
			}
			for i, want := range tc.wantTypes {
				if plan.Nodes[i].Type != want {
					t.Fatalf("node %d type=%s, want %s", i, plan.Nodes[i].Type, want)
				}
			}
			if plan.Meta.GeneralPlanShape != string(tc.wantShape) {
				t.Fatalf("meta.general_plan_shape=%q, want %q", plan.Meta.GeneralPlanShape, tc.wantShape)
			}
		})
	}
}

func TestGeneralResearchPlanBindsAttachments(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	intent := models.IntentContext{
		RawIntent:  "总结一下这份报告的主要内容",
		IntentType: "General",
		Entities:   map[string]any{"uploaded_files": []any{map[string]any{"name": "report.pdf"}}},
	}
	if got := classifyGeneralPlanShape(intent); got != generalPlanResearch {
		t.Fatalf("shape=%q, want research", got)
	}
	plan, err := NewPlanner().BuildPlan(t.Context(), intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 2 {
		t.Fatalf("nodes=%d, want 2", len(plan.Nodes))
	}
	if len(plan.Nodes[0].InputBindings) == 0 {
		t.Fatalf("research node must bind context_document/context_paper")
	}
}

func TestGeneralNeedsResearchKeywordScope(t *testing.T) {
	cases := map[string]bool{
		"解释一下 RAG 是什么":   false,
		"query rewrite 怎么实现": false,
		"总结 RAG 技术进展":    true,
		"compare vector stores": true,
	}
	for raw, want := range cases {
		got := generalNeedsResearch(models.IntentContext{RawIntent: raw, IntentType: "General", Entities: map[string]any{}})
		if got != want {
			t.Fatalf("generalNeedsResearch(%q)=%v, want %v", raw, got, want)
		}
	}
}
