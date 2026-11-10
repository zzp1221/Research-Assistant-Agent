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
	ExecutionMode   string    `json:"execution_mode,omitempty"`
	MaxToolCalls    int       `json:"max_tool_calls,omitempty"`
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
	CheckRendering   bool              `json:"check_rendering,omitempty"`
	Task             *PromptEvalTask   `json:"task,omitempty"`
	Grader           *TaskEvalGrader   `json:"grader,omitempty"`
}

// PromptEvalTask contains only agent-visible inputs. Reference answers live in
// Grader and are never included in the model context or memory.
type PromptEvalTask struct {
	Description string         `json:"description"`
	Inputs      map[string]any `json:"inputs,omitempty"`
	Contract    TaskContract   `json:"contract"`
}

// TaskEvalGrader is a deterministic frozen contract, independent of the agent.
// JSON keys use RFC 6901 pointers. Artifact hashes are sha256 of exact content.
type TaskEvalGrader struct {
	OutputSchema       string            `json:"output_schema,omitempty"`
	ExpectedJSON       map[string]any    `json:"expected_json,omitempty"`
	RequiredTools      []string          `json:"required_tools,omitempty"`
	RequiredArtifacts  map[string]string `json:"required_artifacts,omitempty"`
	ExpectedStatus     TaskStatus        `json:"expected_status,omitempty"`
	ForbiddenTools     []string          `json:"forbidden_tools,omitempty"`
	MaxModelSteps      int               `json:"max_model_steps,omitempty"`
	MaxToolCalls       int               `json:"max_tool_calls,omitempty"`
	ExpectedStopReason string            `json:"expected_stop_reason,omitempty"`
}

// PromptEvalResult is the stored outcome of one evaluation run.
type PromptEvalResult struct {
	PromptVersionID string                 `json:"prompt_version_id"`
	Score           float64                `json:"score"`
	Passed          int                    `json:"passed"`
	Total           int                    `json:"total"`
	Scored          int                    `json:"scored"`
	Failed          int                    `json:"failed"`
	Errors          int                    `json:"errors"`
	Skipped         int                    `json:"skipped"`
	Attempted       int                    `json:"attempted"`
	ScoreAvailable  bool                   `json:"score_available"`
	ScoredRate      *float64               `json:"scored_rate"`
	PlannedRate     *float64               `json:"planned_rate"`
	GraderVersion   string                 `json:"grader_version"`
	ProfileID       string                 `json:"profile_id,omitempty"`
	Cases           []PromptEvalCaseResult `json:"cases"`
	Mode            string                 `json:"mode"`
	EvaluatedAt     time.Time              `json:"evaluated_at"`
}

// PromptEvalCaseResult is the per-case verdict.
type PromptEvalCaseResult struct {
	Name           string     `json:"name,omitempty"`
	Passed         bool       `json:"passed"`
	Score          float64    `json:"score"`
	Detail         string     `json:"detail,omitempty"`
	Status         string     `json:"status"`
	Mode           string     `json:"mode"`
	GraderVersion  string     `json:"grader_version"`
	Attempted      bool       `json:"attempted"`
	ModelInvoked   bool       `json:"model_invoked"`
	EvidenceRefs   []string   `json:"evidence_refs,omitempty"`
	ModelProfileID string     `json:"model_profile_id,omitempty"`
	ModelRevision  int        `json:"model_revision,omitempty"`
	RunID          string     `json:"run_id,omitempty"`
	Usage          *TaskUsage `json:"usage,omitempty"`
}
