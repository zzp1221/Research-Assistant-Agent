package scheduler

import (
	"encoding/json"
	"strings"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
)

// claimEvidenceGraphDecoded decodes the persisted claim evidence graph, if any.
func claimEvidenceGraphDecoded(plan *models.PlanGraph) *models.ClaimEvidenceGraph {
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

// coreTaskIDs is the set of nodes whose failure breaks the plan's core goal.
// Legacy plans have no optional flag, so every node is core.
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

// buildOutcomeDetails explains a terminal evaluation from the SAME acceptance
// evaluation the finalizer used: core goal verdicts, trusted evidence, optional
// losses and the limitations those losses impose. It never re-derives the
// verdict from bare artifact-key existence, because a diagnostic left by a
// failed task is not evidence.
func (s *Scheduler) buildOutcomeDetails(plan *models.PlanGraph, evaluation models.PlanFailureEvaluation) *models.OutcomeDetails {
	if plan == nil {
		return nil
	}
	evidence := planner.EvaluateAcceptanceEvidence(plan)
	details := &models.OutcomeDetails{Mode: "strict"}
	if evidence.HasContract {
		details.Mode = "acceptance"
	}
	// The claim counters describe the SAME graph the verdict used. A graph the
	// acceptance evaluation rejected as untrusted must not be summarised as
	// "3 of 4 claims verified" in the user-facing details (V3-03).
	if graph, _ := planner.TrustedClaimEvidenceGraph(plan); graph != nil {
		details.ClaimsVerified = graph.Summary.Verified
		details.ClaimsTotal = graph.Summary.TotalClaims
	}
	for _, goal := range evidence.GoalResults {
		details.AcceptedGoals = append(details.AcceptedGoals, goal)
		if !goal.Verified {
			for _, key := range goal.EvidenceRefs {
				if _, trusted := planner.TrustedArtifact(plan, key); !trusted {
					details.MissingArtifacts = appendUniqueString(details.MissingArtifacts, key)
				}
			}
		}
	}
	for _, key := range evidence.MissingArtifacts {
		details.MissingArtifacts = appendUniqueString(details.MissingArtifacts, key)
	}

	core := coreTaskIDs(plan)
	for _, node := range plan.Nodes {
		if node == nil || !node.IsOptional() || !node.IsEnabled() || core[node.ID] {
			continue
		}
		switch node.Status {
		case models.StatusFailed, models.StatusBlocked, models.StatusCanceled:
			loss := models.OptionalLoss{TaskID: node.ID, Name: node.Name, Status: node.Status, Reason: node.Error}
			if node.BlockInfo != nil && node.BlockInfo.Code != "" {
				loss.Reason = node.BlockInfo.Code + ": " + node.BlockInfo.Message
			}
			details.OptionalLosses = append(details.OptionalLosses, loss)
		}
	}
	for _, loss := range details.OptionalLosses {
		name := loss.Name
		if strings.TrimSpace(name) == "" {
			name = loss.TaskID
		}
		details.Limitations = append(details.Limitations, "缺少可选工作 "+name+"，相关结论不能宣称完整")
	}
	// A strict plan that fully succeeded needs no explanatory payload.
	if evaluation.Outcome == models.OutcomeCompleted && len(details.OptionalLosses) == 0 && details.Mode == "strict" && len(details.MissingArtifacts) == 0 {
		return nil
	}
	return details
}

func appendUniqueString(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}
