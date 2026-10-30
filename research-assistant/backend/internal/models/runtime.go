package models

import (
	"strings"
	"time"
)

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

// RunBudget contains enforceable limits for one plan execution. Zero means
// "not configured" for every optional limit, so legacy plans keep running.
type RunBudget struct {
	MaxTaskAttempts   int     `json:"max_task_attempts"`
	MaxDurationSec    int     `json:"max_duration_seconds"`
	MaxTokens         int     `json:"max_tokens,omitempty"`
	MaxCostUSD        float64 `json:"max_cost_usd,omitempty"`
	MaxSandboxSeconds float64 `json:"max_sandbox_seconds,omitempty"`
	MaxDownloadBytes  int64   `json:"max_download_bytes,omitempty"`
	// Policy is "soft" (pause and wait for a human) or "hard" (cancel). It only
	// matters when a token/cost/sandbox limit is configured.
	Policy string `json:"policy,omitempty"`
}

// BudgetPolicyOrDefault returns the effective policy for a budget.
func (b RunBudget) BudgetPolicyOrDefault() string {
	if strings.EqualFold(strings.TrimSpace(b.Policy), "hard") {
		return "hard"
	}
	return "soft"
}

// TaskUsage records the measured resource usage of one task execution.
type TaskUsage struct {
	LLMPromptTokens     int     `json:"llm_prompt_tokens,omitempty"`
	LLMCompletionTokens int     `json:"llm_completion_tokens,omitempty"`
	LLMCalls            int     `json:"llm_calls,omitempty"`
	LLMCostUSD          float64 `json:"llm_cost_usd,omitempty"`
	LLMEstimated        bool    `json:"llm_estimated,omitempty"`
	// CostSource explains where the USD figure came from: profile, env,
	// estimated or unknown. It lets the UI say "estimated" instead of implying
	// a measured bill.
	CostSource      string         `json:"cost_source,omitempty"`
	SandboxSeconds  float64        `json:"sandbox_seconds,omitempty"`
	DurationSeconds float64        `json:"duration_seconds,omitempty"`
	Attempts        int            `json:"attempts,omitempty"`
	Models          map[string]int `json:"models,omitempty"`
	// ModelBreakdown keeps per-model tokens and cost so plan detail can show
	// which model cost what, not just call counts.
	ModelBreakdown []ModelUsage `json:"model_breakdown,omitempty"`
	// Prompt budget diagnostics (design: agent-chain-optimization D).
	PromptBudgetLimit       int `json:"prompt_budget_limit,omitempty"`
	PromptTruncatedSections int `json:"prompt_truncated_sections,omitempty"`
}

// ModelUsage is the per-model token and cost breakdown of one task.
type ModelUsage struct {
	Model            string  `json:"model"`
	Calls            int     `json:"calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	Estimated        bool    `json:"estimated,omitempty"`
	CostSource       string  `json:"cost_source,omitempty"`
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
	u.CostSource = weakerCostSource(u.CostSource, other.CostSource)
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
	if len(other.ModelBreakdown) > 0 {
		index := map[string]int{}
		for i, entry := range u.ModelBreakdown {
			index[entry.Model] = i
		}
		for _, entry := range other.ModelBreakdown {
			if i, ok := index[entry.Model]; ok {
				u.ModelBreakdown[i].Calls += entry.Calls
				u.ModelBreakdown[i].PromptTokens += entry.PromptTokens
				u.ModelBreakdown[i].CompletionTokens += entry.CompletionTokens
				u.ModelBreakdown[i].CostUSD += entry.CostUSD
				u.ModelBreakdown[i].Estimated = u.ModelBreakdown[i].Estimated || entry.Estimated
				u.ModelBreakdown[i].CostSource = weakerCostSource(u.ModelBreakdown[i].CostSource, entry.CostSource)
				continue
			}
			index[entry.Model] = len(u.ModelBreakdown)
			u.ModelBreakdown = append(u.ModelBreakdown, entry)
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
	CostSource          string  `json:"cost_source,omitempty"`
	SandboxSeconds      float64 `json:"sandbox_seconds,omitempty"`
	// BudgetStatus mirrors the last budget evaluation: ok/warning/exceeded.
	BudgetStatus   string   `json:"budget_status,omitempty"`
	BudgetWarnings []string `json:"budget_warnings,omitempty"`
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
	u.CostSource = weakerCostSource(u.CostSource, task.CostSource)
	u.SandboxSeconds += task.SandboxSeconds
}

// weakerCostSource returns the less trustworthy of two cost sources. Profile
// pricing is strongest, env pricing is next, then estimated, then unknown.
func weakerCostSource(left, right string) string {
	rank := func(value string) int {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "profile":
			return 3
		case "env":
			return 2
		case "estimated":
			return 1
		case "unknown":
			return 0
		default:
			return -1
		}
	}
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if rank(left) <= rank(right) {
		return left
	}
	return right
}
