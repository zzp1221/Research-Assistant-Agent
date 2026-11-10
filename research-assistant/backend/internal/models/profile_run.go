package models

import "time"

// AgentRunState is a non-secret view of the private durable checkpoint.
type AgentRunState struct {
	Version        string           `json:"version"`
	ID             string           `json:"id"`
	CheckpointRef  string           `json:"checkpoint_ref"`
	Status         string           `json:"status"`
	StopReason     string           `json:"stop_reason,omitempty"`
	Steps          int              `json:"steps"`
	MaxSteps       int              `json:"max_steps"`
	ToolCalls      int              `json:"tool_calls"`
	MaxToolCalls   int              `json:"max_tool_calls"`
	ExecutionID    string           `json:"execution_id,omitempty"`
	ExecutionEpoch int              `json:"execution_epoch,omitempty"`
	PendingCallID  string           `json:"pending_call_id,omitempty"`
	UpdatedAt      time.Time        `json:"updated_at"`
	ModelSteps     []AgentModelStep `json:"model_steps,omitempty"`
}

type AgentModelStep struct {
	Step            int    `json:"step"`
	ProfileID       string `json:"profile_id"`
	ProfileRevision int    `json:"profile_revision"`
	Model           string `json:"model"`
	APIFormat       string `json:"api_format"`
	MaxOutputTokens int    `json:"max_output_tokens"`
}
