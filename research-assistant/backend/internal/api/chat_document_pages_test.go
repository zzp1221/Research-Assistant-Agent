package api

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"research-assistant-backend/internal/documents"
)

func TestChatDocumentReferencePartialExtractionKeepsOriginalPageNumbers(t *testing.T) {
	fixture := newChatDocumentFixture(t)
	metadata := seedChatDocumentUpload(t, "partial-owner", "77777777-7777-4777-8777-777777777777", "partial.pdf",
		[]string{"first original source", "second original source", "third original source"}, documents.StatusExtracted)
	directory := filepath.Dir(metadata.StoredPath)
	document, err := documents.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	document.Pages = []documents.Page{document.Pages[0], document.Pages[2]}
	document.PageCount, document.TotalPages, document.Status = 2, 3, documents.StatusPartial
	if err := documents.Save(directory, document); err != nil {
		t.Fatal(err)
	}
	missing, decoded := fixture.send(t, "partial-owner", "missing-page-session", "解释第二页", []ChatDocumentReference{
		{UploadID: metadata.ID, Page: 2, Quote: "user selection"},
	})
	if missing.Code != http.StatusUnprocessableEntity || decoded.Code != "document_reference_page_unavailable" {
		t.Fatalf("missing extracted page: %d %s", missing.Code, missing.Body)
	}
	if fixture.callCount() != 0 {
		t.Fatal("a different page was sent to the model for a missing page")
	}
	response, _ := fixture.send(t, "partial-owner", "present-page-session", "解释第三页", []ChatDocumentReference{
		{UploadID: metadata.ID, Page: 3, Quote: "third original source"},
	})
	if response.Code != http.StatusOK {
		t.Fatalf("valid page in partial extraction: %d %s", response.Code, response.Body)
	}
	if text := fixture.lastCallText(t); !strings.Contains(text, "third original source") {
		t.Fatal("the original third page was missing from the model context")
	}
}

func TestChatDocumentPageTextOnlyUsesPositionForUnnumberedLegacyPages(t *testing.T) {
	sparse := &documents.Document{Pages: []documents.Page{{Number: 1, Text: "one"}, {Number: 3, Text: "three"}}}
	if text, ok := chatDocumentPageText(sparse, 2); ok {
		t.Fatalf("missing page 2 was replaced by %q", text)
	}
	legacy := &documents.Document{Pages: []documents.Page{{Text: "legacy one"}, {Text: "legacy two"}}}
	if text, ok := chatDocumentPageText(legacy, 2); !ok || text != "legacy two" {
		t.Fatalf("legacy positional pages no longer load: %q %v", text, ok)
	}
}
