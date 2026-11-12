package planner

import (
	"strings"

	"research-assistant-backend/internal/models"
)

// AcceptanceEvidence is the single evaluation of a plan's acceptance state. The
// finalizer and the scheduler's outcome_details both consume it, so the two can
// never disagree about whether a core goal was actually proven.
type AcceptanceEvidence struct {
	// CoreFailures are core tasks that failed.
	CoreFailures []string
	// CoreBlocked are core tasks that stopped on a missing condition.
	CoreBlocked []string
	// CoreCanceled are core tasks that were canceled without completing.
	CoreCanceled []string
	// CoreUnfinished are core tasks that are not terminal at all.
	CoreUnfinished []string
	// MissingContracts lists required tasks that are absent from the graph.
	MissingContracts []string
	// RequiredNotCompleted lists required tasks that did not actually complete.
	RequiredNotCompleted []string
	// GoalResults is the per-goal verdict computed from trusted artifacts.
	GoalResults []models.AcceptedGoalDetail
	// MissingArtifacts are required evidence keys that are absent or untrusted.
	MissingArtifacts []string
	// HasContract reports whether the plan declared an acceptance contract.
	HasContract bool
	// ContractReason explains why the declared contract cannot be accepted.
	ContractReason string
}

// CoreFailed reports whether any core task broke the plan's core goal.
func (e AcceptanceEvidence) CoreFailed() bool { return len(e.CoreFailures) > 0 }

// CoreStopped reports whether core work ended without achieving its goal: a
// block, a cancellation, an unfinished task or a required task that is missing.
func (e AcceptanceEvidence) CoreStopped() bool {
	return len(e.CoreBlocked) > 0 || len(e.CoreCanceled) > 0 || len(e.CoreUnfinished) > 0 ||
		len(e.MissingContracts) > 0 || len(e.RequiredNotCompleted) > 0
}

// GoalsVerified reports whether every declared goal was proven by evidence.
func (e AcceptanceEvidence) GoalsVerified() bool {
	if !e.HasContract || len(e.GoalResults) == 0 {
		return false
	}
	for _, goal := range e.GoalResults {
		if !goal.Verified {
			return false
		}
	}
	return true
}

// EvaluateAcceptanceEvidence inspects the committed graph once and returns every
// fact the terminal decision needs: which core tasks stopped, whether the
// declared contract holds, and which evidence actually proves it.
func EvaluateAcceptanceEvidence(plan *models.PlanGraph) AcceptanceEvidence {
	evidence := AcceptanceEvidence{}
	if plan == nil {
		return evidence
	}
	core := coreTaskIDs(plan)
	for _, node := range plan.Nodes {
		if node == nil || !node.IsEnabled() || !core[node.ID] {
			continue
		}
		switch node.Status {
		case models.StatusFailed:
			evidence.CoreFailures = append(evidence.CoreFailures, node.ID)
		case models.StatusBlocked:
			evidence.CoreBlocked = append(evidence.CoreBlocked, node.ID)
		case models.StatusCanceled:
			evidence.CoreCanceled = append(evidence.CoreCanceled, node.ID)
		case models.StatusCompleted, models.StatusSkipped:
			// Terminal success; the artifact rules below still decide whether the
			// declared goals are actually proven.
		default:
			evidence.CoreUnfinished = append(evidence.CoreUnfinished, node.ID)
		}
	}
	if plan.Acceptance == nil || plan.Acceptance.IsZero() {
		return evidence
	}
	evidence.HasContract = true
	if len(plan.Acceptance.Goals) == 0 {
		evidence.ContractReason = "acceptance contract declares no core goal"
	}
	for _, id := range plan.Acceptance.RequiredTaskIDs {
		node := findPlanNode(plan, id)
		if node == nil {
			evidence.MissingContracts = append(evidence.MissingContracts, id)
			continue
		}
		if node.IsOptional() && evidence.ContractReason == "" {
			evidence.ContractReason = "acceptance requires task " + id + " which is marked optional"
		}
		// A contract that names a task as required and a plan that permanently
		// disables it contradict each other. The run may still finish, but the
		// core goal cannot be called proven without saying so (V3-04).
		if !node.IsEnabled() && evidence.ContractReason == "" {
			evidence.ContractReason = "acceptance requires task " + id + " which the plan disables"
			continue
		}
		// The contract names this task as core work that "must complete for the
		// core goal to hold". A deliberately skipped node satisfies ordinary DAG
		// dependencies, but it never proves the requirement itself.
		if node.IsEnabled() && node.Status != models.StatusCompleted {
			evidence.RequiredNotCompleted = append(evidence.RequiredNotCompleted, id)
		}
	}
	for _, key := range plan.Acceptance.RequiredArtifacts {
		if _, trusted := TrustedArtifact(plan, key); !trusted {
			evidence.MissingArtifacts = appendUnique(evidence.MissingArtifacts, key)
		}
	}
	for _, goal := range plan.Acceptance.Goals {
		detail := evaluateAcceptanceGoal(plan, goal)
		evidence.GoalResults = append(evidence.GoalResults, detail)
		if !detail.Verified {
			for _, key := range detail.EvidenceRefs {
				if _, trusted := TrustedArtifact(plan, key); !trusted {
					evidence.MissingArtifacts = appendUnique(evidence.MissingArtifacts, key)
				}
			}
		}
	}
	return evidence
}

