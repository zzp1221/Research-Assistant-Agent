package agent

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"research-assistant-backend/internal/documents"
	uploadspkg "research-assistant-backend/internal/uploads"
)

func librarianSectionedDocument() *documents.Document {
	texts := []string{
		"1. Introduction\nThis paper studies retrieval systems and motivates the problem.",
		"1. Introduction\nBackground material continues here with more motivation.",
		"2. Method\nWe propose a novel ranking method using BM25.",
		"2. Method\nImplementation details of the method are described.",
		"3. Experiments\nWe experiment on five datasets and report results.",
		"4. Results\nResults show improvements over the baseline.",
		"5. Discussion\nWe discuss limitations of the approach.",
		"6. Conclusion\nWe conclude that retrieval quality improves.",
	}
	pages := make([]documents.Page, 0, len(texts))
	characters := 0
	for index, text := range texts {
		chars := len([]rune(text))
		characters += chars
		pages = append(pages, documents.Page{Number: index + 1, Text: text, Chars: chars})
	}
	return &documents.Document{
		Version:    documents.Version,
		UploadID:   "librarian-test",
		Status:     documents.StatusExtracted,
		PageCount:  len(pages),
		Characters: characters,
		Pages:      pages,
	}
}

func librarianPlainDocument() *documents.Document {
	texts := []string{
		"A plain page discussing retrieval quality.",
		"A plain page about method design and results.",
		"A plain page reporting experimental results.",
	}
	pages := make([]documents.Page, 0, len(texts))
	for index, text := range texts {
		pages = append(pages, documents.Page{Number: index + 1, Text: text, Chars: len([]rune(text))})
	}
	return &documents.Document{
		Version:   documents.Version,
		UploadID:  "librarian-plain",
		Status:    documents.StatusExtracted,
		PageCount: len(pages),
		Pages:     pages,
	}
}

func librarianTestStore(t *testing.T, document *documents.Document) *documents.Store {
	t.Helper()
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	directory := uploadspkg.Directory("owner-librarian", document.UploadID)
	if err := documents.Save(directory, document); err != nil {
		t.Fatal(err)
	}
	return documents.NewStore("")
}

func TestLibrarianTwoStageRetrievalUsesSections(t *testing.T) {
	document := librarianSectionedDocument()
	store := librarianTestStore(t, document)
	agent := &LibrarianAgent{Name: "librarian_agent", Documents: store}

	retrieval := agent.retrieveDocumentContext(context.Background(), "owner-librarian", document.UploadID, "analyze the experimental method and reported results", document)
	if len(retrieval.Sections) != librarianSectionLimit {
		t.Fatalf("sections=%#v, want %d entries", retrieval.Sections, librarianSectionLimit)
	}
	if retrieval.Sections[0] != "2. Method" {
		t.Fatalf("best section=%q, want method section", retrieval.Sections[0])
	}
	if retrieval.Queries != len(retrieval.Sections) {
		t.Fatalf("queries=%d, sections=%d", retrieval.Queries, len(retrieval.Sections))
	}
	if retrieval.Chunks == 0 {
		t.Fatalf("no chunks retrieved: %#v", retrieval)
	}
	if !strings.Contains(retrieval.Context, "§2. Method") || !strings.Contains(retrieval.Context, "results") {
		t.Fatalf("context is missing section tags: %q", retrieval.Context)
	}
	if utf8.RuneCountInString(retrieval.Context) > documents.MaxExcerptChars {
		t.Fatalf("context has %d runes, budget is %d", utf8.RuneCountInString(retrieval.Context), documents.MaxExcerptChars)
	}
	trace := retrieval.traceLine()
	if !strings.HasPrefix(trace, "[retrieval] sections=6 chunks=") {
		t.Fatalf("trace line=%q", trace)
	}
}

