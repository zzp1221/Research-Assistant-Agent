package memory

import (
	"fmt"
	"strings"

	"research-assistant-backend/internal/usage"
)

// EstimateTokens is the shared mixed CJK/English heuristic.
func EstimateTokens(text string) int { return usage.EstimateTokens(text) }

// RenderPlain is the raw title + content used for hashing and truncation.
func RenderPlain(rec Record) string {
	title := strings.TrimSpace(rec.Title)
	content := strings.TrimSpace(rec.Content)
	if title == "" {
		return content
	}
	if content == "" {
		return title
	}
	return title + "\n" + content
}

// RenderRecord renders one memory block. The tag carries id/kind/date/trust/
// source so the model can cite it and the UI can link back to the record.
func RenderRecord(rec Record) string {
	content := strings.ReplaceAll(RenderPlain(rec), "</memory>", "&lt;/memory&gt;")
	date := rec.UpdatedAt.Format("2006-01-02")
	if rec.Validity.ValidFrom != nil {
		date = rec.Validity.ValidFrom.Format("2006-01-02")
	}
	source := rec.Source.Type
	if rec.Source.Ref != "" {
		source += ":" + rec.Source.Ref
	}
	return fmt.Sprintf("<memory kind=%q id=%q date=%q trust=%q source=%q version=%q status=%q>\n%s\n</memory>",
		string(rec.Kind), rec.ID, date, string(rec.Trust), source, fmt.Sprint(rec.Version), string(rec.Status), content)
}

// RenderRecords renders a list of memory blocks separated by blank lines.
func RenderRecords(records []Record) string {
	blocks := make([]string, 0, len(records))
	for _, rec := range records {
		blocks = append(blocks, RenderRecord(rec))
	}
	return strings.Join(blocks, "\n\n")
}

// truncateToTokens cuts text to a token budget without splitting a rune.
func truncateToTokens(text string, maxTokens int) (string, bool) {
	if maxTokens <= 0 {
		return "", strings.TrimSpace(text) != ""
	}
	if EstimateTokens(text) <= maxTokens {
		return text, false
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if EstimateTokens(string(runes[:mid])) <= maxTokens {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return strings.TrimSpace(string(runes[:low])) + "…", true
}
