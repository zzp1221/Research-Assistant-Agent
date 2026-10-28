package prompts

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// InputPolicy bounds how much of one task input value may appear in a prompt.
// Values larger than MaxValueRunes are truncated before the global budget is
// applied, which keeps a single huge artifact from crowding out all others.
type InputPolicy struct {
	MaxValueRunes int
}

// DefaultInputPolicy is the default per-value cap (about 24k Chinese chars).
func DefaultInputPolicy() InputPolicy { return InputPolicy{MaxValueRunes: 24000} }

// FormatTaskInputsForPrompt serializes task inputs deterministically (keys
// sorted) under a global token budget. Image/base64 payloads are always
// omitted. It returns the rendered block plus a per-input budget report.
func FormatTaskInputsForPrompt(inputs map[string]any, maxTokens int, counter TokenCounter) (string, PromptReport) {
	return FormatTaskInputsForPromptWithPolicy(inputs, maxTokens, counter, DefaultInputPolicy())
}

// FormatTaskInputsForPromptWithPolicy is FormatTaskInputsForPrompt with an
// explicit per-value policy.
func FormatTaskInputsForPromptWithPolicy(inputs map[string]any, maxTokens int, counter TokenCounter, policy InputPolicy) (string, PromptReport) {
	counter = counterOr(counter)
	if len(inputs) == 0 {
		return "", PromptReport{Budget: maxTokens}
	}
	if policy.MaxValueRunes <= 0 {
		policy.MaxValueRunes = DefaultInputPolicy().MaxValueRunes
	}
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	sections := make([]Section, 0, len(keys))
	for _, key := range keys {
		value := inputs[key]
		if value == nil {
			continue
		}
		rendered := renderPromptValue(value, policy.MaxValueRunes)
		if strings.TrimSpace(rendered) == "" {
			continue
		}
		sections = append(sections, Section{
			Name:     "input:" + key,
			Content:  "- " + key + ": " + rendered,
			Priority: 3,
		})
	}
	if len(sections) == 0 {
		return "", PromptReport{Budget: maxTokens}
	}
	text, report, err := BuildSectionsWithLimit(sections, maxTokens, counter)
	if err != nil {
		// Input sections are optional; an impossible budget yields an empty block
		// rather than failing the task.
		return "", PromptReport{Budget: maxTokens}
	}
	return text, report
}

func renderPromptValue(value any, maxRunes int) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		trimmed := strings.TrimSpace(typed)
		if looksLikeBase64Image(trimmed) {
			return fmt.Sprintf("[image/base64 artifact omitted from prompt: %d chars]", utf8.RuneCountInString(typed))
		}
		if utf8.RuneCountInString(typed) <= maxRunes {
			return typed
		}
		return TruncateRunes(typed, maxRunes) + fmt.Sprintf("...[truncated, %d chars total]", utf8.RuneCountInString(typed))
	case []any, map[string]any, []string:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("[%T omitted]", value)
		}
		text := string(encoded)
		if utf8.RuneCountInString(text) <= maxRunes {
			return text
		}
		return TruncateRunes(text, maxRunes) + "...[truncated]"
	default:
		text := fmt.Sprint(value)
		if looksLikeBase64Image(strings.TrimSpace(text)) {
			return fmt.Sprintf("[image/base64 artifact omitted from prompt: %d chars]", utf8.RuneCountInString(text))
		}
		if utf8.RuneCountInString(text) <= maxRunes {
			return text
		}
		return TruncateRunes(text, maxRunes) + "...[truncated]"
	}
}

func looksLikeBase64Image(value string) bool {
	if strings.HasPrefix(value, "data:image/") {
		return true
	}
	return strings.HasPrefix(value, "iVBORw0KGgo") || strings.HasPrefix(value, "/9j/")
}
