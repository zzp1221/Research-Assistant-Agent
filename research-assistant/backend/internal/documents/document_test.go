package documents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSplitPagesPreservesPageNumbers(t *testing.T) {
	raw := "Title page\n\nAbstract\f\n\nPage two body\f\n\nPage three body\f"
	pages := SplitPages(raw)
	if len(pages) != 3 {
		t.Fatalf("pages=%d, want 3 (%#v)", len(pages), pages)
	}
	if pages[0].Number != 1 || pages[1].Number != 2 || pages[2].Number != 3 {
		t.Fatalf("unexpected page numbers: %#v", pages)
	}
	if !strings.Contains(pages[1].Text, "Page two body") {
		t.Fatalf("page two text=%q", pages[1].Text)
	}
	if pages[2].Chars != len([]rune(pages[2].Text)) {
		t.Fatalf("chars not counted by rune: %#v", pages[2])
	}
}

func TestCleanTextCollapsesBlankRunsAndNul(t *testing.T) {
	cleaned := CleanText("a\x00b\n\n\n\nc\n")
	if cleaned != "ab\n\nc" {
		t.Fatalf("cleaned=%q", cleaned)
	}
}

func TestSaveLoadAndExcerptRoundTrip(t *testing.T) {
	directory := t.TempDir()
	document := &Document{
		Version: Version, UploadID: "upload-1", Status: StatusExtracted,
		PageCount: 2, Characters: 34,
		Pages: []Page{{Number: 1, Text: "first page", Chars: 10}, {Number: 2, Text: "second page", Chars: 11}},
	}
	if err := Save(directory, document); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path(directory))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("document permissions=%o", info.Mode().Perm())
	}
	loaded, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.HasText() || loaded.PageCount != 2 {
		t.Fatalf("loaded=%#v", loaded)
	}
	full := FullText(loaded)
	if !strings.Contains(full, "[Page 1]") || !strings.Contains(full, "[Page 2]") {
		t.Fatalf("full text=%q", full)
	}
	context := ContextForPage(loaded, 2, 1)
	if !strings.Contains(context, "first page") || !strings.Contains(context, "second page") {
		t.Fatalf("context=%q", context)
	}
	if ContextForPage(loaded, 3, 1) != "" {
		t.Fatal("out-of-range page should return empty context")
	}
}

func TestExcerptTruncatesLongText(t *testing.T) {
	long := strings.Repeat("x", MaxExcerptChars+100)
	document := &Document{Status: StatusExtracted, PageCount: 1, Pages: []Page{{Number: 1, Text: long, Chars: len(long)}}}
	excerpt := Excerpt(document)
	if !strings.HasSuffix(excerpt, "...[truncated]") {
		t.Fatalf("excerpt was not truncated: %d chars", len(excerpt))
	}
}

func TestLoadMissingDocumentReturnsError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected an error for a missing document")
	}
}
