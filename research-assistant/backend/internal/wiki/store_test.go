package wiki

import (
	"context"
	"testing"
)

func TestPaperStorePersistenceAndOwnerIsolation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := NewPaperStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	page := PaperPage{PaperID: "paper-abc", OwnerID: "u1", ProjectID: "p1", Title: "Attention", Year: 2017, DOI: "10.1/x"}
	if _, err := store.Put(ctx, page); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := store.Get(ctx, "u2", "paper-abc"); err == nil {
		t.Fatalf("owner isolation failed")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	reopened, err := NewPaperStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.Get(ctx, "u1", "paper-abc")
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	if got.Title != "Attention" || got.DOI != "10.1/x" {
		t.Fatalf("unexpected page after reopen: %+v", got)
	}
}

func TestPaperStoreListFilterAndArchive(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryPaperStore()
	for _, page := range []PaperPage{
		{PaperID: "paper-1", OwnerID: "u1", Title: "Attention Is All You Need", Year: 2017},
		{PaperID: "paper-2", OwnerID: "u1", Title: "BERT", Year: 2019, ProjectID: "p2"},
	} {
		if _, err := store.Put(ctx, page); err != nil {
			t.Fatalf("put: %v", err)
		}
	}
	papers, total, err := store.List(ctx, PaperQuery{OwnerID: "u1", Text: "attention"})
	if err != nil || total != 1 || len(papers) != 1 {
		t.Fatalf("text filter total=%d len=%d err=%v", total, len(papers), err)
	}
	papers, total, _ = store.List(ctx, PaperQuery{OwnerID: "u1", ProjectID: "p2"})
	if total != 1 || papers[0].PaperID != "paper-2" {
		t.Fatalf("project filter: %+v", papers)
	}
	if _, err := store.Archive(ctx, "u1", "paper-1", true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	_, total, _ = store.List(ctx, PaperQuery{OwnerID: "u1"})
	if total != 1 {
		t.Fatalf("archived paper should be hidden, total=%d", total)
	}
	_, total, _ = store.List(ctx, PaperQuery{OwnerID: "u1", Status: "all"})
	if total != 2 {
		t.Fatalf("status=all should include archived, total=%d", total)
	}
}
