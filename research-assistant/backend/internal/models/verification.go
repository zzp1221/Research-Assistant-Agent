package models

import "time"

// VerificationStatus is the advisory verdict of a verify_result node. It is
// deliberately decoupled from the plan/task execution status: a plan can be
// completed while its verification is fail or inconclusive (design D3).
type VerificationStatus string

const (
	VerificationPass         VerificationStatus = "pass"
	VerificationFail         VerificationStatus = "fail"
	VerificationInconclusive VerificationStatus = "inconclusive"
	VerificationSkipped      VerificationStatus = "skipped"
)

// VerificationLevel records which layer produced the verdict.
type VerificationLevel string

const (
	VerificationLevelNone       VerificationLevel = "none"
	VerificationLevelStructural VerificationLevel = "structural"
	VerificationLevelText       VerificationLevel = "text"
	VerificationLevelVisual     VerificationLevel = "visual"
)

// VerifiedBy identifies the model profile that judged the images.
type VerifiedBy struct {
	ProfileID string `json:"profile_id,omitempty"`
	Model     string `json:"model,omitempty"`
	Mode      string `json:"mode,omitempty"` // main / vision_fallback
}

// VerificationTokenUsage is the token cost of one verification run.
type VerificationTokenUsage struct {
	Prompt      int  `json:"prompt"`
	Completion  int  `json:"completion"`
	Estimated   bool `json:"estimated,omitempty"`
}

// ImageVerification is the per-image outcome of the structural gate and the
// optional visual judgement. SourceKey points at the artifact key the image was
// bound from so the scheduler can annotate that artifact.
type ImageVerification struct {
	SourceKey       string   `json:"source_key,omitempty"`
	SHA256          string   `json:"sha256,omitempty"`
	Width           int      `json:"width,omitempty"`
	Height          int      `json:"height,omitempty"`
	Bytes           int      `json:"bytes,omitempty"`
	StructuralStatus string  `json:"structural_status,omitempty"`
	StructuralIssues []string `json:"structural_issues,omitempty"`
	VisualStatus    string   `json:"visual_status,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	Issues          []string `json:"issues,omitempty"`
	Evidence        []string `json:"evidence,omitempty"`
	Confidence      float64  `json:"confidence,omitempty"`
	ProfileID       string   `json:"profile_id,omitempty"`
	Model           string   `json:"model,omitempty"`
	Error           string   `json:"error,omitempty"`
}

// Verification is the structured, auditable result of verify_result. It is
// stored on the plan (final summary), the verify node and image artifacts.
type Verification struct {
	Status        VerificationStatus `json:"status"`
	Level         VerificationLevel  `json:"level"`
	Verified      bool               `json:"verified"`
	VerifiedBy    *VerifiedBy        `json:"verified_by,omitempty"`
	RubricVersion string             `json:"rubric_version,omitempty"`
	ImageSHA256   []string           `json:"image_sha256,omitempty"`
	Issues        []string           `json:"issues,omitempty"`
	Evidence      []string           `json:"evidence,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Reason        string             `json:"reason,omitempty"`
	CheckedAt     time.Time          `json:"checked_at,omitempty"`
	DurationMS    int64              `json:"duration_ms,omitempty"`
	TokenUsage    *VerificationTokenUsage `json:"token_usage,omitempty"`
	Images        []ImageVerification `json:"images,omitempty"`
	Warnings      []string           `json:"warnings,omitempty"`
}

// ProfileSnapshot records the resolved model profile and revision used by a
// plan so a run stays reproducible after a runtime profile change.
type ProfileSnapshot struct {
	ProfileID           string `json:"profile_id"`
	Model               string `json:"model,omitempty"`
	Revision            int    `json:"revision"`
	Mode                string `json:"mode,omitempty"`
	ContextWindowTokens int    `json:"context_window_tokens,omitempty"`
	MaxOutputTokens     int    `json:"max_output_tokens,omitempty"`
}

// ModelSnapshot captures the text and vision profiles a plan was created with.
type ModelSnapshot struct {
	Text   *ProfileSnapshot  `json:"text,omitempty"`
	Vision []ProfileSnapshot `json:"vision,omitempty"`
	CreatedAt time.Time      `json:"created_at,omitempty"`
}
