package wiki

import (
	"context"
	"strings"
	"testing"

	"research-assistant-backend/internal/memory"
)

func TestAcceptanceWikiScopeBindingsAndRelevance(t *testing.T) {
	ctx := context.Background()
	s := NewMemoryPaperStore()
	_, err := s.Put(ctx, PaperPage{PaperID: "p", OwnerID: "o", ProjectID: "rag", Title: "RAG reranker", TLDR: "PROJECT-PRIVATE-RAG", DocumentIDs: []string{"doc"}})
	if err != nil {
		t.Fatal(err)
	}
	b := NewBuilder(s, memory.NewMemoryStore(), nil, nil, NewAliasStore(t.TempDir()))
	for _, q := range []TaskQuery{{OwnerID: "o", Text: "RAG"}, {OwnerID: "other", ProjectID: "rag", Text: "RAG"}, {OwnerID: "o", ProjectID: "other", PaperID: "p"}, {OwnerID: "o", DocumentID: "doc"}, {OwnerID: "o", PaperID: "p"}} {
		q.PromptLimit = 32000
		got, err := b.RetrieveForTask(ctx, q)
		if err != nil || got.Text != "" || len(got.Citations) > 0 {
			t.Fatal("wiki binding leaked", q, got, err)
		}
	}
	for _, q := range []TaskQuery{{OwnerID: "o", ProjectID: "rag", Text: "RAG"}, {OwnerID: "o", ProjectID: "rag", PaperID: "p"}, {OwnerID: "o", ProjectID: "rag", DocumentID: "doc"}, {OwnerID: "o", ProjectID: "rag", Text: "这篇论文"}} {
		q.PromptLimit = 32000
		got, err := b.RetrieveForTask(ctx, q)
		if err != nil || !strings.Contains(got.Text, "PROJECT-PRIVATE-RAG") {
			t.Fatal("positive binding failed", q, got, err)
		}
	}
	got, _ := b.RetrieveForTask(ctx, TaskQuery{OwnerID: "o", ProjectID: "rag", Text: "如何设置 LaTeX 参考文献格式？", PromptLimit: 32000})
	if got.Text != "" {
		t.Fatal("irrelevant sole paper injected")
	}
	zero := 0
	got, _ = b.RetrieveForTask(ctx, TaskQuery{OwnerID: "o", ProjectID: "rag", PaperID: "p", PromptLimit: 32000, ContextBudget: &zero})
	if got.Tokens != 0 || got.Text != "" {
		t.Fatal("zero wiki budget ignored")
	}
}
