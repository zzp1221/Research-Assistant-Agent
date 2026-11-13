package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestMMRCalibratesRelevanceAndProtectsDifferentEvidence(t *testing.T) {
	candidates := []ScoredRecord{
		{Record: Record{ID: "a", Kind: KindFact, Content: "training batch memory pressure on the gpu"}, Score: .02},
		{Record: Record{ID: "b", Kind: KindFact, Content: "training batch memory pressure on the gpu during evaluation"}, Score: .019},
		{Record: Record{ID: "noise", Kind: KindFact, Content: "unrelated optimizer discussion"}, Score: .001},
	}
	selected := mmrSelect(candidates, 2, .7, map[Kind]int{KindFact: 6})
	if len(selected) != 2 || selected[1].Record.ID != "b" || selected[0].Relevance != 1 {
		t.Fatalf("redundancy overwhelms relevance: %#v", selected)
	}
	a := Record{Content: "B23 macro-F1=0.58, batch=16"}
	b := Record{Content: "B23 macro-F1=0.85, batch=16"}
	if redundancy(a, b) != 0 {
		t.Fatal("different experimental values penalized as redundant")
	}
	b.Content = "纠正：B23 macro-F1=0.58, batch=16"
	if redundancy(a, b) != 0 {
		t.Fatal("correction penalized as redundant")
	}
}

func TestBilingualRecallAndFailureAttribution(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	q := Query{OwnerID: "owner", ProjectID: "p", Text: "out of memory"}
	got, _ := s.Retrieve(ctx, q, 0, 1000)
	if got.Report.FailureStage != "no_records" {
		t.Fatal(got.Report)
	}
	rec := testRecord("owner", "显存不足的负结果", "实验显存不足，缩小批大小后仍失败。")
	rec.Scope.ProjectID = "other"
	_, _ = s.Put(ctx, rec)
	got, _ = s.Retrieve(ctx, q, 0, 1000)
	if got.Report.FailureStage != "not_visible" {
		t.Fatal(got.Report)
	}
	rec.Scope.ProjectID = "p"
	_, _ = s.Put(ctx, rec)
	got, _ = s.Retrieve(ctx, q, 0, 1000)
	if len(got.Retrieved) != 1 || !strings.Contains(got.RetrievedText, "失败") || got.Report.MatchedCandidates != 1 {
		t.Fatal(got.Report)
	}
	got, _ = s.Retrieve(ctx, q, 0, 0)
	if got.Report.FailureStage != "budget_excluded" || got.Report.BudgetExcluded != 1 {
		t.Fatal(got.Report)
	}
	q.Text = "XYZZY_UNANSWERABLE"
	got, _ = s.Retrieve(ctx, q, 0, 1000)
	if got.Report.FailureStage != "not_recalled" {
		t.Fatal(got.Report)
	}
	q.OwnerID = "other-owner"
	hits, _ := s.Search(ctx, q)
	if len(hits) != 0 {
		t.Fatal("owner boundary bypassed")
	}
}

func TestSharedContextBudgetCapsWikiAndUsesUpdatedSettings(t *testing.T) {
	settings := DefaultSettings()
	settings.RetrievedBudgetTokens = 600
	b := ResolveContextBudget(settings, 2000, 1000)
	if b.Core != 512 || b.Retrieved != 488 {
		t.Fatal(b)
	}
	b.WikiTokens = 488
	if b.Remaining() != 0 {
		t.Fatal("wiki and memory oversubscribed")
	}
	settings.RetrievedBudgetTokens = 100
	if next := ResolveContextBudget(settings, 2000, 1000); next.Retrieved != 100 {
		t.Fatal(next)
	}
}

func TestReflectionKeepsConditionsDeduplicatesExecutionsAndScope(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	for i := 0; i < 5; i++ {
		rec := testRecord("owner", fmt.Sprintf("case-%d", i), fmt.Sprintf("batch=16 case %d", i))
		rec.Kind = KindEpisode
		rec.Scope = Scope{OwnerID: "owner", ProjectID: "p", SessionID: "private-session"}
		rec.Attributes = map[string]any{"problem": "OOM", "action": "reduce batch", "environment_hash": "gpu-small", "protocol_hash": "test-v1", "outcome": "success", "execution_id": "same-execution"}
		rec.Refs = []SourceRef{{ArtifactRef: "artifact:verified-log"}}
		if i == 0 {
			rec.Kind = KindNegative
			rec.Attributes["environment_hash"] = "gpu-tiny"
			rec.Attributes["outcome"] = "failed"
		}
		if _, err := s.Put(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}
	c := NewConsolidator(s)
	if report, err := c.RunOnce(ctx, "owner"); err != nil || report.Reflections != 1 {
		t.Fatal(report, err)
	}
	reflections, _, _ := s.List(ctx, Query{OwnerID: "owner", Kinds: []Kind{KindReflection}, Statuses: []Status{StatusPending}})
	if len(reflections) != 1 {
		t.Fatal(reflections)
	}
	r := reflections[0]
	evidence := r.Attributes["reflection_evidence"].(map[string]any)
	if evidence["independent_executions"] != float64(1) || evidence["generalization"] != "not_established" || r.Scope.SessionID != "private-session" || !strings.Contains(r.Content, "gpu-tiny") || !strings.Contains(r.Content, "negative") {
		t.Fatal(r)
	}
	if report, err := c.RunOnce(ctx, "owner"); err != nil || report.Reflections != 0 {
		t.Fatal("replay changed evidence", report, err)
	}
	hits, _ := s.Search(ctx, Query{OwnerID: "owner", ProjectID: "p", SessionID: "other", Text: "OOM", IncludePending: true})
	if len(hits) != 0 {
		t.Fatal("reflection widened source visibility")
	}
}

func TestReflectionReadsVerifiedActionWithoutPromotingMethod(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryStore()
	plan, task, action := repairFixture()
	if err := RecordRepairProcedures(ctx, s, plan, task, []models.ActionLedgerEntry{action}); err != nil {
		t.Fatal(err)
	}
	records, _, _ := s.List(ctx, Query{OwnerID: plan.OwnerID, IncludePending: true})
	if len(records) != 1 {
		t.Fatal(records)
	}
	entry := reflectionCase(records[0])
	if entry.Action != action.Action || entry.Environment != action.EnvironmentHash || entry.IndependentExecution != action.ExecutionID || entry.Outcome != "verified_single_case" || records[0].Status != StatusPending {
		t.Fatal(entry)
	}
}
