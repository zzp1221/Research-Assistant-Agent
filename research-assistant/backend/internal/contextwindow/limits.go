package contextwindow

import (
	"context"
	"fmt"
	"research-assistant-backend/internal/usage"
)

type Limits struct {
	Tokens           int
	CostUSD          float64
	TokensConfigured bool
	CostConfigured   bool
}
type limitsKey struct{}

func WithLimits(ctx context.Context, limits Limits) context.Context {
	if parent, ok := ctx.Value(limitsKey{}).(Limits); ok {
		if parent.TokensConfigured && (!limits.TokensConfigured || parent.Tokens < limits.Tokens) {
			limits.Tokens = parent.Tokens
			limits.TokensConfigured = true
		}
		if parent.CostConfigured && (!limits.CostConfigured || parent.CostUSD < limits.CostUSD) {
			limits.CostUSD = parent.CostUSD
			limits.CostConfigured = true
		}
	}
	return context.WithValue(ctx, limitsKey{}, limits)
}

// CheckAllowance reserves room in the call estimate for both compaction and
// subsequent generation. Unknown native rates cannot spend a finite cost cap.
func CheckAllowance(ctx context.Context, tokens int, cost *float64) error {
	limits, ok := ctx.Value(limitsKey{}).(Limits)
	if !ok {
		return nil
	}
	usedTokens, usedCost := 0, 0.0
	if recorder := usage.FromContext(ctx); recorder != nil {
		used := recorder.Snapshot()
		usedTokens = used.PromptTokens + used.CompletionTokens
		usedCost = used.CostUSD
	}
	if limits.TokensConfigured && (limits.Tokens <= 0 || tokens > limits.Tokens-usedTokens) {
		return fmt.Errorf("context_compaction_budget_exhausted")
	}
	if limits.CostConfigured {
		if cost == nil {
			return fmt.Errorf("context_compaction_cost_unknown")
		}
		if limits.CostUSD <= 0 || *cost > limits.CostUSD-usedCost {
			return fmt.Errorf("context_compaction_budget_exhausted")
		}
	}
	return nil
}
