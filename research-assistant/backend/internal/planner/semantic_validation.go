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

// Finalize decides PlanOutcome in a fixed order:
//
//  1. an explicit stop keeps its own semantics;
//  2. a core task that failed, blocked, was canceled or never finished means the
//     core goal was not achieved — this is checked BEFORE every success or
//     downgraded-success branch so partial/smoke can never hide it;
//  3. claim-evidence degradation and low-confidence evidence go to needs_review;
//  4. mock/smoke keep their own outcome, but only once core work is verified;
//  5. a declared acceptance contract must be provable from trusted artifacts;
//  6. only then may optional losses produce partial;
//  7. everything verified means completed.
func (f *PlanFinalizer) Finalize(ctx context.Context, plan *models.PlanGraph) error {
	_ = ctx
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	// 1. An explicit stop keeps its own semantics.
	if plan.Status == models.StatusCanceled {
		plan.Outcome = models.OutcomeFailed
		if plan.OutcomeReason == "" {
			plan.OutcomeReason = "plan was canceled"
		}
		return nil
	}
	if plan.Status == models.StatusFailed {
		switch plan.Outcome {
		case models.OutcomeBlocked, models.OutcomeNeedsReview, models.OutcomePartial:
			return nil
		}
		plan.Outcome = models.OutcomeFailed
		if plan.OutcomeReason == "" {
			plan.OutcomeReason = "plan failed"
		}
		return nil
	}

	evidence := EvaluateAcceptanceEvidence(plan)

	// 2. Core work that never achieved its goal outranks every success branch.
	// A real failure is a failure; a block or cancellation is a missing
	// condition, which is what the plan actually ended on.
	if evidence.CoreFailed() {
		plan.Outcome = models.OutcomeFailed
		plan.OutcomeReason = "one or more core tasks failed: " + strings.Join(limitIDs(evidence.CoreFailures), ", ")
		return nil
	}
	if evidence.CoreStopped() {
		plan.Outcome = models.OutcomeBlocked
		plan.OutcomeReason = coreStopReason(evidence)
		return nil
	}
	if evidence.ContractReason != "" {
		plan.Outcome = models.OutcomeNeedsReview
		plan.OutcomeReason = evidence.ContractReason
		return nil
	}

	// 3. Claim evidence is the scientific validity gate (D6). It stays ahead of
	// the smoke shortcut so smoke cannot mask a degraded evidence graph.
	// Only a trusted graph counts: one produced by a failed or superseded task
	// describes an earlier attempt, not this run.
	if _, present := plan.Artifacts["claim_evidence_graph"]; present {
		graph, trustReason := trustedClaimEvidenceGraph(plan)
		if graph == nil {
			plan.Outcome = models.OutcomeNeedsReview
			plan.OutcomeReason = trustReason
			return nil
		}
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

	// 4. mock/smoke keep their own outcome, but only after core work was proven
	// above. Optional losses are still listed in outcome_details.
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

	// 5. A declared acceptance contract must be provable from trusted evidence.
	if evidence.HasContract && !evidence.GoalsVerified() {
		plan.Outcome = models.OutcomeNeedsReview
		plan.OutcomeReason = goalFailureReason(evidence)
		return nil
	}
	if len(evidence.MissingArtifacts) > 0 && evidence.HasContract {
		plan.Outcome = models.OutcomeNeedsReview
		plan.OutcomeReason = "acceptance artifacts are missing or untrusted: " + strings.Join(limitIDs(evidence.MissingArtifacts), ", ")
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

	// 6. Partial requires a declared contract, a proven core goal and at least
	// one lost optional task. Without the contract there is nothing that says a
	// loss is acceptable, so the plan is strictly required and cannot be partial.
	if losses := optionalLosses(plan); len(losses) > 0 {
		if !evidence.HasContract || !evidence.GoalsVerified() {
			plan.Outcome = models.OutcomeNeedsReview
			plan.OutcomeReason = "optional work failed but no acceptance contract proves the core goal: " + strings.Join(losses, ", ")
			return nil
		}
		plan.Outcome = models.OutcomePartial
		plan.OutcomeReason = "core goal accepted with optional losses: " + strings.Join(losses, ", ")
		return nil
	}

	// 7. Everything required completed and the contract (if any) holds.
	plan.Outcome = models.OutcomeCompleted
	plan.OutcomeReason = "core goals and enabled work completed"
	return nil
}

// coreStopReason renders the machine-readable explanation for a plan whose core
// work ended without achieving the goal.
func coreStopReason(evidence AcceptanceEvidence) string {
	parts := []string{}
	if len(evidence.CoreBlocked) > 0 {
		parts = append(parts, "blocked="+strings.Join(limitIDs(evidence.CoreBlocked), ","))
	}
	if len(evidence.CoreCanceled) > 0 {
		parts = append(parts, "canceled="+strings.Join(limitIDs(evidence.CoreCanceled), ","))
	}
	if len(evidence.CoreUnfinished) > 0 {
		parts = append(parts, "unfinished="+strings.Join(limitIDs(evidence.CoreUnfinished), ","))
	}
	if len(evidence.MissingContracts) > 0 {
		parts = append(parts, "missing_required="+strings.Join(limitIDs(evidence.MissingContracts), ","))
	}
	return "core goal not achieved: " + strings.Join(parts, "; ")
}

func goalFailureReason(evidence AcceptanceEvidence) string {
	unverified := []string{}
	for _, goal := range evidence.GoalResults {
		if goal.Verified {
			continue
		}
		label := goal.ID
		if goal.Reason != "" {
			label += " (" + goal.Reason + ")"
		}
		unverified = append(unverified, label)
	}
	if len(unverified) == 0 {
		return "acceptance contract is not proven by evidence"
	}
	return "acceptance goals are not verified: " + strings.Join(limitIDs(unverified), ", ")
}

func limitIDs(values []string) []string {
	const limit = 5
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

// optionalLosses lists the enabled optional tasks that did not complete.
func optionalLosses(plan *models.PlanGraph) []string {
	if plan == nil {
		return nil
	}
	core := coreTaskIDs(plan)
	out := []string{}
	for _, node := range plan.Nodes {
		if node == nil || !node.IsOptional() || !node.IsEnabled() || core[node.ID] {
			continue
		}
		switch node.Status {
		case models.StatusFailed, models.StatusBlocked, models.StatusCanceled:
			label := node.ID
			if strings.TrimSpace(node.Name) != "" {
				label = node.Name + "(" + string(node.Status) + ")"
			}
			out = append(out, label)
		}
	}
	return out
}

// coreTaskIDs is the set of nodes whose failure breaks the plan core. Legacy
// plans (no optional flag) make every node core.
func coreTaskIDs(plan *models.PlanGraph) map[string]bool {
	out := map[string]bool{}
	if plan == nil {
		return out
	}
	for _, node := range plan.Nodes {
		if node == nil || node.IsOptional() {
			continue
		}
		out[node.ID] = true
	}
	if plan.Acceptance != nil {
		for _, id := range plan.Acceptance.RequiredTaskIDs {
			out[id] = true
		}
	}
	return out
}

func findPlanNode(plan *models.PlanGraph, taskID string) *models.TaskNode {
	if plan == nil {
		return nil
	}
	for _, node := range plan.Nodes {
		if node != nil && node.ID == taskID {
			return node
		}
	}
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
