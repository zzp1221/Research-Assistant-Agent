package prompts

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"research-assistant-backend/internal/usage"
)

// ErrPromptBudgetExceeded means the mandatory sections (system/plan/task)
// alone exceed the model prompt budget. Callers must fail closed instead of
// silently truncating instructions.
var ErrPromptBudgetExceeded = errors.New("prompt budget exceeded by mandatory sections")

// PromptBudget describes how much prompt room a model call has. ContextWindow
// is the model context window, MaxOutput the reserved completion budget and
// Safety an extra margin for tokenizer drift.
type PromptBudget struct {
	ContextWindow int
	MaxOutput     int
	Safety        int
}

// PromptLimit returns the maximum number of prompt tokens allowed.
func (b PromptBudget) PromptLimit() int {
	if b.ContextWindow <= 0 {
		b.ContextWindow = 128000
	}
	if b.MaxOutput <= 0 {
		b.MaxOutput = 8192
	}
	if b.Safety <= 0 {
		b.Safety = 2000
	}
	limit := b.ContextWindow - b.MaxOutput - b.Safety
	if limit < 1024 {
		limit = 1024
	}
	return limit
}

// DefaultPromptBudget reads the deployment-wide prompt budget. Per-model
// profiles can override ContextWindow via modelprofile.ContextWindowTokens.
func DefaultPromptBudget() PromptBudget {
	return PromptBudget{
		ContextWindow: envInt("PROMPT_CONTEXT_WINDOW", 128000),
		MaxOutput:     envInt("PROMPT_MAX_OUTPUT_TOKENS", 8192),
		Safety:        envInt("PROMPT_SAFETY_TOKENS", 2000),
	}
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// TokenCounter counts tokens for a piece of text. EstimatedCounter is the
// default conservative implementation used when no provider tokenizer is
// available.
type TokenCounter interface {
	Count(text string) int
}

// EstimatedCounter estimates tokens with the shared mixed CJK/English
// heuristic in the usage package.
type EstimatedCounter struct{}

// Count implements TokenCounter.
func (EstimatedCounter) Count(text string) int { return usage.EstimateTokens(text) }

func counterOr(counter TokenCounter) TokenCounter {
	if counter == nil {
		return EstimatedCounter{}
	}
	return counter
}

// Section is one prompt fragment with an explicit priority. Lower priority
// numbers are more important: 0 system, 1 plan, 2 task, 3 primary input,
// 4 secondary input, 5 volatile.
type Section struct {
	Name      string
	Content   string
	Priority  int
	MinTokens int
}

// SectionReport records how one section was handled.
type SectionReport struct {
	Name           string `json:"name"`
	OriginalTokens int    `json:"original_tokens"`
	IncludedTokens int    `json:"included_tokens"`
	Truncated      bool   `json:"truncated,omitempty"`
	Dropped        bool   `json:"dropped,omitempty"`
}

// PromptReport is the machine-readable budget report attached to task metadata.
type PromptReport struct {
	TotalTokens int             `json:"total_tokens"`
	Budget      int             `json:"budget"`
	Sections    []SectionReport `json:"sections,omitempty"`
}

// TruncatedSections returns the names of sections that were truncated/dropped.
func (r PromptReport) TruncatedSections() []string {
	out := make([]string, 0, len(r.Sections))
	for _, section := range r.Sections {
		if section.Truncated || section.Dropped {
			out = append(out, section.Name)
		}
	}
	return out
}

// BuildSectionsWithLimit is BuildSections with an explicit token limit. It is
// used by nested builders (task inputs) that receive an already-computed
// remaining budget.
func BuildSectionsWithLimit(sections []Section, limit int, counter TokenCounter) (string, PromptReport, error) {
	counter = counterOr(counter)
	if limit <= 0 {
		limit = 1024
	}
	report := PromptReport{Budget: limit}

	ordered := make([]Section, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section.Content) == "" {
			continue
		}
		ordered = append(ordered, section)
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Priority < ordered[j].Priority })

	overhead := func(name string) int { return counter.Count("[section: " + name + "]\n") }
	mandatoryTokens := 0
	for _, section := range ordered {
		if section.Priority <= 2 {
			mandatoryTokens += counter.Count(section.Content) + overhead(section.Name)
		}
	}
	if mandatoryTokens > limit {
		return "", report, fmt.Errorf("%w: mandatory=%d limit=%d", ErrPromptBudgetExceeded, mandatoryTokens, limit)
	}

	var builder strings.Builder
	used := 0
	for _, section := range ordered {
		original := counter.Count(section.Content)
		entry := SectionReport{Name: section.Name, OriginalTokens: original}
		sectionOverhead := overhead(section.Name)
		if section.Priority <= 2 {
			entry.IncludedTokens = original
			used += original + sectionOverhead
			writeSection(&builder, section.Name, section.Content, false, 0, 0)
			report.Sections = append(report.Sections, entry)
			continue
		}
		// Reserve room for the section marker and a truncation marker.
		remaining := limit - used - sectionOverhead - 12
		if remaining <= 0 {
			entry.Dropped = true
			report.Sections = append(report.Sections, entry)
			continue
		}
		allowed := original
		if allowed > remaining {
			allowed = remaining
		}
		if allowed < section.MinTokens && remaining >= section.MinTokens {
			allowed = section.MinTokens
		}
		content, truncated := TruncateByTokens(section.Content, allowed, counter)
		if strings.TrimSpace(content) == "" && section.MinTokens == 0 {
			entry.Dropped = true
			report.Sections = append(report.Sections, entry)
			continue
		}
		entry.IncludedTokens = counter.Count(content)
		entry.Truncated = truncated
		used += entry.IncludedTokens + sectionOverhead + 12
		writeSection(&builder, section.Name, content, truncated, entry.IncludedTokens, original)
		report.Sections = append(report.Sections, entry)
	}
	text := strings.TrimSpace(builder.String())
	report.TotalTokens = counter.Count(text)
	return text, report, nil
}

func writeSection(builder *strings.Builder, name, content string, truncated bool, included, original int) {
	if builder.Len() > 0 {
		builder.WriteString("\n\n")
	}
	builder.WriteString("[section: ")
	builder.WriteString(name)
	builder.WriteString("]\n")
	builder.WriteString(content)
	if truncated {
		builder.WriteString(fmt.Sprintf("\n[truncated: %d/%d tokens]", included, original))
	}
}

// TruncateRunes truncates text to at most maxRunes runes without splitting a
// multi-byte character.
func TruncateRunes(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes])
}

// TruncateUTF8Bytes truncates text to at most maxBytes while keeping valid
// UTF-8. It never returns a partial rune.
func TruncateUTF8Bytes(text string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(text) <= maxBytes {
		return text
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

// TruncateByTokens truncates text to at most maxTokens using the supplied
// counter and reports whether truncation happened. The boundary is rune-safe.
func TruncateByTokens(text string, maxTokens int, counter TokenCounter) (string, bool) {
	counter = counterOr(counter)
	if maxTokens <= 0 {
		return "", strings.TrimSpace(text) != ""
	}
	if counter.Count(text) <= maxTokens {
		return text, false
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if counter.Count(string(runes[:mid])) <= maxTokens {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low]), true
}
