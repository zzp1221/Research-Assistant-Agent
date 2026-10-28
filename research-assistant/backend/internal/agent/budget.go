package agent

import (
	"os"
	"strconv"
	"strings"

	"research-assistant-backend/internal/prompts"
)

// agentEnvInt reads a positive integer environment setting.
func agentEnvInt(key string, fallback int) int {
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

// truncateToTokens applies a token budget with the shared conservative
// estimator. It never splits a multi-byte character.
func truncateToTokens(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return text
	}
	out, _ := prompts.TruncateByTokens(text, maxTokens, prompts.EstimatedCounter{})
	return out
}

// truncateToRunes applies a rune budget.
func truncateToRunes(text string, maxRunes int) string {
	return prompts.TruncateRunes(text, maxRunes)
}
