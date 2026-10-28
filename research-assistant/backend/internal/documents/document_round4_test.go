package documents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveWritesTableCSVAndLoadsIt(t *testing.T) {
	directory := t.TempDir()
	document := &Document{
		Version: Version, UploadID: "upload-table", Status: StatusExtracted,
		PageCount: 1, TotalPages: 1, Characters: 20,
		Pages: []Page{{Number: 1, Text: "Table 1: Results\nModel  Acc\nGNN  0.9", Chars: 34}},
		Tables: []Table{{
			ID: "table-p1-1", Page: 1, Caption: "Table 1: Results",
			Rows: [][]string{{"Model", "Acc"}, {"GNN", "0.9"}},
		}},
	}
	if err := Save(directory, document); err != nil {
		t.Fatal(err)
	}
	if document.Tables[0].CSVRef == "" {
		t.Fatal("CSVRef was not assigned")
	}
	raw, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(document.Tables[0].CSVRef)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "Model,Acc") || !strings.Contains(string(raw), "GNN,0.9") {
		t.Fatalf("csv=%q", string(raw))
	}
	loaded, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Tables) != 1 || loaded.Tables[0].CSVRef == "" {
		t.Fatalf("loaded=%#v", loaded)
	}
}

func TestLoadLegacyV1Document(t *testing.T) {
	directory := t.TempDir()
	legacy := map[string]any{
		"version": "document.text/v1", "upload_id": "legacy", "status": "extracted",
		"page_count": 1, "characters": 11,
		"pages": []map[string]any{{"number": 1, "text": "hello world", "chars": 11}},
	}
	raw, _ := json.Marshal(legacy)
	if err := os.WriteFile(Path(directory), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	if !document.HasText() || document.TotalPages != 1 || document.PageCount != 1 {
		t.Fatalf("legacy=%#v", document)
	}
	if len(document.Blocks) != 0 || len(document.Tables) != 0 {
		t.Fatalf("legacy should not invent blocks: %#v", document)
	}
}

func TestStatusPartialHasText(t *testing.T) {
	document := &Document{Status: StatusPartial, PageCount: 2, Pages: []Page{{Number: 1, Text: "x", Chars: 1}}}
	if !document.HasText() {
		t.Fatal("partial document with text must be usable")
	}
	empty := &Document{Status: StatusEmpty, PageCount: 2}
	if empty.HasText() {
		t.Fatal("empty document must not be usable")
	}
}

func TestLocateQuoteLocationReturnsBlockProvenance(t *testing.T) {
	document := &Document{
		Pages: []Page{{Number: 1, Text: "First paragraph here.\nSecond paragraph with the key sentence."}},
		Blocks: []Block{
			{ID: "p1-b1", Page: 1, Type: BlockParagraph, Text: "First paragraph here."},
			{ID: "p1-b2", Page: 1, Type: BlockParagraph, Text: "Second paragraph with the key sentence."},
		},
	}
	location := LocateQuoteLocation(document, "key sentence")
	if !location.Found || location.Page != 1 || location.BlockID != "p1-b2" {
		t.Fatalf("location=%#v", location)
	}
	if location.BlockOffset <= 0 {
		t.Fatalf("block offset=%d", location.BlockOffset)
	}
	page, _, _, ok := LocateQuote(document, "First paragraph")
	if !ok || page != 1 {
		t.Fatalf("legacy LocateQuote page=%d ok=%v", page, ok)
	}
}
