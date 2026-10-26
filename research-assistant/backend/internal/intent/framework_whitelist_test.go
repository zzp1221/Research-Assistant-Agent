package intent

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestFilterKnownFrameworksDropsUnknownNames(t *testing.T) {
	got := filterKnownFrameworks([]string{"bisect", "sorted", "LangChain"})
	if len(got) != 1 || got[0] != "langchain" {
		t.Fatalf("unexpected frameworks: %v", got)
	}
}

func TestPostValidateDowngradesFunctionComparison(t *testing.T) {
	ctx := models.IntentContext{
		RawIntent:  "用 Python 计算并对比 bisect 和 sorted 在 10000 条数据上的查找性能，输出一个柱状图",
		IntentType: "Framework_Evaluation",
		Entities:   map[string]any{"frameworks": []any{"bisect", "sorted"}},
	}
	out := postValidate(ctx)
	if out.IntentType != "Code_Execution" {
		t.Fatalf("expected Code_Execution, got %s", out.IntentType)
	}
	if _, ok := out.Entities["frameworks"]; ok {
		t.Fatalf("unknown frameworks must be removed from entities: %v", out.Entities["frameworks"])
	}
}

func TestPostValidateKeepsWhitelistedFrameworkComparison(t *testing.T) {
	ctx := models.IntentContext{
		RawIntent:  "对比 LangChain 和 LlamaIndex 的 RAG 性能",
		IntentType: "Framework_Evaluation",
		Entities:   map[string]any{"frameworks": []any{"langchain", "llamaindex"}},
	}
	out := postValidate(ctx)
	if out.IntentType != "Framework_Evaluation" {
		t.Fatalf("expected Framework_Evaluation, got %s", out.IntentType)
	}
	frameworks, _ := out.Entities["frameworks"].([]string)
	if len(frameworks) != 2 {
		t.Fatalf("expected two whitelisted frameworks, got %v", out.Entities["frameworks"])
	}
}
