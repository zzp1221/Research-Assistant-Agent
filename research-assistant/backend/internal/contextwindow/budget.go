package contextwindow

import (
	"fmt"
	"math"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/prompts"
)

type Budget struct {
	prompts.PromptBudget
	Trigger int `json:"trigger"`
}

func ResolveBudget(p modelprofile.Profile, output int) (Budget, error) {
	if err := p.ValidateContext(); err != nil {
		return Budget{}, err
	}
	if output <= 0 {
		output = p.MaxOutputTokens
	}
	if output <= 0 {
		output = prompts.DefaultPromptBudget().MaxOutput
	}
	if p.MaxOutputTokens > 0 && output > p.MaxOutputTokens {
		return Budget{}, fmt.Errorf("invalid_context_budget: requested output exceeds configured maximum")
	}
	b := prompts.PromptBudget{ContextWindow: p.ContextWindowTokens, MaxOutput: output, Safety: int(math.Ceil(float64(p.ContextWindowTokens) * .05))}
	if err := b.Validate(); err != nil {
		return Budget{}, err
	}
	return Budget{PromptBudget: b, Trigger: int(float64(b.PromptLimit()) * p.CompactionTriggerRatio)}, nil
}
