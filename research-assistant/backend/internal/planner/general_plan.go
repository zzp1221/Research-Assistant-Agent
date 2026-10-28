package planner

import (
	"os"
	"strings"

	"research-assistant-backend/internal/models"
)

// generalPlanShape is the deterministic shape of a General plan. The shape is
// decided by rules, never by an LLM, so the same request always produces the
// same plan.
type generalPlanShape string

const (
	generalPlanDirect     generalPlanShape = "direct"
	generalPlanResearch   generalPlanShape = "research"
	generalPlanPaper      generalPlanShape = "paper"
	generalPlanLiterature generalPlanShape = "literature"
)

// generalPlanFastPathEnabled reports whether the fast-path shapes are active.
// GENERAL_PLAN_FAST_PATH=false restores the legacy 3-node default.
func generalPlanFastPathEnabled() bool {
	raw := strings.TrimSpace(os.Getenv("GENERAL_PLAN_FAST_PATH"))
	if raw == "" {
		return true
	}
	return !strings.EqualFold(raw, "false") && raw != "0"
}

// classifyGeneralPlanShape selects the plan shape for a General request.
func classifyGeneralPlanShape(intent models.IntentContext) generalPlanShape {
	if isLiteratureDiscoveryIntent(intent) {
		return generalPlanLiterature
	}
	if isPaperAnalysisIntent(intent) {
		return generalPlanPaper
	}
	if generalNeedsResearch(intent) {
		return generalPlanResearch
	}
	return generalPlanDirect
}

// generalNeedsResearch reports whether a General request needs a background
// research stage. The keyword list intentionally contains request actions
// ("总结/调研/报告/对比") rather than topic words such as "rag" or
// "query rewrite", which caused the old false positives.
func generalNeedsResearch(intent models.IntentContext) bool {
	if boolEntity(intent.Entities, "needs_research") || boolEntity(intent.Entities, "needs_report") {
		return true
	}
	if intent.Entities["paper_metadata"] != nil {
		return true
	}
	if _, ok := intent.Entities["uploaded_files"]; ok {
		return true
	}
	lower := strings.ToLower(intent.RawIntent)
	return hasAny(lower,
		"总结", "综述", "调研", "报告", "梳理", "对比",
		"summary", "survey", "review", "report", "research", "compare",
	)
}

// countGeneralLLMNodes counts nodes that invoke an LLM in a General plan.
func countGeneralLLMNodes(nodes []*models.TaskNode) int {
	llmTypes := map[string]struct{}{
		"general_synthesis": {},
		"general_research":  {},
		"general_process":   {},
		"paper_parse":       {},
		"verify_result":     {},
		"literature_search": {},
	}
	count := 0
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if _, ok := llmTypes[node.Type]; ok {
			count++
		}
	}
	return count
}

// documentBindingsIfPresent binds the plan context document/paper when the
// request carries an attachment or paper metadata.
func documentBindingsIfPresent(intent models.IntentContext) []models.InputBinding {
	if intent.Entities["paper_metadata"] == nil {
		if _, ok := intent.Entities["uploaded_files"]; !ok {
			return nil
		}
	}
	return []models.InputBinding{
		{Target: "document_id", Source: models.BindingSource{Kind: "context_document"}, Required: false},
		{Target: "paper_metadata", Source: models.BindingSource{Kind: "context_paper"}, Required: false},
	}
}
