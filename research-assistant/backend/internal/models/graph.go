package models

import "time"

// BindingSource describes where an input value comes from.
type BindingSource struct {
	Kind  string `json:"kind"` // context_attachment / context_document / context_paper / artifact
	Role  string `json:"role,omitempty"`
	Name  string `json:"name,omitempty"`
	Index int    `json:"index,omitempty"`
}

// InputBinding maps a typed source to one task input key.
type InputBinding struct {
	Target   string        `json:"target"`
	Source   BindingSource `json:"source"`
	Required bool          `json:"required,omitempty"`
}

// TaskNode is the graph-oriented task representation returned to the frontend and scheduler.
type TaskNode struct {
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Type              string         `json:"type"`
	Description       string         `json:"description"`
	AssignedTo        string         `json:"assigned_to"`
	Status            TaskStatus     `json:"status"`
	Dependencies      []string       `json:"dependencies"`
	RequiredArtifacts []string       `json:"required_artifacts"`
	OutputArtifacts   []string       `json:"output_artifacts"`
	InputBindings     []InputBinding `json:"input_bindings,omitempty"`
	Outcome           PlanOutcome    `json:"outcome,omitempty"`
	Parallelizable    bool           `json:"parallelizable"`
	Priority          int            `json:"priority"`
	RetryLimit        int            `json:"retry_limit"`
	RunCount          int            `json:"run_count"`
	ExecutionID       string         `json:"execution_id,omitempty"`
	ExecutionEpoch    int            `json:"execution_epoch"`
	LeaseOwner        string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt    *time.Time     `json:"lease_expires_at,omitempty"`
	TimeoutSeconds    int            `json:"timeout_seconds,omitempty"`
	Contract          TaskContract   `json:"contract"`
	Inputs            map[string]any `json:"inputs,omitempty"`
	Result            string         `json:"result,omitempty"`
	Code              string         `json:"code,omitempty"`
	StructuredData    string         `json:"structured_data,omitempty"`
	ImageBase64       string         `json:"image_base64,omitempty"`
	Error             string         `json:"error,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	StartedAt         *time.Time     `json:"started_at,omitempty"`
	FinishedAt        *time.Time     `json:"finished_at,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
}

// TaskEdge describes a control-flow or data-flow relation between two task nodes.
type TaskEdge struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// GraphMeta is a lightweight execution summary for the current plan graph.
type GraphMeta struct {
	TotalNodes      int `json:"total_nodes"`
	CompletedNodes  int `json:"completed_nodes"`
	FailedNodes     int `json:"failed_nodes"`
	BlockedNodes    int `json:"blocked_nodes"`
	InProgressNodes int `json:"in_progress_nodes"`
	ReadyNodes      int `json:"ready_nodes"`
}

// PlanGraph is the executable DAG representation used by the refactored plan flow.
type PlanGraph struct {
	ID                  string               `json:"id"`
	UserIntent          string               `json:"user_intent"`
	IntentType          string               `json:"intent_type"`
	Status              TaskStatus           `json:"status"`
	Outcome             PlanOutcome          `json:"outcome,omitempty"`
	OutcomeReason       string               `json:"outcome_reason,omitempty"`
	MissingRequirements []MissingRequirement `json:"missing_requirements,omitempty"`
	OwnerID             string               `json:"owner_id,omitempty"`
	SessionID           string               `json:"session_id,omitempty"`
	TraceID             string               `json:"trace_id"`
	Approval            ApprovalState        `json:"approval"`
	Budget              RunBudget            `json:"budget"`
	Usage               RunUsage             `json:"usage"`
	Context             *ResearchContext     `json:"context,omitempty"`
	Nodes               []*TaskNode          `json:"nodes"`
	Edges               []*TaskEdge          `json:"edges"`
	Artifacts           map[string]Artifact  `json:"artifacts"`
	Meta                GraphMeta            `json:"meta"`
	CreatedAt           time.Time            `json:"created_at"`
	UpdatedAt           time.Time            `json:"updated_at"`
}

// TaskExecutionResult is the normalized scheduler-facing result of one task execution.
type TaskExecutionResult struct {
	Status         TaskStatus `json:"status"`
	Result         string     `json:"result,omitempty"`
	Code           string     `json:"code,omitempty"`
	StructuredData string     `json:"structured_data,omitempty"`
	ImageBase64    string     `json:"image_base64,omitempty"`
	Error          string     `json:"error,omitempty"`
	Logs           []string   `json:"logs,omitempty"`
	Artifacts      []Artifact `json:"artifacts,omitempty"`
}
