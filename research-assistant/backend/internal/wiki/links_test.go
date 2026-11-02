package wiki

import (
	"context"
	"testing"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

func TestEntityOverlapCreatesSymmetricLink(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryPaperStore()
	mem := memory.NewMemoryStore()
	builder := NewBuilder(store, mem, nil, nil, NewAliasStore(t.TempDir()))
	pages := []PaperPage{
		{PaperID: "paper-a", OwnerID: "u1", Title: "Paper A", Entities: []memory.Entity{{Type: "dataset", Value: "wmt14"}}},
		{PaperID: "paper-b", OwnerID: "u1", Title: "Paper B", Entities: []memory.Entity{{Type: "dataset", Value: "wmt14"}}},
	}
	for _, page := range pages {
		if _, err := store.Put(ctx, page); err != nil {
			t.Fatalf("put page: %v", err)
		}
	}
	links, err := builder.BuildLinksForPaper(ctx, "u1", "paper-a")
	if err != nil {
		t.Fatalf("build links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links=%d want 1", len(links))
	}
	link := links[0]
	if link.Type != LinkSameDataset || link.FromPaper != "paper-a" || link.ToPaper != "paper-b" {
		t.Fatalf("unexpected link: %+v", link)
	}
	if link.Status != memory.StatusActive {
		t.Fatalf("entity link should be active, got %q", link.Status)
	}
	// Building from the other side must not duplicate the symmetric relation.
	if _, err := builder.BuildLinksForPaper(ctx, "u1", "paper-b"); err != nil {
		t.Fatalf("build reverse: %v", err)
	}
	all, total, err := builder.ListLinks(ctx, "u1", "", "", "", 50, 0)
	if err != nil || total != 1 || len(all) != 1 {
		t.Fatalf("symmetric dedupe total=%d len=%d err=%v", total, len(all), err)
	}
	record, err := mem.Get(ctx, "u1", link.ID)
	if err != nil {
		t.Fatalf("link must live in memory: %v", err)
	}
	if record.Kind != memory.KindFact || !hasTag(record.Tags, TagWikiLink) {
		t.Fatalf("unexpected link record: %+v", record)
	}
}

func TestLinkConfirmAndReject(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryPaperStore()
	mem := memory.NewMemoryStore()
	builder := NewBuilder(store, mem, nil, nil, NewAliasStore(t.TempDir()))
	for _, page := range []PaperPage{
		{PaperID: "paper-a", OwnerID: "u1", Title: "Paper A", Entities: []memory.Entity{{Type: "method", Value: "transformer"}}},
		{PaperID: "paper-b", OwnerID: "u1", Title: "Paper B", Entities: []memory.Entity{{Type: "method", Value: "transformer"}}},
	} {
		_, _ = store.Put(ctx, page)
	}
	links, err := builder.BuildLinksForPaper(ctx, "u1", "paper-a")
	if err != nil || len(links) != 1 {
		t.Fatalf("build links: %v %d", err, len(links))
	}
	if _, err := builder.RejectLink(ctx, "u1", links[0].ID, "not the same method"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rejected, _, err := builder.ListLinks(ctx, "u1", "", "", string(memory.StatusArchived), 50, 0)
	if err != nil || len(rejected) != 1 {
		t.Fatalf("rejected links=%d err=%v", len(rejected), err)
	}
	if _, err := builder.ConfirmLink(ctx, "u1", links[0].ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	confirmed, err := mem.Get(ctx, "u1", links[0].ID)
	if err != nil || confirmed.Status != memory.StatusActive {
		t.Fatalf("confirm status=%q err=%v", confirmed.Status, err)
	}
}

func TestRebuildOwnerFromMemory(t *testing.T) {
	ctx := context.Background()
	mem := memory.NewMemoryStore()
	dir := t.TempDir()
	store := NewMemoryPaperStore()
	builder := NewBuilder(store, mem, nil, nil, NewAliasStore(dir))
	page, err := builder.EnsurePaper(ctx, "u1", "proj", models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762"}, "")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := builder.AddUserNote(ctx, "u1", "proj", page.PaperID, "note"); err != nil {
		t.Fatalf("note: %v", err)
	}
	// memory.RecordPlan writes this deterministic paper metadata fact.
	fact := memory.Record{
		ID: "mem-paper-plan-1", Kind: memory.KindFact,
		Scope:   memory.Scope{OwnerID: "u1", ProjectID: "proj"},
		Title:   "论文：Attention Is All You Need",
		Content: "标题：Attention Is All You Need；作者：Ashish Vaswani；年份：2017；会议：NeurIPS；arXiv：1706.03762；仓库：",
		Tags:    []string{"paper", "metadata"},
		Source:  memory.Source{Type: "plan", Ref: "plan-1"},
		Trust:   memory.TrustVerified, Status: memory.StatusActive,
	}
	if _, err := mem.Put(ctx, fact); err != nil {
		t.Fatalf("put fact: %v", err)
	}
	// Simulate deleting data/wiki: a fresh empty store with the same memory.
	empty := NewMemoryPaperStore()
	rebuilt := NewBuilder(empty, mem, nil, nil, NewAliasStore(dir))
	count, err := rebuilt.RebuildOwner(ctx, "u1")
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	if count < 1 {
		t.Fatalf("rebuild count=%d", count)
	}
	got, err := empty.Get(ctx, "u1", page.PaperID)
	if err != nil {
		t.Fatalf("paper page not rebuilt: %v", err)
	}
	if len(got.PointIDs) != 1 {
		t.Fatalf("rebuilt point ids=%v", got.PointIDs)
	}
}
func TestListLinksIncludesPendingReview(t *testing.T) {
	ctx := context.Background()
	builder := NewBuilder(NewMemoryPaperStore(), memory.NewMemoryStore(), nil, nil, NewAliasStore(t.TempDir()))
	link, err := builder.writeLink(ctx, "u1", "", LinkCandidate{
		FromPaper: "paper-a", ToPaper: "paper-b", Type: LinkRelated, Source: "llm",
		Confidence: 0.4, Evidence: []memory.SourceRef{{Section: "text", Quote: "candidate"}},
	})
	if err != nil {
		t.Fatalf("write pending link: %v", err)
	}
	if link.Status != memory.StatusPending {
		t.Fatalf("llm link status=%q want pending", link.Status)
	}
	links, total, err := builder.ListLinks(ctx, "u1", "", "", "", 50, 0)
	if err != nil || total != 1 || len(links) != 1 {
		t.Fatalf("pending link must be listed: total=%d err=%v", total, err)
	}
}
