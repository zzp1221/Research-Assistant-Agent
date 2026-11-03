package wiki

import (
	"testing"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/memory"
)

func TestMatchReferenceEntryPriority(t *testing.T) {
	pages := []PaperPage{
		{PaperID: "paper-a", Title: "Attention Is All You Need", Year: 2017, DOI: "10.48550/arxiv.1706.03762", ArxivID: "1706.03762"},
		{PaperID: "paper-b", Title: "BERT: Pre-training of Deep Bidirectional Transformers", Year: 2019},
	}
	byDOI := documents.ReferenceEntry{DOI: "https://doi.org/10.48550/arXiv.1706.03762"}
	if page, ok := matchReferenceEntry(byDOI, "paper-self", pages); !ok || page.PaperID != "paper-a" {
		t.Fatalf("doi match=%+v ok=%v", page, ok)
	}
	byArxiv := documents.ReferenceEntry{ArxivID: "1706.03762v5"}
	if page, ok := matchReferenceEntry(byArxiv, "paper-self", pages); !ok || page.PaperID != "paper-a" {
		t.Fatalf("arxiv match=%+v ok=%v", page, ok)
	}
	byTitle := documents.ReferenceEntry{Title: "BERT: Pre-training of Deep Bidirectional Transformers", Year: 2020}
	if page, ok := matchReferenceEntry(byTitle, "paper-self", pages); !ok || page.PaperID != "paper-b" {
		t.Fatalf("title match=%+v ok=%v", page, ok)
	}
	// Year gap > 1 must not merge distinct editions.
	if _, ok := matchReferenceEntry(documents.ReferenceEntry{Title: "BERT: Pre-training of Deep Bidirectional Transformers", Year: 2010}, "paper-self", pages); ok {
		t.Fatalf("year gap should not match")
	}
	// Self citations are ignored.
	if _, ok := matchReferenceEntry(documents.ReferenceEntry{DOI: "10.48550/arxiv.1706.03762"}, "paper-a", pages); ok {
		t.Fatalf("self citation must be ignored")
	}
}

func TestReferenceCandidatesDedupeAndEvidence(t *testing.T) {
	builder := NewBuilder(NewMemoryPaperStore(), memory.NewMemoryStore(), nil, nil, NewAliasStore(t.TempDir()))
	page := PaperPage{PaperID: "paper-self", Title: "Self Paper"}
	pages := []PaperPage{
		page,
		{PaperID: "paper-a", Title: "Attention Is All You Need", Year: 2017, DOI: "10.48550/arxiv.1706.03762"},
	}
	entries := []documents.ReferenceEntry{
		{Raw: "[1] Vaswani. Attention Is All You Need. NeurIPS, 2017. arXiv:1706.03762.", Page: 5, ArxivID: "1706.03762"},
		{Raw: "[2] Vaswani. Attention Is All You Need. NeurIPS, 2017.", Page: 5, Title: "Attention Is All You Need", Year: 2017},
		{Raw: "[3] Unknown. Other Paper. Conf, 2020.", Page: 6, Title: "Other Paper", Year: 2020},
	}
	candidates := builder.referenceCandidates(page, pages, "doc-refs", entries)
	if len(candidates) != 1 {
		t.Fatalf("candidates=%d want 1: %+v", len(candidates), candidates)
	}
	candidate := candidates[0]
	if candidate.ToPaper != "paper-a" || candidate.Type != LinkCites || candidate.Source != "pdf_references" {
		t.Fatalf("unexpected candidate: %+v", candidate)
	}
	if len(candidate.Evidence) != 1 || candidate.Evidence[0].DocumentID != "doc-refs" || candidate.Evidence[0].Page != 5 {
		t.Fatalf("unexpected evidence: %+v", candidate.Evidence)
	}
	if len([]rune(candidate.Evidence[0].Quote)) > 500 {
		t.Fatalf("quote exceeds 500 runes")
	}
}

func TestPDFRefsSwitchDefaultOff(t *testing.T) {
	t.Setenv("WIKI_PDF_REFS", "")
	if PDFRefsEnabled() {
		t.Fatalf("WIKI_PDF_REFS must default to false")
	}
	t.Setenv("WIKI_PDF_REFS", "true")
	if !PDFRefsEnabled() {
		t.Fatalf("WIKI_PDF_REFS=true must enable parsing")
	}
}
