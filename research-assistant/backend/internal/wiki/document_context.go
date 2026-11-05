package wiki

import (
	"context"
	"fmt"
	"strings"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/memory"
)

// DocumentContext is the deterministic document evidence block appended to the
// wiki context for chat answers. It contains an outline, table captions,
// reference count and the top BM25 chunks with page/section provenance.
type DocumentContext struct {
	Text      string
	Citations []memory.Citation
	Tokens    int
}

func (b *Builder) buildDocumentContext(ctx context.Context, ownerID string, papers []PaperPage, query string, budget, limit int) DocumentContext {
	result := DocumentContext{}
	if b == nil || b.Documents == nil || budget <= 0 {
		return result
	}
	if limit <= 0 {
		limit = 2
	}
	remaining := budget
	for _, page := range papers {
		if len(page.DocumentIDs) == 0 {
			continue
		}
		document, err := b.Documents.GetDocument(ctx, ownerID, page.DocumentIDs[0])
		if err != nil || document == nil {
			continue
		}
		block, citations := renderDocumentContext(page, document, query, remaining, limit)
		if strings.TrimSpace(block) == "" {
			continue
		}
		if result.Text != "" {
			result.Text += "\n"
		}
		result.Text += block
		result.Citations = append(result.Citations, memory.Citation{
			MemoryID: "document:" + document.UploadID, Title: page.Title, Kind: memory.KindDocument,
			Date: page.UpdatedAt.Format("2006-01-02"), Source: "document", Trust: memory.TrustVerified,
			PaperID: page.PaperID, DocumentID: document.UploadID, Page: 1, Section: "document",
			Quote: "document:" + document.UploadID,
		})
		result.Citations = append(result.Citations, citations...)
		remaining = budget - memory.EstimateTokens(result.Text)
		if remaining <= 0 {
			break
		}
	}
	result.Tokens = memory.EstimateTokens(result.Text)
	return result
}

func renderDocumentContext(page PaperPage, document *documents.Document, query string, budget, limit int) (string, []memory.Citation) {
	if document == nil {
		return "", nil
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "<document id=\"%s\" pages=\"%d\" title=\"%s\">\n", escapeAttr(document.UploadID), document.PageCount, escapeAttr(page.Title))
	sections := documents.Outline(document)
	if len(sections) > 0 {
		builder.WriteString("<outline>\n")
		for index, section := range sections {
			if index >= 40 {
				break
			}
			fmt.Fprintf(&builder, "- %s (p%d-%d)\n", escapeText(section.Title), section.StartPage, section.EndPage)
		}
		builder.WriteString("</outline>\n")
	}
	queryLower := strings.ToLower(query)
	tableQuery := strings.Contains(queryLower, "table") || strings.Contains(queryLower, "表")
	if len(document.Tables) > 0 {
		limitTables := 5
		if tableQuery {
			limitTables = len(document.Tables)
		}
		builder.WriteString("<tables>\n")
		for index, table := range document.Tables {
			if index >= limitTables {
				break
			}
			caption := strings.TrimSpace(table.Caption)
			if caption == "" {
				caption = fmt.Sprintf("Table %d", index+1)
			}
			fmt.Fprintf(&builder, "- %s (p%d)\n", escapeText(caption), table.Page)
		}
		builder.WriteString("</tables>\n")
	}
	references := documents.ExtractReferences(document)
	if len(references) > 0 {
		fmt.Fprintf(&builder, "<references count=\"%d\"/>\n", len(references))
		if strings.Contains(queryLower, "reference") || strings.Contains(queryLower, "参考文献") || strings.Contains(queryLower, "文献") {
			for index, reference := range references {
				if index >= 3 {
					break
				}
				fmt.Fprintf(&builder, "<reference index=\"%d\" page=\"%d\">%s</reference>\n", reference.Index, reference.Page, escapeText(truncate(reference.Raw, 300)))
			}
		}
	}
	chunks := documents.RankDocument(document, query, documents.SearchOptions{TopK: limit})
	citations := make([]memory.Citation, 0, len(chunks))
	for _, chunk := range chunks {
		if strings.TrimSpace(chunk.Text) == "" {
			continue
		}
		fmt.Fprintf(&builder, "<evidence page=\"%d\" section=\"%s\">%s</evidence>\n", chunk.Page, escapeAttr(chunk.Section), escapeText(truncate(chunk.Text, 900)))
		citations = append(citations, memory.Citation{
			MemoryID: chunk.ID, Title: page.Title, Kind: memory.KindDocument,
			Date: page.UpdatedAt.Format("2006-01-02"), Source: "document", Trust: memory.TrustVerified,
			PaperID: page.PaperID, DocumentID: document.UploadID, Page: chunk.Page,
			Section: chunk.Section, Quote: truncate(chunk.Text, 500),
		})
	}
	builder.WriteString("</document>\n")
	text := truncateToTokenBudget(strings.TrimSpace(builder.String()), budget)
	if text == "" {
		return "", nil
	}
	return text, citations
}
