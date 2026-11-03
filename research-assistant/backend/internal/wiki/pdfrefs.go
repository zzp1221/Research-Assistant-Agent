package wiki

import (
	"context"
	"log"
	"strings"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/paperid"
)

// pdfReferenceCandidates parses the paper's own PDF references and matches them
// against known PaperPages. It is only enabled by WIKI_PDF_REFS=true.
func (b *Builder) pdfReferenceCandidates(ctx context.Context, ownerID string, page PaperPage, pages []PaperPage) []LinkCandidate {
	if !PDFRefsEnabled() || b == nil || b.Documents == nil || len(page.DocumentIDs) == 0 {
		return nil
	}
	out := make([]LinkCandidate, 0, 16)
	for _, documentID := range page.DocumentIDs {
		document, err := b.Documents.GetDocument(ctx, ownerID, documentID)
		if err != nil {
			log.Printf("[wiki] pdf refs load %s failed: %v", documentID, err)
			continue
		}
		entries := documents.ExtractReferences(document)
		out = append(out, b.referenceCandidates(page, pages, documentID, entries)...)
	}
	return out
}

// referenceCandidates is the pure matching step, separated for unit tests.
func (b *Builder) referenceCandidates(page PaperPage, pages []PaperPage, documentID string, entries []documents.ReferenceEntry) []LinkCandidate {
	out := make([]LinkCandidate, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		target, ok := matchReferenceEntry(entry, page.PaperID, pages)
		if !ok || seen[target.PaperID] {
			continue
		}
		seen[target.PaperID] = true
		out = append(out, LinkCandidate{
			FromPaper: page.PaperID, ToPaper: target.PaperID, Type: LinkCites,
			Evidence: []memory.SourceRef{{
				DocumentID: documentID,
				Page:       entry.Page,
				Section:    "references",
				Quote:      truncate(entry.Raw, 500),
			}},
			Source: "pdf_references", Confidence: 0.95,
		})
	}
	return out
}

// matchReferenceEntry resolves one reference entry to a known PaperPage using
// the documented identity priority: DOI > arXiv > normalized title + year.
func matchReferenceEntry(entry documents.ReferenceEntry, selfPaperID string, pages []PaperPage) (PaperPage, bool) {
	doi := normalizeDOI(entry.DOI)
	arxiv := strings.ToLower(paperid.StripVersion(entry.ArxivID))
	title := literature.NormalizeTitle(entry.Title)
	for _, page := range pages {
		if page.PaperID == selfPaperID {
			continue
		}
		if doi != "" && normalizeDOI(page.DOI) == doi {
			return page, true
		}
		if arxiv != "" && strings.EqualFold(paperid.StripVersion(page.ArxivID), arxiv) {
			return page, true
		}
		if title != "" && literature.NormalizeTitle(page.Title) == title {
			if entry.Year == 0 || page.Year == 0 || absInt(entry.Year-page.Year) <= 1 {
				return page, true
			}
		}
	}
	return PaperPage{}, false
}
