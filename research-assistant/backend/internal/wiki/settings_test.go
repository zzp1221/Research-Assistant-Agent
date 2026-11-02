package wiki

import "testing"

func TestSettingsRollbackSwitches(t *testing.T) {
	t.Setenv("WIKI_ENABLED", "false")
	if Enabled() {
		t.Fatalf("WIKI_ENABLED=false must disable the wiki")
	}
	t.Setenv("WIKI_ENABLED", "true")
	t.Setenv("WIKI_MODE", "shadow")
	if !Enabled() {
		t.Fatalf("enabled expected")
	}
	if InjectionEnabled() {
		t.Fatalf("shadow mode must not inject")
	}
	t.Setenv("WIKI_MODE", "on")
	if !InjectionEnabled() {
		t.Fatalf("on mode must inject")
	}
	t.Setenv("WIKI_LLM_MODE", "all")
	if LLMMode() != "all" {
		t.Fatalf("llm mode=%q", LLMMode())
	}
	t.Setenv("WIKI_LLM_MODE", "bogus")
	if LLMMode() != "summary" {
		t.Fatalf("default llm mode=%q", LLMMode())
	}
	t.Setenv("WIKI_LINK_MODE", "hybrid")
	if LinkMode() != "hybrid" {
		t.Fatalf("link mode=%q", LinkMode())
	}
	t.Setenv("WIKI_BUDGET_TOKENS", "900")
	if BudgetOverride() != 900 {
		t.Fatalf("budget override=%d", BudgetOverride())
	}
}
