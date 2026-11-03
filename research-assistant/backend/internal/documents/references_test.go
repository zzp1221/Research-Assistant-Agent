package documents

import (
	"strings"
	"testing"
)

func TestExtractReferencesNumbered(t *testing.T) {
	document := &Document{Pages: []Page{
		{Number: 1, Text: "1 Introduction\nWe study the problem.\nReferences\n[1] A. Vaswani, N. Shazeer. Attention Is All You Need. NeurIPS, 2017. arXiv:1706.03762.\n[2] J. Devlin, M. Chang. BERT: Pre-training of Deep Bidirectional Transformers. NAACL, 2019. doi:10.18653/v1/N19-1423.\n[3] L. Smith. A Study of Things. Journal of Tests, 2020."},
	}}
	entries := ExtractReferences(document)
	if len(entries) != 3 {
		t.Fatalf("entries=%d want 3: %+v", len(entries), entries)
	}
	if entries[0].ArxivID != "1706.03762" || entries[0].Year != 2017 {
		t.Fatalf("entry0=%+v", entries[0])
	}
	if !strings.Contains(entries[0].Title, "Attention Is All You Need") {
		t.Fatalf("entry0 title=%q", entries[0].Title)
	}
	if entries[1].DOI != "10.18653/v1/N19-1423" || entries[1].Year != 2019 {
		t.Fatalf("entry1=%+v", entries[1])
	}
	if entries[2].Year != 2020 || !strings.Contains(entries[2].Title, "Study of Things") {
		t.Fatalf("entry2=%+v", entries[2])
	}
	if entries[0].Page != 1 || !strings.Contains(entries[0].Raw, "Vaswani") {
		t.Fatalf("entry0 page/raw=%+v", entries[0])
	}
}

func TestExtractReferencesAuthorYear(t *testing.T) {
	document := &Document{Pages: []Page{
		{Number: 2, Text: "References\n\nVaswani, A., Shazeer, N. (2017). Attention is all you need. NeurIPS.\n\nDevlin, J. (2019). BERT: Pre-training of deep bidirectional transformers. NAACL."},
	}}
	entries := ExtractReferences(document)
	if len(entries) != 2 {
		t.Fatalf("entries=%d want 2: %+v", len(entries), entries)
	}
	if entries[0].Year != 2017 || !strings.Contains(strings.ToLower(entries[0].Title), "attention") {
		t.Fatalf("entry0=%+v", entries[0])
	}
	if entries[1].Year != 2019 || !strings.Contains(entries[1].Title, "BERT") {
		t.Fatalf("entry1=%+v", entries[1])
	}
}

func TestExtractReferencesAcrossPages(t *testing.T) {
	document := &Document{Pages: []Page{
		{Number: 1, Text: "Body text.\nReferences\n[1] A. One. First Paper. Conf, 2020."},
		{Number: 2, Text: "[2] B. Two. Second Paper. Conf, 2021."},
	}}
	entries := ExtractReferences(document)
	if len(entries) != 2 {
		t.Fatalf("entries=%d want 2: %+v", len(entries), entries)
	}
	if entries[1].Page != 2 {
		t.Fatalf("entry1 page=%d", entries[1].Page)
	}
}

func TestExtractReferencesNoSection(t *testing.T) {
	document := &Document{Pages: []Page{{Number: 1, Text: "Abstract\nNo references here."}}}
	if entries := ExtractReferences(document); len(entries) != 0 {
		t.Fatalf("expected no entries, got %+v", entries)
	}
}
