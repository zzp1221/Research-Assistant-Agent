package models

const (
	ClaimRubricVersion        = "claim.rubric/v1"
	ClaimEvidenceGraphVersion = "claim.evidence/v1"
)

// ClaimVerificationStatus is the bounded verdict vocabulary used by the
// claim-to-evidence graph.
type ClaimVerificationStatus string

const (
	ClaimStatusVerified            ClaimVerificationStatus = "verified"
	ClaimStatusPartiallyReproduced ClaimVerificationStatus = "partially_reproduced"
	ClaimStatusContradicted        ClaimVerificationStatus = "contradicted"
	ClaimStatusUnverifiable        ClaimVerificationStatus = "unverifiable"
	ClaimStatusBlockedMissingAsset ClaimVerificationStatus = "blocked_by_missing_asset"
)

// ClaimCriterion is one independently gradable item under a paper claim.
type ClaimCriterion struct {
	ID               string                `json:"id"`
	Description      string                `json:"description"`
	MetricName       string                `json:"metric_name,omitempty"`
	ExpectedValue    *float64              `json:"expected_value,omitempty"`
	Tolerance        *float64              `json:"tolerance,omitempty"`
	Unit             string                `json:"unit,omitempty"`
	RequiredEvidence []string              `json:"required_evidence"`
	NumericContract  *ClaimNumericContract `json:"numeric_contract,omitempty"`
}

// ClaimNumericContract is frozen with the rubric before the measured run.
// Missing fields never acquire permissive defaults from an observed result.
type ClaimNumericContract struct {
	Version             string   `json:"version"`
	Operator            string   `json:"operator"`
	ToleranceMode       string   `json:"tolerance_mode"`
	AbsoluteTolerance   *float64 `json:"absolute_tolerance,omitempty"`
	RelativeTolerance   *float64 `json:"relative_tolerance,omitempty"`
	Inclusive           *bool    `json:"inclusive"`
	ZeroReferencePolicy string   `json:"zero_reference_policy,omitempty"`
	Minimum             *float64 `json:"minimum,omitempty"`
	Maximum             *float64 `json:"maximum,omitempty"`
	SourceTaskID        string   `json:"source_task_id"`
	MetricArtifact      string   `json:"metric_artifact"`
	ExtractionMethod    string   `json:"extraction_method"` // json_metric / benchmark_predictions
	PredictionTaskType  string   `json:"prediction_task_type,omitempty"`
	PredictionsArtifact string   `json:"predictions_artifact,omitempty"`
	DatasetArtifact     string   `json:"dataset_artifact"`
	DatasetSHA256       string   `json:"dataset_sha256"`
	Split               string   `json:"split"`
	ProtocolArtifact    string   `json:"protocol_artifact"`
	ProtocolSHA256      string   `json:"protocol_sha256"`
	CodeSHA256          string   `json:"code_sha256"`
	EnvironmentArtifact string   `json:"environment_artifact"`
	EnvironmentSHA256   string   `json:"environment_sha256"`
	Seeds               []int    `json:"seeds"`
	Aggregation         string   `json:"aggregation"` // single / mean / minimum / maximum
}

// MetricObservation is constructed by a backend parser/recomputer after
// checking the immutable execution and artifact provenance. Model values and
// an input's self-declared extraction_method are never used as observations.
type MetricObservation struct {
	Version           string            `json:"schema_version"`
	CriterionID       string            `json:"criterion_id"`
	MetricName        string            `json:"metric_name"`
	Value             float64           `json:"value"`
	Unit              string            `json:"unit"`
	DatasetSHA256     string            `json:"dataset_sha256"`
	Split             string            `json:"split"`
	ProtocolSHA256    string            `json:"protocol_sha256"`
	Seed              *int              `json:"seed"`
	ExecutionID       string            `json:"execution_id"`
	CodeSHA256        string            `json:"code_sha256"`
	EnvironmentRef    string            `json:"environment_ref"`
	EnvironmentSHA256 string            `json:"environment_sha256"`
	SourceArtifacts   map[string]string `json:"source_artifacts"`
	ExtractionMethod  string            `json:"extraction_method"`
}

// MetricExecutionEvidence is an executed program's structured output. Its
// metadata is checked against a frozen protocol and program-owned run records;
// it is never trusted solely because this JSON has the right shape.
type MetricExecutionEvidence struct {
	Version           string                  `json:"schema_version"`
	ExecutionID       string                  `json:"execution_id"`
	DatasetSHA256     string                  `json:"dataset_sha256"`
	Split             string                  `json:"split"`
	ProtocolSHA256    string                  `json:"protocol_sha256"`
	CodeSHA256        string                  `json:"code_sha256"`
	EnvironmentRef    string                  `json:"environment_ref"`
	EnvironmentSHA256 string                  `json:"environment_sha256"`
	Runs              []MetricExecutionSample `json:"runs"`
}

type MetricExecutionSample struct {
	Seed    *int               `json:"seed"`
	Unit    string             `json:"unit"`
	Metrics map[string]float64 `json:"metrics"`
}

