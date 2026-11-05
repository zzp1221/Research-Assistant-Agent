package api

import (
	"regexp"
	"strings"

	"research-assistant-backend/internal/memory"
)

// Answer evidence is a checked verbatim excerpt, not a promise that every
// generated conclusion is entailed by a retrieved candidate.
var answerEvidencePattern = regexp.MustCompile(`\[\[memory:([^|\]\r\n]+)\|([^\]\r\n]+)\]\]`)

func checkedAnswerEvidence(answer string, sources []memory.Citation) (string, []memory.Citation) {
	unknown := false
	lead := strings.ToLower(strings.TrimLeft(strings.TrimSpace(answer), "*#> "))
	for _, prefix := range []string{"不知道", "未找到", "没有找到", "无记录", "无法确认", "暂无记录", "unknown", "i don't know", "i do not know"} {
		if strings.HasPrefix(lead, prefix) {
			unknown = true
			break
		}
	}
	available := map[string]memory.Citation{}
	for _, source := range sources {
		available[source.MemoryID] = source
	}
	var citations []memory.Citation
	seen := map[string]bool{}
	answer = answerEvidencePattern.ReplaceAllStringFunc(answer, func(marker string) string {
		match := answerEvidencePattern.FindStringSubmatch(marker)
		id, quote := strings.TrimSpace(match[1]), strings.TrimSpace(match[2])
		source, ok := available[id]
		if !ok || len([]rune(quote)) < 4 || !strings.Contains(source.Quote, quote) {
			return ""
		}
		// A refusal/unknown conclusion has no positive answer evidence. Extra
		// excerpts the model volunteered must not turn into its supporting refs.
		if unknown {
			return "“" + quote + "”"
		}
		if key := id + "|" + quote; !seen[key] {
			source.Quote = quote
			citations = append(citations, source)
			seen[key] = true
		}
		return "“" + quote + "”"
	})
	return answer, citations
}
