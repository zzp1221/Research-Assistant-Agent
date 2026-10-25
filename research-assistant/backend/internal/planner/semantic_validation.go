package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"research-assistant-backend/internal/models"
)

// ValidatePlanContextCoverage ensures required paper attachments/documents are
// actually referenced by at least one node. Optional attachments are allowed
// and recorded in the context.
func ValidatePlanContextCoverage(plan *models.PlanGraph, ctx *models.ResearchContext) error {
	if plan == nil || ctx == nil || len(ctx.Attachments) == 0 {
		return nil
	}
	missing := make([]models.MissingRequirement, 0)
	for _, attachment := range ctx.Attachments {
		if attachment.Role != "paper" {
			continue
		}
		if !planReferencesPaperContext(plan) {
			missing = append(missing, models.MissingRequirement{
				Kind:        "attachment",
				Name:        attachment.Name,
				Detail:      "论文附件没有被任何节点通过 InputBindings 或 RequiredArtifacts 引用",
				Remediation: "请确认计划包含 paper_parse 节点并绑定 document_id/paper_metadata。",
			})
		}
	}
	if len(missing) > 0 {
		plan.MissingRequirements = append(plan.MissingRequirements, missing...)
		return fmt.Errorf("plan context coverage failed: %d required paper attachment(s) are not referenced", len(missing))
	}
	return nil
}

func planReferencesPaperContext(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		for _, binding := range node.InputBindings {
			switch binding.Source.Kind {
			case "context_attachment", "context_document", "context_paper":
				return true
			}
		}
		for _, artifact := range node.RequiredArtifacts {
			if artifact == "paper_metadata" || artifact == "parsed_paper" || artifact == "paper_summary" {
				return true
			}
		}
	}
	return false
}

// ValidateFinalAnswer enforces that the final answer is a real answer, not a
// meta evaluation report or a "paper not provided" placeholder.
func ValidateFinalAnswer(plan *models.PlanGraph) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	answer := finalAnswerText(plan)
	if strings.TrimSpace(answer) == "" {
		return fmt.Errorf("final answer is empty")
	}
	if plan.IntentType == "General" && plan.Context != nil && plan.Context.Paper != nil {
		lower := strings.ToLower(answer)
		for _, forbidden := range []string{
			"尚未提供具体的论文标题",
			"未提供具体的论文标题",
			"当前对话中尚未提供",
			"无法对该论文进行实质性内容解析",
			"论文标识信息完备度",
			"无法实质分析",
		} {
			if strings.Contains(lower, strings.ToLower(forbidden)) {
				return fmt.Errorf("final answer is a missing-paper template: %q", forbidden)
			}
		}
		if len([]rune(answer)) < 100 {
			return fmt.Errorf("final answer is too short to be a paper analysis")
		}
	}
	return nil
}

func finalAnswerText(plan *models.PlanGraph) string {
	if plan == nil {
		return ""
	}
	for _, key := range []string{"general_response", "final_answer"} {
		if artifact, ok := plan.Artifacts[key]; ok {
			if value := strings.TrimSpace(artifact.EffectiveValue()); value != "" {
				return value
			}
		}
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		if node.Type == "general_synthesis" || node.Type == "general_process" {
			if strings.TrimSpace(node.Result) != "" {
				return node.Result
			}
		}
	}
	return ""
}

// PlanFinalizer computes PlanGraph.Outcome after execution. It never changes
// TaskStatus, preserving scheduler dependency semantics.
type PlanFinalizer struct{}

func (f *PlanFinalizer) Finalize(ctx context.Context, plan *models.PlanGraph) error {
	_ = ctx
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	if plan.Status == models.StatusFailed || plan.Status == models.StatusCanceled {
		plan.Outcome = models.OutcomeFailed
		return nil
	}
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusFailed {
			plan.Outcome = models.OutcomeFailed
			plan.OutcomeReason = "one or more nodes failed"
			return nil
		}
	}
	// Claim evidence is the scientific validity gate: a degraded or empty
	// graph must never be presented as a successful reproduction. This check
	// runs before the smoke shortcut so smoke cannot mask it (D6).
	if graph := claimEvidenceGraph(plan); graph != nil {
		if graph.Status != "assessed" {
			plan.Outcome = models.OutcomeNeedsReview
			plan.OutcomeReason = "claim evidence graph is " + graph.Status + ": " + graph.StatusReason
			return nil
		}
		if graph.Summary.Verified == 0 && len(graph.Claims) > 0 {
			plan.Outcome = models.OutcomeNeedsReview
			plan.OutcomeReason = "no claim was verified against evidence"
			return nil
		}
	}
	if plan.IntentType == "Framework_Evaluation" && isMockFrameworkPlan(plan) {
		plan.Outcome = models.OutcomeMock
		plan.OutcomeReason = "framework evaluation is an offline mock benchmark"
		return nil
	}
	if plan.IntentType == "Paper_Reproduction" && isSmokePlan(plan) {
		plan.Outcome = models.OutcomeSmoke
		plan.OutcomeReason = "paper reproduction ran in smoke mode"
		return nil
	}
	if plan.Context != nil && len(plan.Context.Attachments) > 0 {
		if err := ValidatePlanContextCoverage(plan, plan.Context); err != nil {
			plan.Outcome = models.OutcomeNeedsReview
			plan.OutcomeReason = err.Error()
			return nil
		}
	}
	if plan.IntentType == "General" {
		if err := ValidateFinalAnswer(plan); err != nil {
			plan.Outcome = models.OutcomeNeedsReview
			plan.OutcomeReason = err.Error()
			return nil
		}
	}
	plan.Outcome = models.OutcomeCompleted
	return nil
}

// claimEvidenceGraph decodes the persisted claim evidence graph, if any.
func claimEvidenceGraph(plan *models.PlanGraph) *models.ClaimEvidenceGraph {
	if plan == nil {
		return nil
	}
	artifact, ok := plan.Artifacts["claim_evidence_graph"]
	if !ok {
		return nil
	}
	raw := strings.TrimSpace(artifact.EffectiveValue())
	if raw == "" {
		return nil
	}
	var graph models.ClaimEvidenceGraph
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		return nil
	}
	return &graph
}

func isSmokePlan(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	for _, key := range []string{"reproduction_mode_report", "reproduction_mode"} {
		artifact, ok := plan.Artifacts[key]
		if !ok {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(artifact.EffectiveValue()), &payload) == nil {
			mode := strings.ToLower(fmt.Sprint(payload["effective_mode"]))
			if mode == "" {
				mode = strings.ToLower(fmt.Sprint(payload["requested_mode"]))
			}
			if mode == "smoke" {
				return true
			}
		}
	}
	return false
}

func isMockFrameworkPlan(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	for _, key := range []string{"framework_metrics_1", "framework_metrics_2"} {
		artifact, ok := plan.Artifacts[key]
		if !ok {
			continue
		}
		var payload map[string]any
		if json.Unmarshal([]byte(artifact.EffectiveValue()), &payload) == nil {
			if strings.Contains(strings.ToLower(fmt.Sprint(payload["mode"])), "mock") {
				return true
			}
		}
	}
	return false
}