type DeterministicClaimVerdict struct {
	VerifierVersion   string                  `json:"verifier_version"`
	Status            ClaimVerificationStatus `json:"status"`
	Reason            string                  `json:"reason"`
	ExpectedValue     *float64                `json:"expected_value,omitempty"`
	ObservedValue     *float64                `json:"observed_value,omitempty"`
	Unit              string                  `json:"unit,omitempty"`
	Operator          string                  `json:"operator,omitempty"`
	TrustBoundary     string                  `json:"trust_boundary"`
	ReviewRequired    bool                    `json:"review_required"`
	ToleranceMode     string                  `json:"tolerance_mode,omitempty"`
	AbsoluteTolerance *float64                `json:"absolute_tolerance,omitempty"`
	RelativeTolerance *float64                `json:"relative_tolerance,omitempty"`
	Inclusive         *bool                   `json:"inclusive,omitempty"`
	Split             string                  `json:"split,omitempty"`
	ProtocolSHA256    string                  `json:"protocol_sha256,omitempty"`
}

type ClaimModelProposal struct {
	Status        ClaimVerificationStatus `json:"status"`
	Confidence    float64                 `json:"confidence"`
	ObservedValue string                  `json:"observed_value,omitempty"`
	EvidenceKeys  []string                `json:"evidence_keys"`
	Reason        string                  `json:"reason"`
}

// PaperClaim is a top-level claim with one or more independently gradable
// criteria. IDs are assigned deterministically by the backend.
type PaperClaim struct {
	ID            string           `json:"id"`
	Title         string           `json:"title"`
	Statement     string           `json:"statement"`
	SourceLocator string           `json:"source_locator"`
	ClaimType     string           `json:"claim_type"`
	Importance    float64          `json:"importance"`
	Criteria      []ClaimCriterion `json:"criteria"`
}

// ClaimRubric freezes the paper claims before repository execution so the
// acceptance criteria cannot drift after results are observed.
type ClaimRubric struct {
	Version        string       `json:"version"`
	PaperTitle     string       `json:"paper_title"`
	SourceArtifact string       `json:"source_artifact"`
	SHA256         string       `json:"sha256"`
	Claims         []PaperClaim `json:"claims"`
}

// ClaimEvidenceNode is immutable evidence inventory metadata. The graph stores
// hashes and bounded summaries rather than duplicating full upstream artifacts.
type ClaimEvidenceNode struct {
	ID           string `json:"id"`
	ArtifactKey  string `json:"artifact_key"`
	EvidenceType string `json:"evidence_type"`
	SHA256       string `json:"sha256,omitempty"`
	Available    bool   `json:"available"`
	Summary      string `json:"summary,omitempty"`
}

// ClaimCriterionVerdict separates model advice from the final backend verdict,
// retaining observations and frozen-comparison metadata for numeric review.
type ClaimCriterionVerdict struct {
	CriterionID          string                     `json:"criterion_id"`
	Description          string                     `json:"description"`
	Status               ClaimVerificationStatus    `json:"status"`
	Confidence           float64                    `json:"confidence"`
	ObservedValue        string                     `json:"observed_value,omitempty"`
	EvidenceIDs          []string                   `json:"evidence_ids"`
	Reason               string                     `json:"reason"`
	VerificationMethod   string                     `json:"verification_method,omitempty"`
	ModelProposal        *ClaimModelProposal        `json:"model_proposal,omitempty"`
	DeterministicVerdict *DeterministicClaimVerdict `json:"deterministic_verdict,omitempty"`
	Observations         []MetricObservation        `json:"observations,omitempty"`
}

// ClaimEvidenceResult aggregates criterion verdicts for one top-level claim.
type ClaimEvidenceResult struct {
	ClaimID       string                  `json:"claim_id"`
	Title         string                  `json:"title"`
	Statement     string                  `json:"statement"`
	SourceLocator string                  `json:"source_locator"`
	ClaimType     string                  `json:"claim_type"`
	Status        ClaimVerificationStatus `json:"status"`
	Confidence    float64                 `json:"confidence"`
	Criteria      []ClaimCriterionVerdict `json:"criteria"`
}

// ClaimEvidenceSummary provides deterministic graph-level coverage statistics.
type ClaimEvidenceSummary struct {
	TotalClaims               int     `json:"total_claims"`
	TotalCriteria             int     `json:"total_criteria"`
	Verified                  int     `json:"verified"`
	PartiallyReproduced       int     `json:"partially_reproduced"`
	Contradicted              int     `json:"contradicted"`
	Unverifiable              int     `json:"unverifiable"`
	BlockedByMissingAsset     int     `json:"blocked_by_missing_asset"`
	CriterionEvidenceCoverage float64 `json:"criterion_evidence_coverage"`
}

// ClaimEvidenceGraph binds the frozen rubric to immutable upstream evidence and
// validated claim verdicts.
type ClaimEvidenceGraph struct {
	Version      string                `json:"version"`
	Status       string                `json:"status"`
	StatusReason string                `json:"status_reason,omitempty"`
	RubricSHA256 string                `json:"rubric_sha256"`
	Evidence     []ClaimEvidenceNode   `json:"evidence"`
	Claims       []ClaimEvidenceResult `json:"claims"`
	Summary      ClaimEvidenceSummary  `json:"summary"`
}
