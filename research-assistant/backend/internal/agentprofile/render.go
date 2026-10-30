package agentprofile

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var templateVariablePattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.\-]+)\s*\}\}`)

// ExtractVariables returns the sorted unique {{variable}} names in a template.
func ExtractVariables(template string) []string {
	matches := templateVariablePattern.FindAllStringSubmatch(template, -1)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		name := strings.TrimSpace(match[1])
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// RenderTemplate substitutes {{name}} placeholders. Missing variables are an
// error instead of silently rendering an empty prompt, because a prompt that
// lost its evidence is worse than a failed run.
func RenderTemplate(template string, variables map[string]string) (string, error) {
	if strings.TrimSpace(template) == "" {
		return "", fmt.Errorf("prompt template is empty")
	}
	var missing []string
	seenMissing := map[string]struct{}{}
	rendered := templateVariablePattern.ReplaceAllStringFunc(template, func(match string) string {
		sub := templateVariablePattern.FindStringSubmatch(match)
		name := strings.TrimSpace(sub[1])
		if value, ok := variables[name]; ok {
			return value
		}
		if _, exists := seenMissing[name]; !exists {
			seenMissing[name] = struct{}{}
			missing = append(missing, name)
		}
		return match
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("missing prompt variables: %s", strings.Join(missing, ", "))
	}
	return rendered, nil
}

// StringVariables converts arbitrary task inputs into template variables.
func StringVariables(inputs map[string]any) map[string]string {
	out := map[string]string{}
	for key, value := range inputs {
		if value == nil {
			continue
		}
		switch typed := value.(type) {
		case string:
			out[key] = typed
		case map[string]any, []any:
			continue
		default:
			out[key] = fmt.Sprint(typed)
		}
	}
	return out
}
