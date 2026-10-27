package documents

import (
	"context"
	"math"
	"strings"
	"testing"

	uploadspkg "research-assistant-backend/internal/uploads"
)

func bm25TestDocument(texts ...string) *Document {
	pages := make([]Page, 0, len(texts))
	characters := 0
	for index, text := range texts {
		chars := len([]rune(text))
		characters += chars
		pages = append(pages, Page{Number: index + 1, Text: text, Chars: chars})
	}
	return &Document{
		Version:    Version,
		UploadID:   "bm25-test-upload",
		Status:     StatusExtracted,
		PageCount:  len(pages),
		Characters: characters,
		Pages:      pages,
	}
}

func TestRankDocumentBM25IDFPrefersRareTerm(t *testing.T) {
	document := bm25TestDocument(
		"common alpha vocabulary",
		"common beta vocabulary",
		"common gamma vocabulary",
		"common delta vocabulary",
		"common rare sigma vocabulary",
		"common epsilon vocabulary",
	)
	results := RankDocument(document, "common rare", SearchOptions{TopK: 6})
	if len(results) == 0 {
		t.Fatal("expected BM25 matches")
	}
	if results[0].Page != 5 {
		t.Fatalf("top page=%d, want 5: %#v", results[0].Page, results)
	}
	if results[0].Score <= results[1].Score {
		t.Fatalf("rare-term chunk score=%f, common-only score=%f", results[0].Score, results[1].Score)
	}
	if results[0].Score <= 0 {
		t.Fatalf("score must be positive: %f", results[0].Score)
	}
}

func TestRankDocumentBM25LengthNormalization(t *testing.T) {
	filler := strings.Repeat("filler ", 90)
	document := bm25TestDocument(
		"normalization",
		filler+"normalization",
		filler+"unrelated",
	)
	results := RankDocument(document, "normalization", SearchOptions{TopK: 3})
	if len(results) < 2 {
		t.Fatalf("expected two matching chunks, got %#v", results)
	}
	if results[0].Page != 1 {
		t.Fatalf("short chunk should win, top page=%d: %#v", results[0].Page, results)
	}
	if results[0].Score <= results[1].Score {
		t.Fatalf("short chunk score=%f, long chunk score=%f", results[0].Score, results[1].Score)
	}
}

func TestRankDocumentBM25RespectsTopK(t *testing.T) {
	texts := make([]string, 0, 12)
	for index := 0; index < 12; index++ {
		texts = append(texts, "needle document text")
	}
	document := bm25TestDocument(texts...)
	if results := RankDocument(document, "needle", SearchOptions{TopK: 3}); len(results) != 3 {
		t.Fatalf("top_k=3 returned %d results", len(results))
	}
	if results := RankDocument(document, "needle", SearchOptions{}); len(results) != defaultSearchTopK {
		t.Fatalf("default top_k returned %d results, want %d", len(results), defaultSearchTopK)
	}
	// TopK is clamped to maxSearchTopK; with only 12 chunks the result set is
	// naturally smaller than the clamp.
	if results := RankDocument(document, "needle", SearchOptions{TopK: 100}); len(results) != len(texts) {
		t.Fatalf("clamped top_k returned %d results, want %d", len(results), len(texts))
	}
}

func TestRankDocumentBM25ChineseBigramQuery(t *testing.T) {
	document := bm25TestDocument(
		"本文提出一种新的实验方法并给出结果",
		"本章介绍无关的背景材料",
		"实验结果表明该方法有效",
	)
	results := RankDocument(document, "实验方法", SearchOptions{TopK: 3})
	if len(results) == 0 {
		t.Fatal("expected Chinese bigram matches")
	}
	if results[0].Page != 1 {
		t.Fatalf("top page=%d, want 1: %#v", results[0].Page, results)
	}
	for _, want := range []string{"实验", "方法", "验方"} {
		found := false
		for _, token := range queryTokens("实验方法") {
			if token == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("queryTokens missing Chinese bigram %q: %#v", want, queryTokens("实验方法"))
		}
	}
}

func TestRankDocumentBM25SectionAndPageFilters(t *testing.T) {
	document := bm25TestDocument(
		"1. Introduction\nsignal about retrieval",
		"2. Method\nsignal about ranking",
		"3. Experiments\nsignal about evaluation",
	)
	bySection := RankDocument(document, "signal", SearchOptions{TopK: 5, Section: "method"})
	if len(bySection) != 1 || bySection[0].Page != 2 {
		t.Fatalf("section filter returned %#v", bySection)
	}
	byPage := RankDocument(document, "signal", SearchOptions{TopK: 5, Page: 3})
	if len(byPage) != 1 || byPage[0].Page != 3 {
		t.Fatalf("page filter returned %#v", byPage)
	}
	if missing := RankDocument(document, "signal", SearchOptions{TopK: 5, Section: "references"}); len(missing) != 0 {
		t.Fatalf("unknown section should return nothing: %#v", missing)
	}

	base := RankDocument(document, "signal", SearchOptions{TopK: 5, Section: "Method"})
	boosted := RankDocument(document, "signal", SearchOptions{TopK: 5, Section: "Method", SectionBoost: 2})
	if len(base) != 1 || len(boosted) != 1 {
		t.Fatalf("boost comparison returned %#v / %#v", base, boosted)
	}
	if math.Abs(boosted[0].Score-2*base[0].Score) > 1e-9 {
		t.Fatalf("section boost score=%f, want %f", boosted[0].Score, 2*base[0].Score)
	}
}

func TestRankDocumentBM25DeterministicTieOrder(t *testing.T) {
	document := bm25TestDocument("alpha", "alpha", "alpha")
	results := RankDocument(document, "alpha", SearchOptions{TopK: 3})
	if len(results) != 3 {
		t.Fatalf("results=%#v", results)
	}
	for index, expectedPage := range []int{1, 2, 3} {
		if results[index].Page != expectedPage {
			t.Fatalf("tie order=%v, want pages 1,2,3", []int{results[0].Page, results[1].Page, results[2].Page})
		}
	}
}

func TestSearchDelegatesToBM25SearchWithOptions(t *testing.T) {
	root := t.TempDir()
	t.Setenv("UPLOAD_ROOT", root)
	document := bm25TestDocument("needle first", "other text", "needle third")
	document.UploadID = "upload-bm25"
	directory := uploadspkg.Directory("owner-bm25", document.UploadID)
	if err := Save(directory, document); err != nil {
		t.Fatal(err)
	}

	store := NewStore("")
	legacy, err := store.Search(context.Background(), "owner-bm25", document.UploadID, "needle", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy) != 2 || legacy[0].Page != 1 || legacy[1].Page != 3 {
		t.Fatalf("legacy search returned %#v", legacy)
	}
	options, err := store.SearchWithOptions(context.Background(), "owner-bm25", document.UploadID, "needle", SearchOptions{TopK: 5, Page: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(options) != 1 || options[0].Page != 3 {
		t.Fatalf("option search returned %#v", options)
	}
	sections := Outline(document)
	if len(sections) != 0 {
		t.Fatalf("document without headings should have no outline: %#v", sections)
	}
}
