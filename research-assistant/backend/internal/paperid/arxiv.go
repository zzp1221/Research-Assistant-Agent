// Package paperid centralizes paper identifier parsing so the document
// extractor, API router, planner and scheduler cannot drift apart again.
package paperid

import (
	"regexp"
	"strings"
)

// ArxivIDPattern matches modern arXiv identifiers, including an optional
// version suffix ("0803.0476v2") that older regexes silently dropped.
var ArxivIDPattern = regexp.MustCompile(`\b\d{4}\.\d{4,5}(?:v\d+)?\b`)

// Find returns the first arXiv identifier in text without its version suffix.
func Find(text string) string {
	match := ArxivIDPattern.FindString(text)
	return StripVersion(match)
}

// StripVersion removes a trailing vN version marker from an arXiv identifier.
func StripVersion(id string) string {
	id = strings.TrimSpace(id)
	if idx := strings.LastIndex(id, "v"); idx > 0 {
		suffix := id[idx+1:]
		if suffix != "" && strings.Trim(suffix, "0123456789") == "" {
			return id[:idx]
		}
	}
	return id
}
