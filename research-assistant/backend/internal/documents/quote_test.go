package documents

import (
	"strings"
	"testing"
)

func quoteTestDocument() *Document {
	pages := []Page{
		{
			Number: 1,
			Text:   "We present a large-\nscale sys-\ntem for retrieval.\nThe   quick\nbrown\tfox jumps.",
			Chars:  0,
		},
		{
			Number: 2,
			Text:   "本文提出了一种新的方法，并在实验中验证了有效性。",
			Chars:  0,
		},
	}
	for index := range pages {
		pages[index].Chars = len([]rune(pages[index].Text))
	}
	return &Document{Version: Version, UploadID: "quote-test", Status: StatusExtracted, PageCount: len(pages), Pages: pages}
}

func TestLocateQuoteIgnoresCaseAndWhitespace(t *testing.T) {
	document := quoteTestDocument()
	page, start, end, ok := LocateQuote(document, "THE QUICK BROWN FOX")
	if !ok {
		t.Fatal("quote was not located")
	}
	if page != 1 {
		t.Fatalf("page=%d, want 1", page)
	}
	located := document.Pages[0].Text[start:end]
	normalized := normalizeMatchText(located)
	if normalized.text != "the quick brown fox" {
		t.Fatalf("located span=%q normalized=%q", located, normalized.text)
	}
}

func TestLocateQuoteHandlesHyphenatedLineBreaks(t *testing.T) {
	document := quoteTestDocument()
	page, start, end, ok := LocateQuote(document, "system for retrieval")
	if !ok || page != 1 {
		t.Fatalf("hyphenated quote ok=%v page=%d", ok, page)
	}
	located := document.Pages[0].Text[start:end]
	if !strings.Contains(located, "sys-") || !strings.Contains(located, "tem") {
		t.Fatalf("located span=%q", located)
	}

	page, start, end, ok = LocateQuote(document, "large-scale system")
	if !ok || page != 1 {
		t.Fatalf("keep-hyphen quote ok=%v page=%d", ok, page)
	}
	if located = document.Pages[0].Text[start:end]; !strings.Contains(located, "large-") {
		t.Fatalf("located span=%q", located)
	}
}

func TestLocateQuoteFindsChinesePage(t *testing.T) {
	document := quoteTestDocument()
	page, start, end, ok := LocateQuote(document, "新的方法")
	if !ok || page != 2 {
		t.Fatalf("Chinese quote ok=%v page=%d", ok, page)
	}
	if located := document.Pages[1].Text[start:end]; !strings.Contains(located, "新的方法") {
		t.Fatalf("located span=%q", located)
	}
}

func TestLocateQuoteReturnsFalseWhenMissing(t *testing.T) {
	document := quoteTestDocument()
	page, start, end, ok := LocateQuote(document, "not present anywhere")
	if ok || page != 0 || start != 0 || end != 0 {
		t.Fatalf("missing quote returned ok=%v page=%d start=%d end=%d", ok, page, start, end)
	}
	if _, _, _, ok := LocateQuote(nil, "anything"); ok {
		t.Fatal("nil document should not match")
	}
	if _, _, _, ok := LocateQuote(document, "   "); ok {
		t.Fatal("blank quote should not match")
	}
}

func TestNormalizeMatchTextKeepsOffsets(t *testing.T) {
	text := "Alpha-\nBeta  GAMMA"
	normalized := normalizeMatchText(text)
	if normalized.text != "alphabeta gamma" {
		t.Fatalf("normalized=%q", normalized.text)
	}
	if len(normalized.starts) != len(normalized.text) || len(normalized.ends) != len(normalized.text) {
		t.Fatalf("mapping length mismatch: %d/%d for %q", len(normalized.starts), len(normalized.ends), normalized.text)
	}
	for index := 0; index < len(normalized.text); index++ {
		if normalized.starts[index] > normalized.ends[index] {
			t.Fatalf("bad mapping at %d: %d>%d", index, normalized.starts[index], normalized.ends[index])
		}
		if normalized.ends[index] > len(text) {
			t.Fatalf("offset out of range at %d: %d", index, normalized.ends[index])
		}
	}
}
