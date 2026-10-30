package usage

import (
	"context"
	"testing"
)

func TestRecorderAccumulatesAndSnapshots(t *testing.T) {
	t.Setenv("LLM_PRICE_PROMPT_PER_1K_USD", "0.5")
	t.Setenv("LLM_PRICE_COMPLETION_PER_1K_USD", "1.5")
	recorder := NewRecorder()
	recorder.RecordLLM("model-a", 1000, 500, false)
	recorder.RecordLLM("model-a", 1000, 500, true)
	recorder.RecordLLM("model-b", 100, 50, false)
	recorder.RecordSandbox(12.5)

	snapshot := recorder.Snapshot()
	if snapshot.LLMCalls != 3 || snapshot.PromptTokens != 2100 || snapshot.CompletionTokens != 1050 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	// 2100 prompt tokens * 0.5/1k + 1050 completion tokens * 1.5/1k = 1.05 + 1.575
	if diff := snapshot.CostUSD - 2.625; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("cost=%v", snapshot.CostUSD)
	}
	if !snapshot.Estimated {
		t.Fatal("estimated flag must be sticky")
	}
	if snapshot.SandboxSeconds != 12.5 {
		t.Fatalf("sandbox seconds=%v", snapshot.SandboxSeconds)
	}
	if snapshot.ByModel["model-a"].Calls != 2 {
		t.Fatalf("by model=%#v", snapshot.ByModel)
	}
}

func TestRecorderContextRoundTrip(t *testing.T) {
	recorder := NewRecorder()
	ctx := WithRecorder(context.Background(), recorder)
	if got := FromContext(ctx); got != recorder {
		t.Fatalf("recorder round trip failed: %#v", got)
	}
	if got := FromContext(context.Background()); got != nil {
		t.Fatalf("expected nil recorder, got %#v", got)
	}
}

func TestMerge(t *testing.T) {
	first := NewRecorder()
	first.RecordLLM("m", 10, 20, false)
	second := NewRecorder()
	second.RecordLLM("m", 5, 5, false)
	second.RecordSandbox(3)
	first.Merge(second.Snapshot())
	snapshot := first.Snapshot()
	if snapshot.LLMCalls != 2 || snapshot.PromptTokens != 15 || snapshot.CompletionTokens != 25 {
		t.Fatalf("merged=%#v", snapshot)
	}
	if snapshot.SandboxSeconds != 3 {
		t.Fatalf("sandbox=%v", snapshot.SandboxSeconds)
	}
}

func TestEstimateTokens(t *testing.T) {
	if got := EstimateTokens(""); got != 0 {
		t.Fatalf("empty=%d", got)
	}
	english := EstimateTokens("The quick brown fox jumps over the lazy dog")
	if english < 8 || english > 20 {
		t.Fatalf("english estimate=%d", english)
	}
	chinese := EstimateTokens("论文复现与实验分析")
	if chinese < 4 || chinese > 10 {
		t.Fatalf("chinese estimate=%d", chinese)
	}
}

func TestEstimateCostZeroWithoutPrices(t *testing.T) {
	t.Setenv("LLM_PRICE_PROMPT_PER_1K_USD", "")
	t.Setenv("LLM_PRICE_COMPLETION_PER_1K_USD", "")
	if got := EstimateCostUSD(1000, 1000); got != 0 {
		t.Fatalf("cost=%v", got)
	}
}
func TestRecordLLMWithProfilePrice(t *testing.T) {
	t.Setenv("LLM_PRICE_PROMPT_PER_1K_USD", "0.5")
	t.Setenv("LLM_PRICE_COMPLETION_PER_1K_USD", "1.5")
	recorder := NewRecorder()
	recorder.RecordLLMWithPrice("model-profile", 1000, 500, false, Price{PromptPer1K: 2, CompletionPer1K: 4})
	snapshot := recorder.Snapshot()
	// profile price wins over env: 1000*2/1000 + 500*4/1000 = 4
	if diff := snapshot.CostUSD - 4; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("profile cost=%v", snapshot.CostUSD)
	}
	if snapshot.CostSource != "profile" {
		t.Fatalf("cost source=%q", snapshot.CostSource)
	}
	if snapshot.ByModel["model-profile"].CostSource != "profile" {
		t.Fatalf("by model cost source=%q", snapshot.ByModel["model-profile"].CostSource)
	}
}

func TestRecordLLMEnvFallbackAndUnknown(t *testing.T) {
	t.Setenv("LLM_PRICE_PROMPT_PER_1K_USD", "0.5")
	t.Setenv("LLM_PRICE_COMPLETION_PER_1K_USD", "1.5")
	recorder := NewRecorder()
	recorder.RecordLLMWithPrice("m", 1000, 0, false, Price{})
	if recorder.Snapshot().CostSource != "env" {
		t.Fatalf("expected env source, got %q", recorder.Snapshot().CostSource)
	}

	t.Setenv("LLM_PRICE_PROMPT_PER_1K_USD", "")
	t.Setenv("LLM_PRICE_COMPLETION_PER_1K_USD", "")
	empty := NewRecorder()
	empty.RecordLLMWithPrice("m", 1000, 1000, false, Price{})
	snapshot := empty.Snapshot()
	if snapshot.CostUSD != 0 || snapshot.CostSource != "unknown" || snapshot.Estimated {
		t.Fatalf("unknown-price snapshot=%#v", snapshot)
	}
}

func TestCostSourceMergingPrefersWeakest(t *testing.T) {
	recorder := NewRecorder()
	recorder.RecordLLMWithPrice("m1", 10, 0, false, Price{PromptPer1K: 1})
	recorder.RecordLLMWithPrice("m2", 10, 0, false, Price{})
	t.Setenv("LLM_PRICE_PROMPT_PER_1K_USD", "")
	t.Setenv("LLM_PRICE_COMPLETION_PER_1K_USD", "")
	recorder.RecordLLMWithPrice("m3", 10, 0, true, Price{})
	if got := recorder.Snapshot().CostSource; got != "unknown" {
		t.Fatalf("merged source=%q, want unknown", got)
	}
}

func TestEstimateCostWithPriceSource(t *testing.T) {
	t.Setenv("LLM_PRICE_PROMPT_PER_1K_USD", "1")
	t.Setenv("LLM_PRICE_COMPLETION_PER_1K_USD", "2")
	cost, source := EstimateCostWithPrice(1000, 1000, Price{})
	if source != "env" || cost != 3 {
		t.Fatalf("env cost=%v source=%s", cost, source)
	}
	cost, source = EstimateCostWithPrice(1000, 1000, Price{PromptPer1K: 0.5, CompletionPer1K: 0.5})
	if source != "profile" || cost != 1 {
		t.Fatalf("profile cost=%v source=%s", cost, source)
	}
}
