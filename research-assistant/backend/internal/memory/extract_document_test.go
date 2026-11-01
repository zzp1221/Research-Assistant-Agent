package memory

import (
	"context"
	"strings"
	"testing"
)

func TestRecordDocumentMarksPendingWithoutSections(t *testing.T) {
	store := NewMemoryStore()
	if err := RecordDocument(context.Background(), store, "u1", "p1", "s1", "plan-1", "doc-1", "Attention Is All You Need", 15, nil, "partial"); err != nil {
		t.Fatal(err)
	}
	rec, err := store.Get(context.Background(), "u1", "mem-doc-doc-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != StatusPending {
		t.Fatalf("expected pending_review, got %s", rec.Status)
	}
	if !strings.Contains(rec.Content, "section_detection_failed") {
		t.Fatalf("expected section failure note: %s", rec.Content)
	}
}
