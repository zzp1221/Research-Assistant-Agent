package models

import "time"

// AcceptanceContract declares, before execution, what makes a plan's core goal
// accepted. It is written into the revision and reviewed with the plan; it is
// never derived from the run afterwards, so a failed node can never be
// re-classified as "optional" once it is cheap to do so.
type AcceptanceContract struct {
	// Version is the contract schema version ("acceptance/v1").
	Version string `json:"version,omitempty"`
	// Goals are the non-empty core goals this plan must achieve. A plan without
	// at least one goal cannot produce a partial outcome.
	Goals []AcceptanceGoal `json:"goals,omitempty"`
	// RequiredArtifacts are artifact keys whose presence is part of acceptance.
	RequiredArtifacts []string `json:"required_artifacts,omitempty"`
	// RequiredTaskIDs are nodes that must complete for the core goal to hold.
	RequiredTaskIDs []string `json:"required_task_ids,omitempty"`
}

// IsZero reports whether no acceptance contract was declared.
func (a AcceptanceContract) IsZero() bool {
	return len(a.Goals) == 0 && len(a.RequiredArtifacts) == 0 && len(a.RequiredTaskIDs) == 0
}

// AcceptanceGoal is one verifiable statement of success. Evidence must be a
// machine-checkable reference (an artifact key or a claim-evidence graph), not a
// model's prose claim that the work is "basically done".
type AcceptanceGoal struct {
	ID        string `json:"id"`
	Statement string `json:"statement"`
	// EvidenceRefs are artifact keys that prove the goal.
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	// MinVerifiedClaims requires at least this many verified claims in the claim
	// evidence graph (0 means the graph is not used as evidence).
	MinVerifiedClaims int `json:"min_verified_claims,omitempty"`
}

// OutcomeDetails records why a plan reached its outcome, including which core
// goals passed and which optional work was lost. It is persisted with the
// terminal status so UI, notifications, history and export share one source.
type OutcomeDetails struct {
	// Mode is the evidence mode the outcome was derived from: acceptance
	// (explicit contract), strict (no optional work allowed) or stop reason.
	Mode string `json:"mode,omitempty"`
	// AcceptedGoals are the core goals proven by the run.
	AcceptedGoals []AcceptedGoalDetail `json:"accepted_goals,omitempty"`
	// OptionalLosses are the enabled optional tasks that did not complete.
	OptionalLosses []OptionalLoss `json:"optional_losses,omitempty"`
	// MissingArtifacts are acceptance-required artifacts that were not produced.
	MissingArtifacts []string `json:"missing_artifacts,omitempty"`
	// Limitations explain which conclusions the losses restrict.
	Limitations []string `json:"limitations,omitempty"`
	// ClaimsVerified/ClaimsTotal summarise the claim evidence graph when present.
	ClaimsVerified int `json:"claims_verified,omitempty"`
	ClaimsTotal    int `json:"claims_total,omitempty"`
}

// AcceptedGoalDetail is one core goal and the evidence that satisfied it.
type AcceptedGoalDetail struct {
	ID           string   `json:"id"`
	Statement    string   `json:"statement"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	Verified     bool     `json:"verified"`
	Reason       string   `json:"reason,omitempty"`
}

// OptionalLoss is one enabled optional task that did not complete.
type OptionalLoss struct {
	TaskID string     `json:"task_id"`
	Name   string     `json:"name,omitempty"`
	Status TaskStatus `json:"status"`
	Reason string     `json:"reason,omitempty"`
}

// PlanFailureEvaluation is the single evaluated terminal decision the scheduler
// commits in one state transition: status, outcome, reason, outcome details and
// the finish timestamp.
type PlanFailureEvaluation struct {
	Status        TaskStatus
	Outcome       PlanOutcome
	OutcomeReason string
	Details       *OutcomeDetails
	FinishedAt    time.Time
	EventType     string
}
