package wiki

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

func TestResolveIdentityGuards(t *testing.T) {
	ctx := context.Background()
	_ = ctx
	store := NewAliasStore(t.TempDir())
	// CP-02: same title, different year, different DOI must stay distinct.
	a, _, err := store.Resolve("u1", models.PaperMetadata{Title: "A Survey on RAG", Year: 2023, DOI: "10.5555/rag.2023"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := store.Resolve("u1", models.PaperMetadata{Title: "A Survey on RAG", Year: 2024, DOI: "10.5555/rag.2024"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different years must not merge")
	}
	// CP-03: bare same title/year with no authors must stay distinct.
	c, _, err := store.Resolve("u1", models.PaperMetadata{Title: "Efficient Retrieval", Year: 2022}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := store.Resolve("u1", models.PaperMetadata{Title: "Efficient Retrieval", Year: 2022}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c == d {
		t.Fatal("bare same title/year must not merge")
	}
	// CP-01: DOI, arXiv and bare title must merge into one paper.
	e, _, err := store.Resolve("u1", models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, DOI: "10.48550/arXiv.1706.03762"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f, _, err := store.Resolve("u1", models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762v5"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g, _, err := store.Resolve("u1", models.PaperMetadata{Title: "Attention is all you need", Year: 2017}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if e != f || e != g {
		t.Fatalf("DOI/arXiv/title aliases must merge: %s %s %s", e, f, g)
	}
	// Same title/year with overlapping authors merges.
	h, _, err := store.Resolve("u1", models.PaperMetadata{Title: "Author Paper", Year: 2021, Authors: []string{"Alice Smith", "Bob Jones"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	i, _, err := store.Resolve("u1", models.PaperMetadata{Title: "Author Paper", Year: 2021, Authors: []string{"Bob Jones", "Carol Lee"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h != i {
		t.Fatalf("author overlap must merge: %s %s", h, i)
	}
}

func TestMatchPapersCJK(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryPaperStore()
	now := time.Now().UTC()
	store.Put(ctx, PaperPage{PaperID: "paper-cn", OwnerID: "u1", Title: "中文目标论文", Abstract: "本文提出一种新的检索方法", Year: 2024, UpdatedAt: now})
	store.Put(ctx, PaperPage{PaperID: "paper-en", OwnerID: "u1", Title: "Unrelated English Paper", Abstract: "unrelated work", Year: 2023, UpdatedAt: now.Add(-time.Hour)})
	builder := NewBuilder(store, memory.NewMemoryStore(), nil, nil, NewAliasStore(t.TempDir()))
	pages, reason := builder.matchPapers(ctx, TaskQuery{OwnerID: "u1", Text: "这篇论文的方法是什么"}, 2)
	if len(pages) != 1 || pages[0].PaperID != "paper-cn" {
		t.Fatalf("CJK query matched %+v reason=%s", pages, reason)
	}
	if reason != "token_match" {
		t.Fatalf("reason=%s", reason)
	}
	single := NewMemoryPaperStore()
	single.Put(ctx, PaperPage{PaperID: "paper-solo", OwnerID: "u2", Title: "Solo Paper", Abstract: "nothing relevant", Year: 2024})
	singleBuilder := NewBuilder(single, memory.NewMemoryStore(), nil, nil, NewAliasStore(t.TempDir()))
	pages, reason = singleBuilder.matchPapers(ctx, TaskQuery{OwnerID: "u2", Text: "这篇论文的方法是什么"}, 2)
	if len(pages) != 1 || pages[0].PaperID != "paper-solo" || reason != "single_paper_fallback" {
		t.Fatalf("single fallback=%+v reason=%s", pages, reason)
	}
}

func TestPaperStoreListAllBeyond200(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryPaperStore()
	for index := 0; index < 220; index++ {
		if _, err := store.Put(ctx, PaperPage{PaperID: "paper-" + string(rune('a'+index%26)) + "-" + string(rune('a'+index/26)), OwnerID: "u1", Title: "Paper"}); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.ListAll(ctx, "u1")
	if err != nil || len(all) != 220 {
		t.Fatalf("ListAll len=%d err=%v", len(all), err)
	}
	page, total, err := store.List(ctx, PaperQuery{OwnerID: "u1", Limit: 500})
	if err != nil || total != 220 || len(page) != 200 {
		t.Fatalf("List total=%d len=%d err=%v", total, len(page), err)
	}
}
