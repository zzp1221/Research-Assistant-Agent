package models

import "time"

// RecoveryState is the durable allowance for one logical task across workers,
// local repairs and revisions. A new execution does not renew this allowance.
type RecoveryState struct {
	Version         string              `json:"version"`
	ChainID         string              `json:"chain_id"`
	MaxActions      int                 `json:"max_actions"`
	UsedActions     int                 `json:"used_actions"`
	HTTPAttempts    int                 `json:"http_attempts"`
	MaxHTTPAttempts int                 `json:"max_http_attempts"`
	Replans         int                 `json:"replans"`
	PendingRetry    bool                `json:"pending_retry,omitempty"`
	Waiting         bool                `json:"waiting,omitempty"`
	Failures        []FailureInfo       `json:"failures,omitempty"`
	Decisions       []RecoveryDecision  `json:"decisions,omitempty"`
	Ledger          []ActionLedgerEntry `json:"ledger,omitempty"`
	Evidence        []Artifact          `json:"evidence,omitempty"`
}

type FailureInfo struct {
	Class        string   `json:"class"`
	Code         string   `json:"code"`
	Stage        string   `json:"stage"`
	Message      string   `json:"message"`
	Fingerprint  string   `json:"fingerprint"`
	StateHash    string   `json:"state_hash"`
	Recoverable  bool     `json:"recoverable"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	RetryAfterMS int64    `json:"retry_after_ms,omitempty"`
}

type RecoveryDecision struct {
	Action             string    `json:"action"`
	Reason             string    `json:"reason"`
	FailureFingerprint string    `json:"failure_fingerprint,omitempty"`
	Consumed           int       `json:"consumed"`
	Remaining          int       `json:"remaining"`
	At                 time.Time `json:"at"`
}

// Verified means the stated action was independently checked; it never means
// its strategy is universally effective or authorizes the same command again.
type ActionLedgerEntry struct {
	ProcedureIDs     []string  `json:"procedure_ids,omitempty"`
	ID               string    `json:"id"`
	Kind             string    `json:"kind"`
	Action           string    `json:"action"`
	Arguments        []string  `json:"arguments,omitempty"`
	WorkingDirectory string    `json:"working_directory,omitempty"`
	BeforeStateHash  string    `json:"before_state_hash,omitempty"`
	AfterStateHash   string    `json:"after_state_hash,omitempty"`
	EnvironmentHash  string    `json:"environment_hash,omitempty"`
	ProtocolHash     string    `json:"protocol_hash,omitempty"`
	BeforeEvidence   []string  `json:"before_evidence,omitempty"`
	AfterEvidence    []string  `json:"after_evidence,omitempty"`
	ArtifactRefs     []string  `json:"artifact_refs,omitempty"`
	Verified         bool      `json:"verified"`
	ExecutionID      string    `json:"execution_id,omitempty"`
	At               time.Time `json:"at"`
}

type ReplanProposal struct {
	SuggestedChanges            []string `json:"suggested_changes"`
	PermissionChanges           []string `json:"permission_changes"`
	AcceptanceContractUnchanged bool     `json:"acceptance_contract_unchanged"`
	Reason                      string   `json:"reason"`
	FailureFingerprint          string   `json:"failure_fingerprint"`
	BaseRevision                int      `json:"base_revision"`
	RequiresReview              bool     `json:"requires_review"`
	FrozenContractHash          string   `json:"frozen_contract_hash"`
	PreserveArtifacts           []string `json:"preserve_artifacts,omitempty"`
	InvalidateArtifacts         []string `json:"invalidate_artifacts,omitempty"`
}
