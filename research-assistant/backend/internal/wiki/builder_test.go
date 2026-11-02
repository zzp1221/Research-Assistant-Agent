package wiki

import (
	"context"
	"testing"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

func TestRefreshForPlanCreatesPaperPage(t *testing.T) {
	ctx := context.Background()
	mem := memory.NewMemoryStore()
	store := NewMemoryPaperStore()
	builder := NewBuilder(store, mem, nil, nil, NewAliasStore(t.TempDir()))
	plan := &models.PlanGraph{
		ID: "plan-1", OwnerID: "u1", ProjectID: "proj",
		Context: &models.ResearchContext{Paper: &models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762"}},
		Outcome: models.OutcomeFailed, OutcomeReason: "torch version conflict",
	}
	if err := builder.RefreshForPlan(ctx, plan); err != nil {
		t.Fatalf("refresh plan: %v", err)
	}
	pages, total, err := store.List(ctx, PaperQuery{OwnerID: "u1"})
	if err != nil || total != 1 || len(pages) != 1 {
		t.Fatalf("expected plan paper page, total=%d err=%v", total, err)
	}
	page := pages[0]
	if page.Reproduction.Status != "failed" || page.Reproduction.LastPlanID != "plan-1" {
		t.Fatalf("reproduction=%+v", page.Reproduction)
	}
	if page.Reproduction.LastError == "" {
		t.Fatalf("expected last error")
	}
}
