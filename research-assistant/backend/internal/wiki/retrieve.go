package wiki

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

// BudgetTokens returns the wiki injection budget for a prompt limit, following
// the documented 2% target / 3% hard cap / 150-1500 clamp.
func BudgetTokens(promptLimit int) int {
	if promptLimit <= 0 {
		return 0
	}
	base := promptLimit * 2 / 100
	hard := promptLimit * 3 / 100
	budget := base
	if budget < 300 {
		budget = 300
	}
	if budget > 1500 {
		budget = 1500
	}
	if budget > hard {
		budget = hard
	}
	if budget < 150 {
		return 0
	}
	return budget
}

// RetrieveForTask finds the papers relevant to a task and renders a bounded
// wiki context block.
func (b *Builder) RetrieveForTask(ctx context.Context, q TaskQuery) (WikiContext, error) {
	if b == nil || b.Store == nil {
		return WikiContext{}, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 3
	}
	papers := b.matchPapers(ctx, q, limit)
	contexts := make([]PaperContext, 0, len(papers))
	for _, page := range papers {
		points, _, err := b.ListPoints(ctx, q.OwnerID, page.PaperID, "", 10, 0)
		if err != nil {
			continue
		}
		filtered := make([]Point, 0, len(points))
		for _, point := range points {
			if !injectablePoint(point) {
				continue
			}
			filtered = append(filtered, point)
		}
		links, _, err := b.ListLinks(ctx, q.OwnerID, page.PaperID, "", string(memory.StatusActive), 5, 0)
		if err != nil {
			links = nil
		}
		contexts = append(contexts, PaperContext{Paper: page, Points: filtered, Links: links})
	}
	return RenderContext(contexts, BudgetTokens(q.PromptLimit)), nil
}

func injectablePoint(point Point) bool {
	record := point.Record
	if record.Status != memory.StatusActive {
		return false
	}
	if record.Trust == memory.TrustUntrusted || record.Evidence.Hidden {
		return false
	}
	return true
}

func (b *Builder) matchPapers(ctx context.Context, q TaskQuery, limit int) []PaperPage {
	pages, _, err := b.Store.List(ctx, PaperQuery{OwnerID: q.OwnerID, ProjectID: q.ProjectID, Status: "active", Limit: 200})
	if err != nil {
		return nil
	}
	type scored struct {
		page  PaperPage
		score int
	}
	results := make([]scored, 0, len(pages))
	text := strings.ToLower(strings.TrimSpace(q.Text))
	for _, page := range pages {
		score := 0
		for _, entity := range q.Entities {
			if paperHasEntity(page, entity) {
				score += 10
			}
		}
		if text != "" {
			haystack := strings.ToLower(page.Title + " " + page.Abstract + " " + page.TLDR + " " + page.DOI + " " + page.ArxivID)
			for _, token := range strings.Fields(text) {
				if len([]rune(token)) >= 2 && strings.Contains(haystack, token) {
					score += 2
				}
			}
		}
		if score > 0 {
			results = append(results, scored{page: page, score: score})
		}
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score == results[j].score {
			return results[i].page.UpdatedAt.After(results[j].page.UpdatedAt)
		}
		return results[i].score > results[j].score
	})
	out := make([]PaperPage, 0, limit)
	for _, result := range results {
		if len(out) >= limit {
			break
		}
		out = append(out, result.page)
	}
	if len(out) == 0 && len(pages) > 0 {
		// Chat frequently says "this paper" / "这篇论文" without naming it.
		// Fall back to the most recently updated papers so a project with one
		// active paper still gets its bounded wiki block instead of silence.
		sort.SliceStable(pages, func(i, j int) bool { return pages[i].UpdatedAt.After(pages[j].UpdatedAt) })
		for _, page := range pages {
			if len(out) >= limit {
				break
			}
			out = append(out, page)
		}
	}
	return out
}

