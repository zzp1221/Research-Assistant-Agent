package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// formatTaskInputsForPrompt serializes upstream inputs for an LLM prompt while
// preventing image bytes and huge payloads from being sent as text. The agents
// still receive the original task.Inputs for execution; only the prompt copy is
// bounded.
func formatTaskInputsForPrompt(inputs map[string]any, perValueLimit int) string {
	if len(inputs) == 0 {
		return ""
	}
	if perValueLimit <= 0 {
		perValueLimit = 4000
	}
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString("- ")
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(previewPromptValue(inputs[key], perValueLimit))
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}

func previewPromptValue(value any, limit int) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case string:
		trimmed := strings.TrimSpace(typed)
		if looksLikeBase64Image(trimmed) {
			return fmt.Sprintf("[image/base64 artifact omitted from prompt: %d chars]", len(typed))
		}
		if len(typed) <= limit {
			return typed
		}
		return typed[:limit] + fmt.Sprintf("...[truncated, %d chars total]", len(typed))
	case []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("[%T omitted]", value)
		}
		if len(encoded) <= limit {
			return string(encoded)
		}
		return string(encoded[:limit]) + "...[truncated]"
	case map[string]any, []string:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("[%T omitted]", value)
		}
		if len(encoded) <= limit {
			return string(encoded)
		}
		return string(encoded[:limit]) + "...[truncated]"
	default:
		text := fmt.Sprint(value)
		if looksLikeBase64Image(strings.TrimSpace(text)) {
			return fmt.Sprintf("[image/base64 artifact omitted from prompt: %d chars]", len(text))
		}
		if len(text) <= limit {
			return text
		}
		return text[:limit] + "...[truncated]"
	}
}

func looksLikeBase64Image(value string) bool {
	if strings.HasPrefix(value, "data:image/") {
		return true
	}
	return strings.HasPrefix(value, "iVBORw0KGgo") || strings.HasPrefix(value, "/9j/")
}
