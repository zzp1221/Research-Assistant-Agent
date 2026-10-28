package models

import "time"

const TaskContractVersion = "v1"

// TaskContract is the versioned boundary shared by planners, agents and tools.
type TaskContract struct {
	Version         string   `json:"version"`
	InputArtifacts  []string `json:"input_artifacts"`
	OutputArtifacts []string `json:"output_artifacts"`
	InputSchema     string   `json:"input_schema,omitempty"`
	OutputSchema    string   `json:"output_schema,omitempty"`
	AllowedTools    []string `json:"allowed_tools,omitempty"`
}

// ApprovalState records the human decision required before a high-risk plan runs.
type ApprovalState struct {
	Required   bool       `json:"required"`
	Status     string     `json:"status"`
	Reason     string     `json:"reason,omitempty"`
	ApprovedBy string     `json:"approved_by,omitempty"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
}

// RunBudget contains enforceable limits for one plan execution.
type RunBudget struct {
	MaxTaskAttempts int `json:"max_task_attempts"`
	MaxDurationSec  int `json:"max_duration_seconds"`
}

// TaskUsage records the measured resource usage of one task execution.
type TaskUsage struct {
	LLMPromptTokens     int            `json:"llm_prompt_tokens,omitempty"`
	LLMCompletionTokens int            `json:"llm_completion_tokens,omitempty"`
	LLMCalls            int            `json:"llm_calls,omitempty"`
	LLMCostUSD          float64        `json:"llm_cost_usd,omitempty"`
	LLMEstimated        bool           `json:"llm_estimated,omitempty"`
	SandboxSeconds      float64        `json:"sandbox_seconds,omitempty"`
	DurationSeconds     float64        `json:"duration_seconds,omitempty"`
	Attempts            int            `json:"attempts,omitempty"`
	Models              map[string]int `json:"models,omitempty"`
	// Prompt budget diagnostics (design: agent-chain-optimization D).
	PromptBudgetLimit       int `json:"prompt_budget_limit,omitempty"`
	PromptTruncatedSections int `json:"prompt_truncated_sections,omitempty"`
}

// Empty reports whether no usage was observed.
func (u *TaskUsage) Empty() bool {
	if u == nil {
		return true
	}
	return u.LLMCalls == 0 && u.SandboxSeconds == 0 && u.DurationSeconds == 0
}

// Add merges another usage record into this one.
func (u *TaskUsage) Add(other *TaskUsage) {
	if u == nil || other == nil {
		return
	}
	u.LLMPromptTokens += other.LLMPromptTokens
	u.LLMCompletionTokens += other.LLMCompletionTokens
	u.LLMCalls += other.LLMCalls
	u.LLMCostUSD += other.LLMCostUSD
	u.LLMEstimated = u.LLMEstimated || other.LLMEstimated
	u.SandboxSeconds += other.SandboxSeconds
	u.DurationSeconds += other.DurationSeconds
	u.Attempts += other.Attempts
	if len(other.Models) > 0 {
		if u.Models == nil {
			u.Models = map[string]int{}
		}
		for model, calls := range other.Models {
			u.Models[model] += calls
		}
	}
}

// RunUsage is updated transactionally with task state.
type RunUsage struct {
	TaskAttempts int        `json:"task_attempts"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	// ApprovalWaitSeconds is the accumulated time the plan spent waiting for a
	// human decision. It is excluded from the execution duration budget so an
	// approval wait can never "burn" the plan timeout.
	ApprovalWaitSeconds int `json:"approval_wait_seconds,omitempty"`
	// ApprovalWaitingSince marks the start of the current approval wait window.
	ApprovalWaitingSince *time.Time `json:"approval_waiting_since,omitempty"`
	// LLM/sandbox usage is accumulated when tasks complete so plan detail and
	// cost dashboards show real spend instead of guesses.
	LLMPromptTokens     int     `json:"llm_prompt_tokens,omitempty"`
	LLMCompletionTokens int     `json:"llm_completion_tokens,omitempty"`
	LLMCalls            int     `json:"llm_calls,omitempty"`
	LLMCostUSD          float64 `json:"llm_cost_usd,omitempty"`
	LLMEstimated        bool    `json:"llm_estimated,omitempty"`
	SandboxSeconds      float64 `json:"sandbox_seconds,omitempty"`
}

// AddTaskUsage folds task usage into the plan totals.
func (u *RunUsage) AddTaskUsage(task *TaskUsage) {
	if u == nil || task == nil {
		return
	}
	u.LLMPromptTokens += task.LLMPromptTokens
	u.LLMCompletionTokens += task.LLMCompletionTokens
	u.LLMCalls += task.LLMCalls
	u.LLMCostUSD += task.LLMCostUSD
	u.LLMEstimated = u.LLMEstimated || task.LLMEstimated
	u.SandboxSeconds += task.SandboxSeconds
}
