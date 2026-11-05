package wiki

import (
	"os"
	"strconv"
	"strings"
)

// Enabled reports whether the wiki is enabled. WIKI_ENABLED=false is the
// documented full rollback switch: no read, no write, no injection.
func Enabled() bool {
	raw := strings.TrimSpace(os.Getenv("WIKI_ENABLED"))
	if raw == "" {
		return true
	}
	return !(strings.EqualFold(raw, "false") || raw == "0" || strings.EqualFold(raw, "off"))
}

// Mode returns on | shadow | off.
func Mode() string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("WIKI_MODE")))
	if raw == "" {
		return "on"
	}
	return raw
}

// InjectionEnabled is true only when the wiki is fully on.
func InjectionEnabled() bool {
	return Enabled() && Mode() != "shadow" && Mode() != "off"
}

// LLMMode returns off | summary | links | all (default summary).
func LLMMode() string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("WIKI_LLM_MODE")))
	switch raw {
	case "off", "summary", "links", "all":
		return raw
	}
	return "summary"
}

// LinkMode returns explicit | hybrid | llm (default explicit).
func LinkMode() string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("WIKI_LINK_MODE")))
	switch raw {
	case "explicit", "hybrid", "llm":
		return raw
	}
	return "explicit"
}

// BudgetOverride returns WIKI_BUDGET_TOKENS when set, otherwise 0.
func BudgetOverride() int {
	raw := strings.TrimSpace(os.Getenv("WIKI_BUDGET_TOKENS"))
	if raw == "" {
		return 0
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0
	}
	return value
}

// DocumentContextBudget returns the document-evidence token budget. Default is
// 1200 tokens; 0 disables document-context injection.
func DocumentContextBudget() int {
	raw := strings.TrimSpace(os.Getenv("DOCUMENT_CONTEXT_BUDGET_TOKENS"))
	if raw == "" {
		return 1200
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 1200
	}
	return value
}

// PDFRefsEnabled reports whether the optional PDF references parser is on.
func PDFRefsEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("WIKI_PDF_REFS"))
	return strings.EqualFold(raw, "true") || raw == "1"
}
