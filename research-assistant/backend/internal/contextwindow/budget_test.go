package contextwindow

import (
	"context"
	"research-assistant-backend/internal/modelprofile"
	"testing"
)

func TestModelBudgetsAndInvalidCombinations(t *testing.T) {
	for _, test := range []struct{ window, limit, trigger int }{{32000, 22208, 17766}, {128000, 113408, 90726}, {200000, 181808, 145446}} {
		b, err := ResolveBudget(modelprofile.Profile{ContextWindowTokens: test.window, MaxOutputTokens: 8192}, 0)
		if err != nil || b.PromptLimit() != test.limit || b.Trigger != test.trigger {
			t.Fatalf("window %d: %#v %v", test.window, b, err)
		}
	}
	for _, p := range []modelprofile.Profile{{}, {ContextWindowTokens: -1}, {ContextWindowTokens: 100, MaxOutputTokens: 99}, {ContextWindowTokens: 100, MaxOutputTokens: -1}} {
		if _, err := ResolveBudget(p, 0); err == nil {
			t.Fatalf("invalid budget accepted: %#v", p)
		}
	}
	p := modelprofile.Profile{ContextWindowTokens: 32000, MaxOutputTokens: 512}
	if _, err := ResolveBudget(p, 1024); err == nil {
		t.Fatal("output cap exceeded")
	}
}

func TestStoreGenerationsCASAndBranchIsolation(t *testing.T) {
	s := NewStore(t.TempDir())
	unlock, err := s.Acquire(context.Background(), "owner/project/session/a")
	if err != nil {
		t.Fatal(err)
	}
	state := State{Binding: "profile", Source: []string{"first"}}
	if err := s.Commit("owner/project/session/a", 0, &state); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit("owner/project/session/a", 0, &state); err == nil {
		t.Fatal("stale generation overwrote current window")
	}
	unlock()
	loaded, err := NewStore(s.dir).Load("owner/project/session/a")
	if err != nil || loaded.Generation != 1 || loaded.Source[0] != "first" {
		t.Fatalf("restart: %#v %v", loaded, err)
	}
	other, _ := s.Load("owner/project/session/b")
	if other.Generation != 0 {
		t.Fatal("branch state leaked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	unlock, _ = s.Acquire(ctx, "a")
	cancel()
	if _, err := s.Acquire(ctx, "a"); err == nil {
		t.Fatal("cancelled wait acquired locked branch")
	}
	unlock()
}

func TestSummaryRejectsInventedSources(t *testing.T) {
	valid := []byte(`{"version":"context.summary/v1","goal":"continue","constraints":[],"decisions":[],"attempts":[],"hypotheses":[],"open_questions":[],"next_steps":[],"artifact_refs":[],"facts":[{"text":"observed","source_ids":["source-1"]}]}`)
	if err := ValidateSummary(valid, map[string]bool{"source-1": true}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSummary(valid, map[string]bool{"source-2": true}); err == nil {
		t.Fatal("invented reference accepted")
	}
}

func TestUnknownCompactionCostCannotBypassFiniteLimit(t *testing.T) {
	ctx := WithLimits(context.Background(), Limits{CostConfigured: true, CostUSD: 1})
	if CheckAllowance(ctx, 100, nil) == nil {
		t.Fatal("unknown native price bypassed finite allowance")
	}
	cost := .5
	if err := CheckAllowance(ctx, 100, &cost); err != nil {
		t.Fatal(err)
	}
	ctx = WithLimits(ctx, Limits{TokensConfigured: true, Tokens: 99})
	if CheckAllowance(ctx, 100, &cost) == nil {
		t.Fatal("token cap bypassed")
	}
}
