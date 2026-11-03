package wiki

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

type fakeLLM struct {
	responses []string
	calls     int
	lastUser  string
	err       error
}

func (f *fakeLLM) Complete(ctx context.Context, ownerID, system, user string) (string, error) {
	f.calls++
	f.lastUser = user
	if f.err != nil {
		return "", f.err
	}
	if len(f.responses) == 0 {
		return "", fmt.Errorf("no fake response")
	}
	response := f.responses[0]
	f.responses = f.responses[1:]
	return response, nil
}

func newSummaryFixture(t *testing.T) (*Builder, *memory.FileStore, *PaperPage) {
	t.Helper()
	mem := memory.NewMemoryStore()
	builder := NewBuilder(NewMemoryPaperStore(), mem, nil, nil, NewAliasStore(t.TempDir()))
	page, err := builder.EnsurePaper(context.Background(), "u1", "proj", models.PaperMetadata{
		Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762",
		Abstract: "The Transformer replaces recurrence with self-attention and reaches 27.3 BLEU on WMT14 EN-DE.",
	}, "")
	if err != nil {
		t.Fatalf("ensure paper: %v", err)
	}
	return builder, mem, &page
}

func summaryDocument() *documents.Document {
	return &documents.Document{Pages: []documents.Page{{
		Number: 1,
		Text:   "We use a transformer architecture with multi-head self-attention. A limitation is quadratic cost on long sequences. Future work should study low-resource settings.",
	}}}
}

func TestMaybeSummarizePaperCreatesTLDRAndVerifiedPoints(t *testing.T) {
	t.Setenv("WIKI_LLM_MODE", "summary")
	builder, mem, page := newSummaryFixture(t)
	fake := &fakeLLM{responses: []string{`{
	  "tldr": "Transformer 用自注意力替代循环结构，在 WMT14 达到 27.3 BLEU。",
	  "points": [
	    {"kind":"method","title":"自注意力","text":"使用多头自注意力。","section":"3","quote":"multi-head self-attention"},
	    {"kind":"limitation","title":"长序列成本","text":"长序列上二次复杂度。","section":"4","quote":"quadratic cost on long sequences"},
	    {"kind":"open_question","title":"低资源","text":"低资源场景待研究。","section":"5","quote":"this quote is not in the document"}
	  ]
	}`}}
	builder.LLM = fake
	if err := builder.maybeSummarizePaper(context.Background(), "u1", page, "doc-1", summaryDocument()); err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if !strings.Contains(page.TLDR, "27.3") || !page.TLDRInferred {
		t.Fatalf("unexpected tldr: %+v", page)
	}
	if fake.calls != 1 {
		t.Fatalf("llm calls=%d", fake.calls)
	}
	points, total, err := builder.ListPoints(context.Background(), "u1", page.PaperID, "", 50, 0)
	if err != nil || total != 3 {
		t.Fatalf("points total=%d err=%v", total, err)
	}
	verified := 0
	unverified := 0
	for _, point := range points {
		if len(point.Refs) == 0 {
			t.Fatalf("point missing ref: %+v", point)
		}
		if point.Refs[0].Quote != "" && point.Refs[0].Page == 1 {
			verified++
		} else {
			unverified++
			if point.Record.Status != memory.StatusPending {
				t.Fatalf("unverified quote must stay pending: %+v", point.Record)
			}
		}
	}
	if verified != 2 || unverified != 1 {
		t.Fatalf("quote locate rate verified=%d unverified=%d", verified, unverified)
	}
	if tldr, ok := builder.loadTLDR(context.Background(), "u1", page.PaperID); !ok || !strings.Contains(tldr, "27.3") {
		t.Fatalf("tldr not persisted to memory: %q ok=%v", tldr, ok)
	}
	if _, err := mem.Get(context.Background(), "u1", "mem-wiki-tldr-"+page.PaperID); err != nil {
		t.Fatalf("wiki_tldr record missing: %v", err)
	}
}

func TestMaybeSummarizePaperRejectsInvalidJSON(t *testing.T) {
	t.Setenv("WIKI_LLM_MODE", "summary")
	builder, _, page := newSummaryFixture(t)
	builder.LLM = &fakeLLM{responses: []string{"not json", "still not json"}}
	if err := builder.maybeSummarizePaper(context.Background(), "u1", page, "doc-1", summaryDocument()); err == nil {
		t.Fatalf("invalid JSON must fail after two attempts")
	}
	if page.TLDR != "" {
		t.Fatalf("invalid JSON must not set tldr")
	}
	if builder.LLM.(*fakeLLM).calls != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", builder.LLM.(*fakeLLM).calls)
	}
}

