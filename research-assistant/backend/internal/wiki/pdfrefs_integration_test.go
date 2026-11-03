package wiki

import (
	"context"
	"testing"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/memory"
	uploadspkg "research-assistant-backend/internal/uploads"
)

func TestPDFReferenceCandidatesFromStore(t *testing.T) {
	root := t.TempDir()
	t.Setenv("UPLOAD_ROOT", root)
	t.Setenv("WIKI_PDF_REFS", "true")
	document := &documents.Document{
		Version: documents.Version, UploadID: "doc-refs", Status: documents.StatusExtracted, PageCount: 1,
		Pages: []documents.Page{{Number: 1, Text: "Body.\nReferences\n[1] A. Vaswani. Attention Is All You Need. NeurIPS, 2017. arXiv:1706.03762.\n[2] J. Devlin. BERT: Pre-training of Deep Bidirectional Transformers. NAACL, 2019. doi:10.18653/v1/N19-1423."}},
	}
	if err := documents.Save(uploadspkg.Directory("u1", "doc-refs"), document); err != nil {
		t.Fatalf("save document: %v", err)
	}
	builder := NewBuilder(NewMemoryPaperStore(), memory.NewMemoryStore(), documents.NewStore(""), nil, NewAliasStore(t.TempDir()))
	page := PaperPage{PaperID: "paper-self", OwnerID: "u1", Title: "Self", DocumentIDs: []string{"doc-refs"}}
	pages := []PaperPage{
		page,
		{PaperID: "paper-a", Title: "Attention Is All You Need", Year: 2017, ArxivID: "1706.03762"},
		{PaperID: "paper-b", Title: "BERT: Pre-training of Deep Bidirectional Transformers", Year: 2019, DOI: "10.18653/v1/N19-1423"},
	}
	candidates := builder.pdfReferenceCandidates(context.Background(), "u1", page, pages)
	if len(candidates) != 2 {
		t.Fatalf("candidates=%d want 2: %+v", len(candidates), candidates)
	}
}