func TestLibrarianRetrievalFallsBackWithoutSections(t *testing.T) {
	document := librarianPlainDocument()
	store := librarianTestStore(t, document)
	agent := &LibrarianAgent{Name: "librarian_agent", Documents: store}

	description := "summarize the method and results"
	retrieval := agent.retrieveDocumentContext(context.Background(), "owner-librarian", document.UploadID, description, document)
	if len(retrieval.Sections) != 0 {
		t.Fatalf("plain document should have no sections: %#v", retrieval.Sections)
	}
	if retrieval.Queries != len(librarianRetrievalQueries(description)) {
		t.Fatalf("queries=%d, want %d", retrieval.Queries, len(librarianRetrievalQueries(description)))
	}
	if retrieval.Chunks == 0 || !strings.Contains(retrieval.Context, "[p.") {
		t.Fatalf("fallback retrieval produced no tagged context: %#v", retrieval)
	}
	if !strings.HasPrefix(retrieval.traceLine(), "[retrieval] sections=0 chunks=") {
		t.Fatalf("trace line=%q", retrieval.traceLine())
	}
}

func TestLibrarianRetrievalIsSafeWithoutDocuments(t *testing.T) {
	agent := &LibrarianAgent{Name: "librarian_agent"}
	retrieval := agent.retrieveDocumentContext(context.Background(), "owner", "doc", "anything", &documents.Document{})
	if retrieval.Context != "" || retrieval.Chunks != 0 || retrieval.Queries != 0 || len(retrieval.Sections) != 0 {
		t.Fatalf("nil Documents should return an empty retrieval: %#v", retrieval)
	}
}

func TestSelectLibrarianSectionsRanksAndLimits(t *testing.T) {
	sections := []documents.SectionInfo{
		{Title: "1. Introduction", StartPage: 1, EndPage: 2},
		{Title: "2. Method", StartPage: 3, EndPage: 4},
		{Title: "3. Experiments", StartPage: 5, EndPage: 5},
		{Title: "4. Results", StartPage: 6, EndPage: 6},
		{Title: "5. Discussion", StartPage: 7, EndPage: 7},
		{Title: "6. Conclusion", StartPage: 8, EndPage: 8},
		{Title: "7. Appendix", StartPage: 9, EndPage: 9},
		{Title: "8. References", StartPage: 10, EndPage: 10},
	}
	selected := selectLibrarianSections(sections, "focus on the method and results", librarianSectionLimit)
	if len(selected) != librarianSectionLimit {
		t.Fatalf("selected=%#v, want %d entries", selected, librarianSectionLimit)
	}
	if selected[0].Title != "2. Method" || selected[1].Title != "4. Results" {
		t.Fatalf("unexpected ranking: %#v", selected)
	}
}

func TestBuildRetrievalContextHonorsCharacterBudget(t *testing.T) {
	chunks := make([]documents.Chunk, 0, 40)
	for index := 0; index < 40; index++ {
		chunks = append(chunks, documents.Chunk{
			ID:      "chunk-" + string(rune('a'+index)),
			Page:    index + 1,
			Section: "2. Method",
			Text:    strings.Repeat("x", 900),
		})
	}
	contextText, included := buildRetrievalContext(chunks, nil, documents.MaxExcerptChars)
	if included <= 0 {
		t.Fatal("expected some chunks inside the budget")
	}
	if utf8.RuneCountInString(contextText) > documents.MaxExcerptChars {
		t.Fatalf("context has %d runes", utf8.RuneCountInString(contextText))
	}

	oversized := []documents.Chunk{{ID: "one", Page: 1, Text: strings.Repeat("y", documents.MaxExcerptChars+500)}}
	truncated, included := buildRetrievalContext(oversized, nil, documents.MaxExcerptChars)
	if included != 1 {
		t.Fatalf("included=%d", included)
	}
	if utf8.RuneCountInString(truncated) > documents.MaxExcerptChars {
		t.Fatalf("truncated context has %d runes", utf8.RuneCountInString(truncated))
	}
	if !strings.HasSuffix(truncated, "...[truncated]") {
		t.Fatalf("truncated context has no marker: %q", truncated[len(truncated)-40:])
	}
}
