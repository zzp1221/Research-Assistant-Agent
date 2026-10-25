package intent

import (
	"context"
	"errors"
	"testing"

	"research-assistant-backend/internal/models"
)

type fakeClassifier struct {
	result *Classification
	err    error
	calls  int
}

func (f *fakeClassifier) Classify(_ context.Context, _ string) (*Classification, error) {
	f.calls++
	return f.result, f.err
}

func ruleContext(intentType, raw string) models.IntentContext {
	return models.IntentContext{
		RawIntent:  raw,
		IntentType: intentType,
		Entities:   map[string]any{},
		Metadata:   map[string]any{},
		Confidence: 0.8,
		Source:     "rule_fallback",
	}
}

func TestHybridDowngradesFrameworkEvaluationForPlotRequest(t *testing.T) {
	classifier := &fakeClassifier{result: &Classification{
		IntentType: "Framework_Evaluation",
		Confidence: 0.91,
		Entities:   map[string]any{},
		Model:      "test-model",
	}}
	resolver := &Resolver{Mode: ModeHybrid, Threshold: 0.6, Classifier: classifier}
	got := resolver.Resolve(context.Background(), "帮我画一个正弦函数和余弦函数的对比图", ruleContext("Framework_Evaluation", "帮我画一个正弦函数和余弦函数的对比图"))
	if got.IntentType != "Code_Execution" {
		t.Fatalf("intent_type=%s, want Code_Execution", got.IntentType)
	}
	if got.Entities["needs_plot"] != true || got.Entities["output_mode"] != "plot" {
		t.Fatalf("plot entities=%#v", got.Entities)
	}
	if classifier.calls != 1 {
		t.Fatalf("classifier calls=%d, want 1", classifier.calls)
	}
}

func TestHybridKeepsFrameworkEvaluationForFrameworkPlot(t *testing.T) {
	classifier := &fakeClassifier{result: &Classification{
		IntentType: "Framework_Evaluation",
		Confidence: 0.93,
		Entities: map[string]any{
			"frameworks":      []any{"langchain", "llamaindex"},
			"needs_plot":      true,
			"needs_benchmark": true,
		},
		Model: "test-model",
	}}
	resolver := &Resolver{Mode: ModeHybrid, Threshold: 0.6, Classifier: classifier}
	got := resolver.Resolve(context.Background(), "对比 LangChain 和 LlamaIndex 的响应时间柱状图", ruleContext("Framework_Evaluation", "对比 LangChain 和 LlamaIndex 的响应时间柱状图"))
	if got.IntentType != "Framework_Evaluation" {
		t.Fatalf("intent_type=%s, want Framework_Evaluation", got.IntentType)
	}
	frameworks, ok := got.Entities["frameworks"].([]string)
	if !ok || len(frameworks) != 2 {
		t.Fatalf("frameworks=%#v", got.Entities["frameworks"])
	}
	if got.Entities["needs_plot"] != true {
		t.Fatalf("needs_plot=%#v", got.Entities["needs_plot"])
	}
}

func TestRuleModeDoesNotCallClassifier(t *testing.T) {
	classifier := &fakeClassifier{result: &Classification{IntentType: "Code_Execution", Confidence: 0.99}}
	resolver := &Resolver{Mode: ModeRule, Threshold: 0.6, Classifier: classifier}
	rule := ruleContext("General", "随便聊聊")
	got := resolver.Resolve(context.Background(), "随便聊聊", rule)
	if got.IntentType != "General" {
		t.Fatalf("intent_type=%s", got.IntentType)
	}
	if classifier.calls != 0 {
		t.Fatalf("classifier calls=%d, want 0", classifier.calls)
	}
}

func TestClassifierFailureFallsBackToRule(t *testing.T) {
	classifier := &fakeClassifier{err: errors.New("gateway down")}
	resolver := &Resolver{Mode: ModeHybrid, Threshold: 0.6, Classifier: classifier}
	rule := ruleContext("Code_Execution", "运行一段 Python 代码")
	got := resolver.Resolve(context.Background(), "运行一段 Python 代码", rule)
	if got.IntentType != "Code_Execution" || got.Source != "rule_fallback" {
		t.Fatalf("fallback intent=%s source=%s", got.IntentType, got.Source)
	}
}

