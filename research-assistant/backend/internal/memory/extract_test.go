package memory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

func TestRecordPlanExtractsEpisodeFactAndNegative(t *testing.T) {
	store := NewMemoryStore()
	plan := &models.PlanGraph{
		ID: "p-1", OwnerID: "u1", SessionID: "s1", UserIntent: "复现 Attention 论文", IntentType: "Paper_Reproduction",
		Title: "Attention 复现", Status: models.StatusFailed, Outcome: models.OutcomeFailed, OutcomeReason: "依赖安装失败",
		Nodes:   []*models.TaskNode{{ID: "t1", Name: "install", Status: models.StatusFailed, Error: "ModuleNotFoundError: torch"}},
		Context: &models.ResearchContext{Paper: &models.PaperMetadata{Title: "Attention Is All You Need", Authors: []string{"Vaswani"}, Year: 2017, ArxivID: "1706.03762"}},
		Usage:   models.RunUsage{LLMPromptTokens: 100, LLMCompletionTokens: 20, LLMCalls: 1},
	}
	if err := RecordPlan(context.Background(), store, plan); err != nil {
		t.Fatal(err)
	}
	records, total, err := store.List(context.Background(), Query{OwnerID: "u1", Statuses: []Status{StatusActive}, Limit: 50})
	if err != nil || total < 3 {
		t.Fatalf("expected >=3 records, got %d err=%v", total, err)
	}
	kinds := map[Kind]bool{}
	for _, rec := range records {
		kinds[rec.Kind] = true
	}
	for _, want := range []Kind{KindEpisode, KindFact, KindNegative} {
		if !kinds[want] {
			t.Fatalf("missing kind %s in %#v", want, kinds)
		}
	}
}

func TestRecordTrialWritesNegativeAndProcedure(t *testing.T) {
	store := NewMemoryStore()
	plan := &models.PlanGraph{ID: "p-2", OwnerID: "u1", UserIntent: "tune", IntentType: "AutoResearch"}
	metric1 := 0.5
	metric2 := 0.7
	now := time.Now().UTC()
	ledger := models.ResearchTrialLedger{
		MetricKey: "score", Direction: "maximize", BaselineScore: 0.4, BestScore: 0.7, SearchRuns: 2, CompletedTrials: 2, AcceptedTrials: 1,
		Trials: []models.ResearchTrial{
			{Number: 1, Status: "rejected", Decision: "reject", Hypothesis: "try A", Reason: "no improvement", Metric: &metric1, FinishedAt: now},
			{Number: 2, Status: "kept", Decision: "keep", Hypothesis: "try B", Reason: "improved", Metric: &metric2, FinishedAt: now},
		},
	}
	if err := RecordTrial(context.Background(), store, plan, ledger); err != nil {
		t.Fatal(err)
	}
	records, _, err := store.List(context.Background(), Query{OwnerID: "u1", Statuses: []Status{StatusActive}, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[Kind]bool{}
	for _, rec := range records {
		kinds[rec.Kind] = true
	}
	if !kinds[KindNegative] || !kinds[KindProcedure] || !kinds[KindFact] {
		t.Fatalf("missing trial memory kinds: %#v", kinds)
	}
}

func TestConsolidatorCreatesReflectionAndArchives(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	old := time.Now().Add(-400 * 24 * time.Hour).UTC()
	for i := 0; i < 5; i++ {
		rec := testRecord("u1", fmt.Sprintf("episode-%d", i), fmt.Sprintf("task failed with torch missing %d", i))
		rec.Kind = KindEpisode
		saved, err := store.Put(ctx, rec)
		if err != nil {
			t.Fatal(err)
		}
		saved.UpdatedAt = old
		saved.CreatedAt = old
		saved.Validity.ValidFrom = &old
		store.records[saved.ID] = saved
	}
	report, err := NewConsolidator(store).RunOnce(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if report.Archived == 0 {
		t.Fatalf("expected archived records, got %#v", report)
	}
	if report.Reflections == 0 {
		t.Fatalf("expected reflection, got %#v", report)
	}
}
