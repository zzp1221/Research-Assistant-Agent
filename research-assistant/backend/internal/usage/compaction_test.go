package usage

import "testing"

func TestNativeCompactionTracksUnknownCostSeparately(t *testing.T) {
	t.Setenv("LLM_PRICE_PROMPT_PER_1K_USD", "100")
	r := NewRecorder()
	r.RecordCompaction("model", 100, 30, false, Price{PromptPer1K: 100}, false)
	got := r.Snapshot()
	if got.LLMCalls != 1 || got.Compaction.Calls != 1 || got.CostUSD != 0 || got.CostSource != "unknown" || got.Compaction.CostSource != "unknown" {
		t.Fatalf("native price was inferred: %#v", got)
	}
	r.RecordCompaction("model", 100, 30, false, Price{PromptPer1K: 1, CompletionPer1K: 2}, true)
	got = r.Snapshot()
	if got.LLMCalls != 2 || got.Compaction.Calls != 2 || got.Compaction.CostUSD != .16 || got.CostSource != "unknown" {
		t.Fatalf("purpose or unknown cost lost: %#v", got)
	}
}