// RenderContext renders paper contexts into the documented wiki XML block,
// truncating to the token budget. Empty output means "do not inject".
func RenderContext(contexts []PaperContext, budget int) WikiContext {
	if budget <= 0 || len(contexts) == 0 {
		return WikiContext{}
	}
	var builder strings.Builder
	citations := make([]memory.Citation, 0, 16)
	paperIDs := make([]string, 0, len(contexts))
	for _, context := range contexts {
		page := context.Paper
		block := renderPaper(context)
		if block == "" {
			continue
		}
		if builder.Len() == 0 {
			block = truncateToTokenBudget(block, budget)
			if block == "" {
				continue
			}
		} else if memory.EstimateTokens(builder.String()+block) > budget {
			break
		}
		builder.WriteString(block)
		paperIDs = append(paperIDs, page.PaperID)
		for _, point := range context.Points {
			citation := memory.Citation{
				MemoryID: point.Record.ID, Title: point.Record.Title, Kind: point.Record.Kind,
				Date: point.Record.CreatedAt.Format("2006-01-02"), Source: "wiki", Trust: point.Record.Trust,
				PaperID: page.PaperID, PointID: point.Record.ID,
			}
			if len(point.Refs) > 0 {
				citation.Page = point.Refs[0].Page
				citation.Section = point.Refs[0].Section
				citation.Quote = point.Refs[0].Quote
			}
			citations = append(citations, citation)
		}
	}
	text := strings.TrimSpace(builder.String())
	return WikiContext{Text: text, Papers: contexts, Citations: citations, PaperIDs: paperIDs, Tokens: memory.EstimateTokens(text)}
}

func renderPaper(context PaperContext) string {
	page := context.Paper
	var builder strings.Builder
	builder.WriteString(`<wiki_paper id="`)
	builder.WriteString(escapeAttr(page.PaperID))
	builder.WriteString(`" title="`)
	builder.WriteString(escapeAttr(page.Title))
	builder.WriteString(`">`)
	builder.WriteString("\n")
	if summary := strings.TrimSpace(page.TLDR); summary != "" {
		builder.WriteString("  <wiki_summary>")
		builder.WriteString(escapeText(summary))
		builder.WriteString("</wiki_summary>\n")
	}
	for _, point := range context.Points {
		ref := memory.SourceRef{}
		if len(point.Refs) > 0 {
			ref = point.Refs[0]
		}
		builder.WriteString("  <wiki_point id=\"")
		builder.WriteString(escapeAttr(point.Record.ID))
		builder.WriteString("\" kind=\"")
		builder.WriteString(string(point.Kind))
		builder.WriteString("\"")
		if ref.Page > 0 {
			builder.WriteString(fmt.Sprintf(" page=\"%d\"", ref.Page))
		}
		if ref.Section != "" {
			builder.WriteString(" section=\"")
			builder.WriteString(escapeAttr(ref.Section))
			builder.WriteString("\"")
		}
		builder.WriteString(" trust=\"")
		builder.WriteString(string(point.Record.Trust))
		builder.WriteString("\">")
		builder.WriteString(escapeText(point.Record.Content))
		builder.WriteString("</wiki_point>\n")
	}
	for _, link := range context.Links {
		builder.WriteString("  <wiki_link type=\"")
		builder.WriteString(string(link.Type))
		builder.WriteString("\" to=\"")
		builder.WriteString(escapeAttr(link.ToPaper))
		builder.WriteString(fmt.Sprintf("\" confidence=\"%.2f\">", link.Confidence))
		builder.WriteString(escapeText(link.Source))
		builder.WriteString("</wiki_link>\n")
	}
	builder.WriteString("</wiki_paper>\n")
	return builder.String()
}

func escapeAttr(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "\"", "&quot;", "<", "&lt;", ">", "&gt;", "\n", " ")
	return replacer.Replace(value)
}

func escapeText(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

// MatchEntities builds query entities from a free-text task description.
func MatchEntities(meta *models.PaperMetadata) []memory.Entity {
	if meta == nil {
		return nil
	}
	entities := make([]memory.Entity, 0, 3)
	if title := strings.TrimSpace(meta.Title); title != "" {
		entities = append(entities, memory.Entity{Type: "paper", Value: strings.ToLower(title)})
	}
	if doi := normalizeDOI(meta.DOI); doi != "" {
		entities = append(entities, memory.Entity{Type: "paper", Value: "doi:" + doi})
	}
	if arxiv := strings.TrimSpace(meta.ArxivID); arxiv != "" {
		entities = append(entities, memory.Entity{Type: "paper", Value: "arxiv:" + strings.ToLower(arxiv)})
	}
	return entities
}

// truncateToTokenBudget cuts text at a line boundary until it fits the budget.
// It guarantees RenderContext never exceeds the documented hard cap.
func truncateToTokenBudget(text string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if memory.EstimateTokens(text) <= budget {
		return text
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if memory.EstimateTokens(string(runes[:mid])) <= budget {
			low = mid
		} else {
			high = mid - 1
		}
	}
	if low <= 0 {
		return ""
	}
	cut := string(runes[:low])
	if index := strings.LastIndex(cut, "\n"); index > 0 {
		cut = cut[:index]
	}
	return strings.TrimSpace(cut)
}
