package models

import "time"

// Prompt version lifecycle statuses.
const (
	PromptStatusDraft    = "draft"
	PromptStatusActive   = "active"
	PromptStatusArchived = "archived"
)

// AgentProfile is a configurable agent: a prompt version, a model profile, a
// tool allowlist and bounded execution. Built-in agents use the same shape so
// there is only one execution path.
type AgentProfile struct {
	ID              string    `json:"id"`
	OwnerID         string    `json:"owner_id,omitempty"`
	Name            string    `json:"name"`
	Description     string    `json:"description,omitempty"`
	PromptVersionID string    `json:"prompt_version_id,omitempty"`
	ModelProfileID  string    `json:"model_profile_id,omitempty"`
	ToolAllowlist   []string  `json:"tool_allowlist,omitempty"`
	MaxSteps        int       `json:"max_steps,omitempty"`
	OutputSchema    string    `json:"output_schema,omitempty"`
	Builtin         bool      `json:"builtin,omitempty"`
	Enabled         bool      `json:"enabled"`
	CreatedBy       string    `json:"created_by,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// PromptVersion is an immutable prompt template. Editing a prompt creates a
// new version; activating a version only moves the active pointer.
type PromptVersion struct {
	ID         string    `json:"id"`
	OwnerID    string    `json:"owner_id,omitempty"`
	Name       string    `json:"name"`
	Version    int       `json:"version"`
	Template   string    `json:"template"`
	Variables  []string  `json:"variables,omitempty"`
	Status     string    `json:"status"`
	EvalScore  float64   `json:"eval_score,omitempty"`
	EvalDetail string    `json:"eval_detail,omitempty"`
	CreatedBy  string    `json:"created_by,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// PromptEvalCase is one deterministic prompt evaluation case.
type PromptEvalCase struct {
	Name             string            `json:"name,omitempty"`
	Variables        map[string]string `json:"variables,omitempty"`
	ExpectedContains string            `json:"expected_contains,omitempty"`
}

// PromptEvalResult is the stored outcome of one evaluation run.
type PromptEvalResult struct {
	PromptVersionID string           `json:"prompt_version_id"`
	Score           float64          `json:"score"`
	Passed          int              `json:"passed"`
	Total           int              `json:"total"`
	Cases           []PromptEvalCaseResult `json:"cases"`
	Mode            string           `json:"mode"`
	EvaluatedAt     time.Time        `json:"evaluated_at"`
}

// PromptEvalCaseResult is the per-case verdict.
type PromptEvalCaseResult struct {
	Name    string  `json:"name,omitempty"`
	Passed  bool    `json:"passed"`
	Score   float64 `json:"score"`
	Detail  string  `json:"detail,omitempty"`
}
