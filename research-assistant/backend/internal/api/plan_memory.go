package api

import (
	"context"
	"sort"
	"strings"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

func isFollowUpIntent(intent string) bool {
	lower := strings.ToLower(strings.TrimSpace(intent))
	for _, marker := range []string{"继续", "再详细", "基于上面", "基于刚才", "补充", "重做", "接着", "上一个", "上次", "continue", "more detail", "follow up", "same as above"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// findParentPlan returns the most recently updated plan in the same session.
func (r *planRuntime) findParentPlan(ownerID, sessionID string) *models.PlanGraph {
	if strings.TrimSpace(sessionID) == "" || r == nil || r.store == nil {
		return nil
	}
	all, err := r.store.ListPlans()
	if err != nil {
		return nil
	}
	candidates := make([]*models.PlanGraph, 0)
	for _, plan := range all {
		if plan == nil || plan.OwnerID != ownerID || plan.SessionID != sessionID {
			continue
		}
		candidates = append(candidates, plan)
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].UpdatedAt.After(candidates[j].UpdatedAt) })
	return candidates[0]
}

func summarizePlanForContext(plan *models.PlanGraph) string {
	if plan == nil {
		return ""
	}
	var builder strings.Builder
	title := strings.TrimSpace(plan.Title)
	if title == "" {
		title = truncateRunes(strings.TrimSpace(plan.UserIntent), 80)
	}
	builder.WriteString("Title: ")
	builder.WriteString(title)
	builder.WriteString("\nIntent: ")
	builder.WriteString(truncateRunes(strings.TrimSpace(plan.UserIntent), 200))
	builder.WriteString("\nOutcome: ")
	outcome := string(plan.Outcome)
	if outcome == "" {
		outcome = "pending"
	}
	builder.WriteString(outcome)
	if strings.TrimSpace(plan.OutcomeReason) != "" {
		builder.WriteString(" (")
		builder.WriteString(truncateRunes(plan.OutcomeReason, 120))
		builder.WriteString(")")
	}
	if len(plan.Artifacts) > 0 {
		keys := make([]string, 0, len(plan.Artifacts))
		for key := range plan.Artifacts {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if len(keys) > 6 {
			keys = keys[:6]
		}
		builder.WriteString("\nArtifacts: ")
		builder.WriteString(strings.Join(keys, ", "))
	}
	return truncateRunes(builder.String(), 1200)
}

func collectPlanMemoryContext(ownerID, projectID, sessionID, intent string) ([]string, string) {
	store := MemoryStore()
	if store == nil {
		return nil, ""
	}
	settings := memory.DefaultSettings()
	if MemorySettings() != nil {
		settings = MemorySettings().Get(ownerID)
	}
	mode := memory.EffectiveMode(settings)
	if mode == "off" {
		return nil, ""
	}
	q := memory.Query{
		OwnerID: ownerID, ProjectID: projectID, SessionID: sessionID,
		Text: memory.BuildQuery(intent, nil, 2000), Limit: settings.RetrievalTopK,
	}
	got, err := store.Retrieve(context.Background(), q, settings.CoreBudgetTokens, 800)
	if err != nil || mode == "shadow" {
		return nil, ""
	}
	refs := make([]string, 0, len(got.Citations))
	for _, citation := range got.Citations {
		refs = append(refs, citation.MemoryID)
	}
	text := strings.TrimSpace(strings.Join([]string{got.CoreText, got.RetrievedText}, "\n"))
	return refs, truncateRunes(text, 1600)
}