// evaluateAcceptanceGoal turns one declared goal into a verified/unverified
// verdict. Only an artifact whose producer completed in the current execution
// round is accepted; a diagnostic left by a failed task never verifies a goal.
func evaluateAcceptanceGoal(plan *models.PlanGraph, goal models.AcceptanceGoal) models.AcceptedGoalDetail {
	detail := models.AcceptedGoalDetail{
		ID:           goal.ID,
		Statement:    goal.Statement,
		EvidenceRefs: append([]string(nil), goal.EvidenceRefs...),
	}
	if strings.TrimSpace(goal.Statement) == "" {
		detail.Reason = "goal has no statement"
		return detail
	}
	if len(goal.EvidenceRefs) == 0 && goal.MinVerifiedClaims <= 0 {
		detail.Reason = "goal has no verifiable evidence"
		return detail
	}
	detail.Verified = true
	missing := []string{}
	for _, key := range goal.EvidenceRefs {
		if _, trusted := TrustedArtifact(plan, key); !trusted {
			detail.Verified = false
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		detail.Reason = "untrusted or missing evidence: " + strings.Join(missing, ", ")
		return detail
	}
	if goal.MinVerifiedClaims > 0 {
		// The threshold path must apply the same trust rule as EvidenceRefs: a
		// claim graph left by a failed or superseded task is not evidence, no
		// matter what its summary says.
		graph, reason := trustedClaimEvidenceGraph(plan)
		if graph == nil {
			detail.Verified = false
			detail.Reason = reason
			return detail
		}
		if graph.Summary.Verified < goal.MinVerifiedClaims {
			detail.Verified = false
			detail.Reason = "claim evidence does not meet the goal threshold"
		}
	}
	return detail
}

// TrustedClaimEvidenceGraph is the exported form of trustedClaimEvidenceGraph:
// the terminal verdict and every summary derived from the graph must use it.
func TrustedClaimEvidenceGraph(plan *models.PlanGraph) (*models.ClaimEvidenceGraph, string) {
	return trustedClaimEvidenceGraph(plan)
}

// trustedClaimEvidenceGraph decodes the persisted claim evidence graph and
// applies the same producer/revision/execution trust rule as any other artifact.
func trustedClaimEvidenceGraph(plan *models.PlanGraph) (*models.ClaimEvidenceGraph, string) {
	if _, trusted := TrustedArtifact(plan, "claim_evidence_graph"); !trusted {
		return nil, "claim evidence graph is missing or untrusted"
	}
	graph := claimEvidenceGraph(plan)
	if graph == nil {
		return nil, "claim evidence graph cannot be decoded"
	}
	return graph, ""
}

// TrustedArtifact reports whether an artifact is usable as acceptance evidence.
// Three things must hold: the artifact exists, its producer ended successfully
// in the current execution round, and it belongs to the current revision. A
// failed producer's diagnostic output is explicitly not evidence.
func TrustedArtifact(plan *models.PlanGraph, key string) (models.Artifact, bool) {
	if plan == nil || strings.TrimSpace(key) == "" {
		return models.Artifact{}, false
	}
	artifact, ok := plan.Artifacts[key]
	if !ok {
		return models.Artifact{}, false
	}
	producerID := strings.TrimSpace(artifact.ProducerTaskID)
	if producerID == "" {
		return models.Artifact{}, false
	}
	producer := findPlanNode(plan, producerID)
	if producer == nil {
		return models.Artifact{}, false
	}
	if producer.Status != models.StatusCompleted && producer.Status != models.StatusSkipped {
		return models.Artifact{}, false
	}
	// Evidence produced by an earlier round of a retried task does not describe
	// the current run.
	if artifact.Revision != 0 && plan.CurrentRevision != 0 && artifact.Revision != plan.CurrentRevision {
		return models.Artifact{}, false
	}
	// Output written by a run that then failed is a diagnostic, not evidence. A
	// retry that does not re-produce the value keeps this label, so the failed
	// attempt's output can never be re-certified just because the node now
	// reports completed (V3-01).
	if artifactEvidenceClass(artifact) == "diagnostic" {
		return models.Artifact{}, false
	}
	// The strongest binding: an artifact stamped with the execution that produced
	// it must match the producer's latest successful execution. A retry that does
	// not re-produce the artifact leaves the stamp on the earlier attempt, so the
	// old diagnostic cannot be re-certified just because the node now completed.
	if stamp := artifactExecutionID(artifact); stamp != "" {
		if current := completedExecutionID(producer); current != "" && current != stamp {
			return models.Artifact{}, false
		}
	}
	return artifact, true
}

// artifactExecutionID reads the execution stamp of an artifact, if any.
func artifactExecutionID(artifact models.Artifact) string {
	if artifact.Metadata == nil {
		return ""
	}
	if value, ok := artifact.Metadata["execution_id"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// artifactEvidenceClass reads the server-stamped evidence label of an artifact:
// "verified" for output of a successful execution, "diagnostic" for output kept
// from a failed one. An empty label means legacy/unclassified.
func artifactEvidenceClass(artifact models.Artifact) string {
	if artifact.Metadata == nil {
		return ""
	}
	if value, ok := artifact.Metadata["evidence"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// completedExecutionID reads the server-stamped completion boundary of a node.
// markTaskCompleted records it inside the same transition that accepts a result,
// so it identifies the attempt that actually finished the node.
func completedExecutionID(node *models.TaskNode) string {
	if node == nil || node.Metadata == nil {
		return ""
	}
	if value, ok := node.Metadata["completed_execution_id"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}
