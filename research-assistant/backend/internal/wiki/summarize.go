package wiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/memory"
)

// LLM is the narrow completion interface used by W3. The production adapter
// lives in the API layer (modelresolver); tests inject a deterministic fake.
type LLM interface {
	Complete(ctx context.Context, ownerID, system, user string) (string, error)
}

// SummaryEnabled reports whether WIKI_LLM_MODE allows TLDR/point summaries.
func SummaryEnabled() bool {
	mode := LLMMode()
	return mode == "summary" || mode == "all"
}

// LinkAdjudicationEnabled reports whether WIKI_LLM_MODE allows link type
// adjudication.
func LinkAdjudicationEnabled() bool {
	mode := LLMMode()
	return mode == "links" || mode == "all"
}

const summarySystemPrompt = "你是科研论文信息抽取器。只输出严格 JSON，不要输出 Markdown 代码块、前后缀或解释。\n" +
	"JSON schema:\n" +
	"{\"tldr\":\"一句话中文摘要，<=200字\",\"points\":[{\"kind\":\"method|limitation|open_question\",\"title\":\"短标题\",\"text\":\"结论式描述，<=300字\",\"section\":\"章节标题（可空）\",\"quote\":\"必须逐字来自给定原文的摘录\"}]}\n" +
	"规则：\n1. points 最多 6 条；每条必须有 quote；quote 不得改写或编造。\n2. 没有原文依据时不要输出该条；宁可少输出。\n3. kind 只能是 method、limitation、open_question。"

type summaryPointPayload struct {
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Text    string `json:"text"`
	Section string `json:"section"`
	Quote   string `json:"quote"`
}

type summaryPayload struct {
	TLDR   string                `json:"tldr"`
	Points []summaryPointPayload `json:"points"`
}

// maybeSummarizePaper fills TLDR and creates pending summary points from a
// paper's document. It never creates factual points without a verified quote:
// when no document is available only the TLDR is produced.
func (b *Builder) maybeSummarizePaper(ctx context.Context, ownerID string, page *PaperPage, documentID string, document *documents.Document) error {
	if b == nil || page == nil || b.LLM == nil || !SummaryEnabled() {
		return nil
	}
	if strings.TrimSpace(page.TLDR) != "" {
		return nil
	}
	excerpt := ""
	if document != nil {
		excerpt = documents.Excerpt(document)
	}
	if strings.TrimSpace(excerpt) == "" {
		excerpt = strings.TrimSpace(page.Abstract)
	}
	if strings.TrimSpace(excerpt) == "" {
		return nil
	}
	if utf8.RuneCountInString(excerpt) > 8000 {
		excerpt = string([]rune(excerpt)[:8000])
	}
	user := buildSummaryPrompt(*page, excerpt)
	payload, err := b.completeSummaryJSON(ctx, ownerID, user)
	if err != nil {
		return err
	}
	page.TLDR = truncate(payload.TLDR, 300)
	page.TLDRInferred = true
	if err := b.persistTLDR(ctx, ownerID, *page); err != nil {
		return err
	}
	if document == nil || strings.TrimSpace(documentID) == "" {
		return nil
	}
	created := 0
	for _, item := range payload.Points {
		kind := PointKind(strings.TrimSpace(item.Kind))
		if kind != PointMethod && kind != PointLimitation && kind != PointOpenQuestion {
			continue
		}
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		ref := memory.SourceRef{DocumentID: documentID, Section: strings.TrimSpace(item.Section)}
		status := memory.StatusPending
		quote := strings.TrimSpace(item.Quote)
		if quote != "" {
			location := documents.LocateQuoteLocation(document, quote)
			if location.Found {
				ref.Page = location.Page
				ref.BlockID = location.BlockID
				ref.Quote = truncate(quote, 500)
			} else {
				// Never persist an unverified quote: keep the point as pending
				// review without a quote instead of fabricating evidence.
				ref.Quote = ""
			}
		}
		if _, err := b.CreatePoint(ctx, PointInput{
			OwnerID: ownerID, ProjectID: page.ProjectID, PaperID: page.PaperID, Kind: kind,
			Title: firstNonEmpty(item.Title, "LLM 摘要"), Text: truncate(text, 800), Refs: []memory.SourceRef{ref},
			Trust: memory.TrustInferred, Status: status, SourceType: "llm_summary", SourceRef: page.PaperID,
			Importance: 7, Confidence: 0.6, EvidenceClass: "llm_inference",
			Attributes: map[string]any{"point_key": "llm:" + string(kind) + ":" + shortHash(text)},
		}); err != nil {
			continue
		}
		created++
		if created >= 6 {
			break
		}
	}
	return nil
}

