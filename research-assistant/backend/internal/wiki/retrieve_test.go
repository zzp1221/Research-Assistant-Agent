package wiki

import (
	"strings"
	"testing"

	"research-assistant-backend/internal/memory"
)

func TestBudgetTokens(t *testing.T) {
	cases := []struct {
		limit int
		want  int
	}{
		{100000, 1500},
		{50000, 1000},
		{8000, 240},
		{4000, 0},
		{0, 0},
	}
	for _, testCase := range cases {
		if got := BudgetTokens(testCase.limit); got != testCase.want {
			t.Fatalf("BudgetTokens(%d)=%d want %d", testCase.limit, got, testCase.want)
		}
	}
}

func TestRenderContext(t *testing.T) {
	page := PaperPage{PaperID: "paper-a", OwnerID: "u1", Title: "Attention", TLDR: "Transformer 用纯注意力替代 RNN。"}
	point := Point{PaperID: "paper-a", Kind: PointResult, Record: memory.Record{ID: "point-1", Title: "BLEU", Content: "WMT14 EN-DE BLEU 27.3", Trust: memory.TrustVerified, Status: memory.StatusActive, Kind: memory.KindFact}, Refs: []memory.SourceRef{{Page: 3, Section: "4.1"}}}
	context := RenderContext([]PaperContext{{Paper: page, Points: []Point{point}}}, 1500)
	if context.Text == "" || !strings.Contains(context.Text, "<wiki_paper") || !strings.Contains(context.Text, "<wiki_point") || !strings.Contains(context.Text, "</wiki_paper>") {
		t.Fatalf("unexpected wiki text: %q", context.Text)
	}
	if context.Tokens <= 0 || len(context.Citations) < 2 || context.Citations[0].PaperID != "paper-a" || context.Citations[0].MemoryID != "paper:paper-a" {
		t.Fatalf("citations=%+v tokens=%d", context.Citations, context.Tokens)
	}
	if got := RenderContext([]PaperContext{{Paper: page}}, 0); got.Text != "" {
		t.Fatalf("zero budget must not inject")
	}
}

func TestInjectablePointRules(t *testing.T) {
	active := Point{Record: memory.Record{Status: memory.StatusActive, Trust: memory.TrustVerified}}
	if !injectablePoint(active) {
		t.Fatalf("active verified point should be injectable")
	}
	pending := Point{Record: memory.Record{Status: memory.StatusPending, Trust: memory.TrustInferred}}
	if injectablePoint(pending) {
		t.Fatalf("pending point must not be injectable")
	}
	untrusted := Point{Record: memory.Record{Status: memory.StatusActive, Trust: memory.TrustUntrusted}}
	if injectablePoint(untrusted) {
		t.Fatalf("untrusted point must not be injectable")
	}
}
func TestRenderContextNeverExceedsBudget(t *testing.T) {
	page := PaperPage{PaperID: "paper-big", OwnerID: "u1", Title: "Big Paper", TLDR: strings.Repeat("很长的摘要内容，", 500)}
	point := Point{PaperID: "paper-big", Kind: PointClaim, Record: memory.Record{ID: "point-big", Title: "长结论", Content: strings.Repeat("这是很长的一段结论。", 200), Trust: memory.TrustVerified, Status: memory.StatusActive, Kind: memory.KindFact}}
	context := RenderContext([]PaperContext{{Paper: page, Points: []Point{point}}}, 200)
	if context.Tokens > 200 {
		t.Fatalf("wiki context tokens=%d exceeds budget 200", context.Tokens)
	}
	if context.Text == "" {
		t.Fatalf("expected truncated context, got empty")
	}
}
