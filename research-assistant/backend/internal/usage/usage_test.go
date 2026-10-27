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
