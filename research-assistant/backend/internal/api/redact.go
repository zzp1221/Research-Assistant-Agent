package api

import (
	"regexp"
	"strings"
)

// sensitiveKeyPattern matches map keys that must never leave the process in an
// export bundle. The match is intentionally broad: a redacted extra field is
// harmless, a leaked credential is not.
var sensitiveKeyPattern = regexp.MustCompile(`(?i)^(authorization|proxy-authorization|api[_-]?key|apikey|access[_-]?key|secret|client[_-]?secret|password|passwd|token|access[_-]?token|refresh[_-]?token|private[_-]?key|session[_-]?key|cookie|set[_-]?cookie)$`)

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]{6,}`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`gho_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret)\s*[:=]\s*[^\s"',;]{6,}`),
}

// redactString removes credential-looking substrings from free text.
func redactString(value string) string {
	for _, pattern := range redactionPatterns {
		value = pattern.ReplaceAllString(value, "[REDACTED]")
	}
	return value
}

// redactJSONValue recursively redacts sensitive keys and credential-like
// strings in decoded JSON. Depth is bounded so a hostile payload cannot cause
// unbounded recursion.
func redactJSONValue(value any, depth int) any {
	if depth > 32 {
		return value
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if sensitiveKeyPattern.MatchString(strings.TrimSpace(key)) {
				out[key] = "[REDACTED]"
				continue
			}
			out[key] = redactJSONValue(item, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for index, item := range typed {
			out[index] = redactJSONValue(item, depth+1)
		}
		return out
	case string:
		return redactString(typed)
	default:
		return value
	}
}
