package planner

import (
	"context"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestComplexityRouterRuleLevels(t *testing.T) {
	cases := []struct {
		name        string
		intent      models.IntentContext
		attachments []map[string]any
		want        ComplexityLevel
	}{
		{
			name:   "greeting is L0",
			intent: models.IntentContext{RawIntent: "你好", IntentType: "General", Entities: map[string]any{}},
			want:   ComplexityL0,
		},
		{
			name:   "explain is L0",
			intent: models.IntentContext{RawIntent: "解释一下 Transformer 的多头注意力", IntentType: "General", Entities: map[string]any{}},
			want:   ComplexityL0,
		},
		{
			name:   "simple code is L1",
			intent: models.IntentContext{RawIntent: "写一段 Python 代码计算均值", IntentType: "Code_Execution", Entities: map[string]any{}},
			want:   ComplexityL1,
		},
		{
			name:   "plot code stays standard because of runtime chain",
			intent: models.IntentContext{RawIntent: "画一个正弦函数图", IntentType: "Code_Execution", Entities: map[string]any{"needs_plot": true}},
			want:   ComplexityL2,
		},
		{
			name:   "summary is L2",
			intent: models.IntentContext{RawIntent: "总结 RAG 技术进展", IntentType: "General", Entities: map[string]any{}},
			want:   ComplexityL2,
		},
		{
			name:   "two frameworks is L2",
			intent: models.IntentContext{RawIntent: "对比 LangChain 和 LlamaIndex 的响应时间", IntentType: "Framework_Evaluation", Entities: map[string]any{"frameworks": []string{"langchain", "llamaindex"}}},
			want:   ComplexityL2,
		},
		{
			name: "complex paper is L4",
			intent: models.IntentContext{
				RawIntent:  "复现 Attention Is All You Need，使用自定义数据集，跑 3 个 seed，生成 claim evidence 和 hidden holdout",
				IntentType: "Paper_Reproduction",
				Entities: map[string]any{
					"paper_metadata": map[string]any{"title": "Attention Is All You Need"},
					"repo_url":       "https://github.com/example/attention",
				},
			},
			want: ComplexityL4,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision := NewComplexityRouterFromEnv().Decide(context.Background(), tc.intent, tc.attachments, nil)
			if decision.Complexity != tc.want {
				t.Fatalf("complexity=%s want %s (reason=%s)", decision.Complexity, tc.want, decision.Reason)
			}
			if decision.Variant == "" || decision.RouteHint == "" {
				t.Fatalf("decision missing variant/route hint: %+v", decision)
			}
		})
	}
}

func TestComplexityRouterNeverDowngradesLLMLevel(t *testing.T) {
	intent := models.IntentContext{
		RawIntent:  "画一个简单的图",
		IntentType: "Code_Execution",
		Entities:   map[string]any{"needs_plot": true},
		Complexity: "L3",
	}
	decision := NewComplexityRouterFromEnv().Decide(context.Background(), intent, nil, nil)
	if levelRank(decision.Complexity) < 3 {
		t.Fatalf("LLM level was downgraded: %+v", decision)
	}
	if decision.Source != "merged" {
		t.Fatalf("source=%s want merged", decision.Source)
	}
}

func TestComplexityRouterDisabledFallsBackToStandard(t *testing.T) {
	t.Setenv("COMPLEXITY_ROUTER_ENABLED", "false")
	decision := NewComplexityRouterFromEnv().Decide(context.Background(), models.IntentContext{RawIntent: "你好", IntentType: "General"}, nil, nil)
	if decision.Complexity != ComplexityL2 || decision.Variant != "standard" || decision.Source != "disabled" {
		t.Fatalf("unexpected disabled decision: %+v", decision)
	}
}

func TestMergeAgentPlanWithSkeletonKeepsSkeletonIdentity(t *testing.T) {
	skeleton := []*models.TaskNode{
		{ID: "sk-1", Name: "Generate Code", Type: "generate_code", OutputArtifacts: []string{"generated_code"}},
		{ID: "sk-2", Name: "Execute Code", Type: "execute_code", Dependencies: []string{"sk-1"}, OutputArtifacts: []string{"execution_result"}},
	}
	agentNodes := []*models.TaskNode{
		{ID: "ag-1", Name: "Generate Code", Type: "generate_code", Description: "generate", OutputArtifacts: []string{"other_artifact"}},
		{ID: "ag-2", Name: "Execute Code", Type: "execute_code", Dependencies: []string{"ag-1"}, OutputArtifacts: []string{"execution_result"}},
		{ID: "ag-3", Name: "Extra Check", Type: "verify_result", Dependencies: []string{"ag-2"}, OutputArtifacts: []string{"verification_report"}},
	}
	merged := mergeAgentPlanWithSkeleton(skeleton, agentNodes)
	if len(merged) != 3 {
		t.Fatalf("merged nodes=%d want 3", len(merged))
	}
	if merged[0].ID != "sk-1" || merged[0].OutputArtifacts[0] != "generated_code" {
		t.Fatalf("skeleton identity/artifacts not preserved: %#v", merged[0])
	}
	if merged[2].Dependencies[0] != "sk-2" {
		t.Fatalf("new node dependency not remapped: %#v", merged[2].Dependencies)
	}
}
