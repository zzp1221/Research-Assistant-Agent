package wiki

import (
	"context"
	"strings"
	"testing"

	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

func TestCanonicalKeyPriority(t *testing.T) {
	cases := []struct {
		name string
		meta models.PaperMetadata
		want string
	}{
		{"doi wins", models.PaperMetadata{Title: "Attention", Year: 2017, DOI: "https://doi.org/10.48550/arXiv.1706.03762", ArxivID: "1706.03762v5"}, "doi:10.48550/arxiv.1706.03762"},
		{"arxiv second", models.PaperMetadata{Title: "Attention", Year: 2017, ArxivID: "1706.03762v5"}, "arxiv:1706.03762"},
		{"title last", models.PaperMetadata{Title: "Attention Is All You Need!", Year: 2017}, "title:attention is all you need:2017"},
	}
	for _, testCase := range cases {
		if got := CanonicalKey(testCase.meta, nil); got != testCase.want {
			t.Fatalf("%s: CanonicalKey=%q want %q", testCase.name, got, testCase.want)
		}
	}
}

func TestPaperIDOwnerScoped(t *testing.T) {
	first := PaperID("owner-a", "doi:10.1/x")
	second := PaperID("owner-b", "doi:10.1/x")
	if first == second {
		t.Fatalf("paper ids must differ per owner: %q", first)
	}
	if !strings.HasPrefix(first, "paper-") || len(first) != len("paper-")+24 {
		t.Fatalf("unexpected paper id shape: %q", first)
	}
}

func TestAliasStoreMergesDOIAndArxiv(t *testing.T) {
	store := NewAliasStore(t.TempDir())
	arxivMeta := models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762v5"}
	first, firstKey, err := store.Resolve("u1", arxivMeta, nil)
	if err != nil {
		t.Fatalf("resolve arxiv: %v", err)
	}
	if firstKey != "arxiv:1706.03762" {
		t.Fatalf("first key=%q", firstKey)
	}
	doiMeta := models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, DOI: "10.48550/arXiv.1706.03762"}
	second, secondKey, err := store.Resolve("u1", doiMeta, nil)
	if err != nil {
		t.Fatalf("resolve doi: %v", err)
	}
	if first != second {
		t.Fatalf("DOI and arXiv must merge to one paper: %q vs %q", first, second)
	}
	if secondKey != "doi:10.48550/arxiv.1706.03762" {
		t.Fatalf("canonical key should upgrade to DOI, got %q", secondKey)
	}
	other, _, err := store.Resolve("u2", arxivMeta, nil)
	if err != nil {
		t.Fatalf("resolve other owner: %v", err)
	}
	if other == first {
		t.Fatalf("owners must not share paper ids")
	}
}

func TestAliasStoreLibraryRecordAliases(t *testing.T) {
	store := NewAliasStore(t.TempDir())
	record := &literature.Record{ID: "lib-1", Title: "Deep Residual Learning", Year: 2016, DOI: "10.1007/978-3-319-46493-0_38", ArxivID: "1512.03385"}
	meta := models.PaperMetadata{Title: "Deep Residual Learning", Year: 2016}
	paperID, key, err := store.Resolve("u1", meta, record)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if key != "doi:10.1007/978-3-319-46493-0_38" {
		t.Fatalf("library DOI should win: %q", key)
	}
	aliases := BuildAliases(meta, record)
	for _, wanted := range []string{"doi:10.1007/978-3-319-46493-0_38", "arxiv:1512.03385", "library:lib-1"} {
		if !containsString(aliases, wanted) {
			t.Fatalf("aliases missing %q: %v", wanted, aliases)
		}
	}
	// A second metadata-only resolve for the same title must hit the alias.
	again, _, err := store.Resolve("u1", models.PaperMetadata{Title: "Deep Residual Learning", Year: 2016}, nil)
	if err != nil {
		t.Fatalf("resolve again: %v", err)
	}
	if again != paperID {
		t.Fatalf("title alias should resolve to %q, got %q", paperID, again)
	}
}

func TestBuilderEnsurePaperAndRefresh(t *testing.T) {
	ctx := context.Background()
	mem := memory.NewMemoryStore()
	store := NewMemoryPaperStore()
	builder := NewBuilder(store, mem, nil, nil, NewAliasStore(t.TempDir()))
	page, err := builder.EnsurePaper(ctx, "u1", "proj", models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762"}, "")
	if err != nil {
		t.Fatalf("ensure paper: %v", err)
	}
	if page.Title != "Attention Is All You Need" || page.ArxivID != "1706.03762" {
		t.Fatalf("unexpected page: %+v", page)
	}
	if page.Reproduction.Status != "none" {
		t.Fatalf("reproduction status=%q", page.Reproduction.Status)
	}
	if _, err := builder.EnsurePaper(ctx, "u2", "proj", models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017}, ""); err != nil {
		t.Fatalf("other owner: %v", err)
	}
	if _, err := store.Get(ctx, "u2", page.PaperID); err == nil {
		t.Fatalf("cross-owner paper lookup must fail")
	}
}

func TestEnsurePaperRejectsHidden(t *testing.T) {
	ctx := context.Background()
	builder := NewBuilder(NewMemoryPaperStore(), memory.NewMemoryStore(), nil, nil, NewAliasStore(t.TempDir()))
	if _, err := builder.EnsurePaper(ctx, "u1", "", models.PaperMetadata{Title: "Hidden Benchmark Secret Paper"}, ""); err == nil {
		t.Fatalf("hidden benchmark title must be rejected")
	}
}