func TestSummaryModeOffDoesNotCallLLM(t *testing.T) {
	t.Setenv("WIKI_LLM_MODE", "off")
	builder, _, page := newSummaryFixture(t)
	fake := &fakeLLM{responses: []string{`{"tldr":"x"}`}}
	builder.LLM = fake
	if err := builder.maybeSummarizePaper(context.Background(), "u1", page, "doc-1", summaryDocument()); err != nil {
		t.Fatalf("off mode: %v", err)
	}
	if fake.calls != 0 || page.TLDR != "" {
		t.Fatalf("off mode must not call LLM: calls=%d tldr=%q", fake.calls, page.TLDR)
	}
}

func TestEnsurePaperRestoresTLDRWithoutCallingLLM(t *testing.T) {
	t.Setenv("WIKI_LLM_MODE", "summary")
	ctx := context.Background()
	mem := memory.NewMemoryStore()
	aliasDir := t.TempDir()
	first := NewBuilder(NewMemoryPaperStore(), mem, nil, nil, NewAliasStore(aliasDir))
	fake := &fakeLLM{responses: []string{`{"tldr":"缓存的 TLDR","points":[]}`}}
	first.LLM = fake
	meta := models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762", Abstract: "self-attention transformer"}
	page, err := first.EnsurePaper(ctx, "u1", "proj", meta, "")
	if err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	if fake.calls != 1 || page.TLDR == "" {
		t.Fatalf("first ensure should generate: calls=%d tldr=%q", fake.calls, page.TLDR)
	}
	// Simulate deleting data/wiki: empty store, same memory/alias.
	second := NewBuilder(NewMemoryPaperStore(), mem, nil, nil, NewAliasStore(aliasDir))
	fake2 := &fakeLLM{responses: []string{`{"tldr":"不应调用"}`}}
	second.LLM = fake2
	rebuilt, err := second.EnsurePaper(ctx, "u1", "proj", meta, "")
	if err != nil {
		t.Fatalf("rebuild ensure: %v", err)
	}
	if fake2.calls != 0 {
		t.Fatalf("rebuild must not call LLM, calls=%d", fake2.calls)
	}
	if rebuilt.TLDR != "缓存的 TLDR" {
		t.Fatalf("rebuilt tldr=%q", rebuilt.TLDR)
	}
}

func TestAdjudicateLink(t *testing.T) {
	t.Setenv("WIKI_LLM_MODE", "links")
	builder, _, page := newSummaryFixture(t)
	other := PaperPage{PaperID: "paper-b", OwnerID: "u1", Title: "BERT", Abstract: "A bidirectional transformer pretrained on unlabeled text."}
	fake := &fakeLLM{responses: []string{`{"type":"contradicts","confidence":0.82,"evidence_quote":"multi-head self-attention"}`}}
	builder.LLM = fake
	candidate, ok := builder.adjudicateLink(context.Background(), "u1", *page, other)
	if !ok {
		t.Fatalf("adjudication failed")
	}
	if candidate.Type != LinkContradicts || candidate.Source != "llm" || candidate.Confidence != 0.82 {
		t.Fatalf("unexpected candidate: %+v", candidate)
	}
	// Invalid type is rejected.
	builder.LLM = &fakeLLM{responses: []string{`{"type":"not_a_link","confidence":0.5}`}}
	if _, ok := builder.adjudicateLink(context.Background(), "u1", *page, other); ok {
		t.Fatalf("invalid link type must be rejected")
	}
}

func TestTextSimilarityCandidates(t *testing.T) {
	builder := NewBuilder(NewMemoryPaperStore(), memory.NewMemoryStore(), nil, nil, NewAliasStore(t.TempDir()))
	page := PaperPage{PaperID: "paper-a", Title: "Transformer for machine translation", Abstract: "We study self-attention transformer models for machine translation on WMT14."}
	other := PaperPage{PaperID: "paper-b", Title: "Transformer models for translation", Abstract: "Self-attention transformer models improve machine translation on WMT14 benchmark."}
	candidates := builder.textSimilarityCandidates(page, []PaperPage{page, other})
	if len(candidates) != 1 || candidates[0].ToPaper != "paper-b" || candidates[0].Type != LinkRelated {
		t.Fatalf("unexpected candidates: %+v", candidates)
	}
}