func TestPostValidatePromotesFrameworkBenchmark(t *testing.T) {
	classifier := &fakeClassifier{result: &Classification{
		IntentType: "Code_Execution",
		Confidence: 0.9,
		Entities: map[string]any{
			"frameworks":      []any{"langchain", "llamaindex"},
			"needs_benchmark": true,
		},
	}}
	resolver := &Resolver{Mode: ModeHybrid, Threshold: 0.6, Classifier: classifier}
	got := resolver.Resolve(context.Background(), "对比 LangChain 和 LlamaIndex 的性能", ruleContext("Code_Execution", "对比 LangChain 和 LlamaIndex 的性能"))
	if got.IntentType != "Framework_Evaluation" {
		t.Fatalf("intent_type=%s, want Framework_Evaluation", got.IntentType)
	}
}

func TestHighRiskIntentRequiresRuleSupport(t *testing.T) {
	classifier := &fakeClassifier{result: &Classification{IntentType: "AutoResearch", Confidence: 0.99}}
	resolver := &Resolver{Mode: ModeHybrid, Threshold: 0.6, Classifier: classifier}
	rule := ruleContext("General", "帮我优化一下这段文字")
	got := resolver.Resolve(context.Background(), "帮我优化一下这段文字", rule)
	if got.IntentType != "General" {
		t.Fatalf("high-risk model intent was accepted without rule support: %s", got.IntentType)
	}
	if classifier.calls != 1 {
		t.Fatalf("classifier calls=%d", classifier.calls)
	}
}

func TestGeneralPlotRequestPromotesToCodeExecution(t *testing.T) {
	classifier := &fakeClassifier{result: &Classification{
		IntentType: "General",
		Confidence: 0.9,
		Entities:   map[string]any{"needs_plot": true},
	}}
	resolver := &Resolver{Mode: ModeHybrid, Threshold: 0.6, Classifier: classifier}
	got := resolver.Resolve(context.Background(), "帮我画一个图", ruleContext("General", "帮我画一个图"))
	if got.IntentType != "Code_Execution" {
		t.Fatalf("intent_type=%s, want Code_Execution", got.IntentType)
	}
	if got.Entities["needs_plot"] != true {
		t.Fatalf("needs_plot=%#v", got.Entities["needs_plot"])
	}
}
func TestClassifierFailureStillRunsPostValidate(t *testing.T) {
	classifier := &fakeClassifier{err: errors.New("gateway down")}
	resolver := &Resolver{Mode: ModeHybrid, Threshold: 0.6, Classifier: classifier}
	rule := ruleContext("Framework_Evaluation", "对比一下python中二分法和摩尔排序的性能")
	got := resolver.Resolve(context.Background(), "对比一下python中二分法和摩尔排序的性能", rule)
	if got.IntentType == "Framework_Evaluation" {
		t.Fatalf("rule fallback bypassed postValidate: %s", got.IntentType)
	}
}

func TestRuleModeRunsPostValidate(t *testing.T) {
	resolver := &Resolver{Mode: ModeRule}
	rule := ruleContext("Framework_Evaluation", "帮我画一个正弦函数和余弦函数的对比图")
	got := resolver.Resolve(context.Background(), "帮我画一个正弦函数和余弦函数的对比图", rule)
	if got.IntentType != "Code_Execution" {
		t.Fatalf("rule mode did not post-validate: %s", got.IntentType)
	}
	if got.Entities["needs_plot"] != true {
		t.Fatalf("needs_plot not set: %#v", got.Entities)
	}
}

func TestLLMFrameworkEvaluationCannotOverrideCodePerformance(t *testing.T) {
	classifier := &fakeClassifier{result: &Classification{
		IntentType: "Framework_Evaluation",
		Confidence: 0.92,
		Entities:   map[string]any{},
	}}
	resolver := &Resolver{Mode: ModeHybrid, Threshold: 0.6, Classifier: classifier}
	raw := "对比一下python中二分法和摩尔排序的性能"
	got := resolver.Resolve(context.Background(), raw, ruleContext("Code_Execution", raw))
	if got.IntentType != "Code_Execution" {
		t.Fatalf("intent_type=%s, want Code_Execution", got.IntentType)
	}
}
