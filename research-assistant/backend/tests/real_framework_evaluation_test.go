package tests

import (
	"context"
	"os"
	"testing"
	"time"

	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
)

func TestRealFrameworkEvaluationPlan(t *testing.T) {
	if os.Getenv("REAL_FRAMEWORK_EVAL_TEST") == "" {
		t.Skip("跳过真实框架对比测试。如需运行，请设置 REAL_FRAMEWORK_EVAL_TEST=1")
	}
	if _, err := appconfig.LoadLLMConfig(); err != nil {
		t.Skipf("跳过真实框架对比测试：无法从 config.toml 读取 LLM 配置: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	p := planner.NewPlanner()
	plan, err := p.BuildPlan(ctx, models.IntentContext{
		RawIntent:   "比较 LangChain 和 LlamaIndex 在同一个 RAG 场景下的最小可运行例子，并给出客观对比",
		IntentType:  "Framework_Evaluation",
		Entities:    map[string]any{},
		Constraints: map[string]any{},
		Metadata:    map[string]any{},
	})
	if err != nil {
		t.Fatalf("BuildPlan failed: %v", err)
	}
	if len(plan.Nodes) == 0 {
		t.Fatal("real planner returned an empty plan graph")
	}
	var coderCount int
	for _, node := range plan.Nodes {
		if node != nil && node.AssignedTo == "coder_agent" {
			coderCount++
		}
	}
	if coderCount == 0 {
		t.Fatalf("expected coder nodes in real plan, got %#v", plan.Nodes)
	}
}