func buildSummaryPrompt(page PaperPage, excerpt string) string {
	var builder strings.Builder
	builder.WriteString("论文元数据：\n")
	builder.WriteString("标题：")
	builder.WriteString(page.Title)
	builder.WriteString("\n作者：")
	builder.WriteString(strings.Join(page.Authors, ", "))
	builder.WriteString("\n年份：")
	builder.WriteString(fmt.Sprintf("%d", page.Year))
	builder.WriteString("\n会议：")
	builder.WriteString(page.Venue)
	if strings.TrimSpace(page.Abstract) != "" {
		builder.WriteString("\n摘要：")
		builder.WriteString(truncate(page.Abstract, 2000))
	}
	if len(page.Sections) > 0 {
		builder.WriteString("\n章节：")
		for index, section := range page.Sections {
			if index >= 20 {
				break
			}
			if index > 0 {
				builder.WriteString("；")
			}
			builder.WriteString(section.Title)
		}
	}
	builder.WriteString("\n\n原文摘录（quote 必须逐字来自这里）：\n")
	builder.WriteString(excerpt)
	return builder.String()
}

func (b *Builder) completeSummaryJSON(ctx context.Context, ownerID, user string) (summaryPayload, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := b.LLM.Complete(ctx, ownerID, summarySystemPrompt, user)
		if err != nil {
			lastErr = err
			continue
		}
		payload, parseErr := parseSummaryJSON(raw)
		if parseErr == nil {
			return payload, nil
		}
		lastErr = parseErr
	}
	return summaryPayload{}, fmt.Errorf("llm summary failed after 2 attempts: %w", lastErr)
}

func parseSummaryJSON(raw string) (summaryPayload, error) {
	var payload summaryPayload
	text := strings.TrimSpace(raw)
	fence := "\x60\x60\x60"
	text = strings.TrimPrefix(text, fence+"json")
	text = strings.TrimPrefix(text, fence)
	text = strings.TrimSuffix(text, fence)
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			text = text[start : end+1]
		}
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return summaryPayload{}, err
	}
	payload.TLDR = strings.TrimSpace(payload.TLDR)
	if utf8.RuneCountInString(payload.TLDR) > 500 {
		payload.TLDR = string([]rune(payload.TLDR)[:500])
	}
	points := make([]summaryPointPayload, 0, len(payload.Points))
	for _, point := range payload.Points {
		kind := PointKind(strings.TrimSpace(point.Kind))
		if kind != PointMethod && kind != PointLimitation && kind != PointOpenQuestion {
			continue
		}
		if strings.TrimSpace(point.Text) == "" {
			continue
		}
		point.Kind = string(kind)
		point.Text = truncate(point.Text, 800)
		point.Quote = truncate(strings.TrimSpace(point.Quote), 500)
		points = append(points, point)
	}
	payload.Points = points
	if payload.TLDR == "" && len(payload.Points) == 0 {
		return summaryPayload{}, fmt.Errorf("empty summary payload")
	}
	return payload, nil
}

func (b *Builder) persistTLDR(ctx context.Context, ownerID string, page PaperPage) error {
	if b.Memory == nil || strings.TrimSpace(page.TLDR) == "" {
		return nil
	}
	record := memory.Record{
		ID:    "mem-wiki-tldr-" + page.PaperID,
		Kind:  memory.KindFact,
		Scope: memory.Scope{OwnerID: ownerID, ProjectID: page.ProjectID},
		Title: truncate("TLDR："+page.Title, 120), Content: truncate(page.TLDR, 800),
		Tags:       []string{"wiki_tldr", PaperTag(page.PaperID)},
		Entities:   []memory.Entity{{Type: "paper", Value: page.PaperID}},
		Source:     memory.Source{Type: "llm_summary", Ref: page.PaperID, CreatedAt: time.Now().UTC()},
		Evidence:   memory.Evidence{Class: "llm_summary", Confidence: 0.6},
		Attributes: map[string]any{"paper_id": page.PaperID, "tldr_inferred": true},
		Importance: 8, Confidence: 0.6, Trust: memory.TrustInferred, Status: memory.StatusPending,
	}
	_, err := b.Memory.Put(ctx, record)
	return err
}

// loadTLDR restores a previously generated TLDR from memory so rebuilding the
// wiki directory never calls the LLM again.
func (b *Builder) loadTLDR(ctx context.Context, ownerID, paperID string) (string, bool) {
	if b.Memory == nil {
		return "", false
	}
	records, _, err := b.Memory.List(ctx, memory.Query{OwnerID: ownerID, Tags: []string{"wiki_tldr", PaperTag(paperID)}, IncludePending: true, Limit: 10})
	if err != nil || len(records) == 0 {
		return "", false
	}
	latest := records[0]
	for _, record := range records[1:] {
		if record.UpdatedAt.After(latest.UpdatedAt) {
			latest = record
		}
	}
	if strings.TrimSpace(latest.Content) == "" {
		return "", false
	}
	return strings.TrimSpace(latest.Content), true
}

func shortHash(text string) string {
	sum := sha256.Sum256([]byte(memory.NormalizeText(text)))
	return hex.EncodeToString(sum[:])[:12]
}

// LLMHealth is a small diagnostic used by tests and stats: it reports whether
// the configured LLM mode and adapter can summarize.
func (b *Builder) LLMHealth() (bool, string) {
	if b == nil || b.LLM == nil {
		return false, "llm adapter not configured"
	}
	return SummaryEnabled(), LLMMode()
}
