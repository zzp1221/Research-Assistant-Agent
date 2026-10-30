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
	RuntimeActionIDs  []string       `json:"runtime_action_ids,omitempty"`
	Outcome           PlanOutcome    `json:"outcome,omitempty"`
	Parallelizable    bool           `json:"parallelizable"`
	Priority          int            `json:"priority"`
	RetryLimit        int            `json:"retry_limit"`
	RunCount          int            `json:"run_count"`
	ExecutionID       string         `json:"execution_id,omitempty"`
	ExecutionEpoch    int            `json:"execution_epoch"`
	// Revision is the plan revision this task belongs to. Legacy tasks are 0.
	Revision int `json:"revision,omitempty"`
	// Enabled controls whether the node participates in execution. Nil means
	// enabled so legacy plans keep running unchanged.
	Enabled        *bool          `json:"enabled,omitempty"`
	LeaseOwner     string         `json:"lease_owner,omitempty"`
	LeaseExpiresAt *time.Time     `json:"lease_expires_at,omitempty"`
	TimeoutSeconds int            `json:"timeout_seconds,omitempty"`
	Contract       TaskContract   `json:"contract"`
	Inputs         map[string]any `json:"inputs,omitempty"`
	Result         string         `json:"result,omitempty"`
	Code           string         `json:"code,omitempty"`
	StructuredData string         `json:"structured_data,omitempty"`
	ImageBase64    string         `json:"image_base64,omitempty"`
	ImageURL       string         `json:"image_url,omitempty"`
	Error          string         `json:"error,omitempty"`
	Metadata       map[string]any `json:"metadata,omitempty"`
	Usage          *TaskUsage     `json:"usage,omitempty"`
	// Verification is the advisory verdict of a verify_result node. It never
	// changes the execution status (design D3).
	Verification *Verification `json:"verification,omitempty"`
	StartedAt    *time.Time    `json:"started_at,omitempty"`
	FinishedAt   *time.Time    `json:"finished_at,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
	UpdatedAt    time.Time     `json:"updated_at"`
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
	TotalNodes            int    `json:"total_nodes"`
	CompletedNodes        int    `json:"completed_nodes"`
	FailedNodes           int    `json:"failed_nodes"`
	BlockedNodes          int    `json:"blocked_nodes"`
	InProgressNodes       int    `json:"in_progress_nodes"`
	ReadyNodes            int    `json:"ready_nodes"`
	PlannerSource         string `json:"planner_source,omitempty"`          // agent / template
	PlannerFallbackReason string `json:"planner_fallback_reason,omitempty"` // why the template was used
	// Routing/observability fields added by the complexity router. They are
	// deliberately separate from PlannerSource so old clients keep working.
	PlanningRoute    string `json:"planning_route,omitempty"`     // fast_path | standard_template | agent_augmented | template_fallback
	Complexity       string `json:"complexity,omitempty"`         // L0 | L1 | L2 | L3 | L4
	ComplexitySource string `json:"complexity_source,omitempty"`  // rule | llm | merged | fallback
	TemplateVariant  string `json:"template_variant,omitempty"`   // simple | standard | full | light
	GeneralPlanShape string `json:"general_plan_shape,omitempty"` // direct | research | paper | literature | legacy
}

// PlanGraph is the executable DAG representation used by the refactored plan flow.
type PlanGraph struct {
	ID string `json:"id"`
	// Title is the user-editable display name. Empty means the summary derives
	// a title from UserIntent, preserving legacy behaviour.
	Title string `json:"title,omitempty"`
	// ProjectID groups the plan under a research project; empty means Inbox.
	ProjectID           string                 `json:"project_id,omitempty"`
	Archived            bool                   `json:"archived,omitempty"`
	ArchivedAt          *time.Time             `json:"archived_at,omitempty"`
	UserIntent          string                 `json:"user_intent"`
	IntentType          string                 `json:"intent_type"`
	Status              TaskStatus             `json:"status"`
	Outcome             PlanOutcome            `json:"outcome,omitempty"`
	OutcomeReason       string                 `json:"outcome_reason,omitempty"`
	MissingRequirements []MissingRequirement   `json:"missing_requirements,omitempty"`
	OwnerID             string                 `json:"owner_id,omitempty"`
	SessionID           string                 `json:"session_id,omitempty"`
	TraceID             string                 `json:"trace_id"`
	Approval            ApprovalState          `json:"approval"`
	Budget              RunBudget              `json:"budget"`
	Usage               RunUsage               `json:"usage"`
	Context             *ResearchContext       `json:"context,omitempty"`
	RuntimePolicy       RuntimePolicy          `json:"runtime_policy,omitempty"`
	RuntimeActions      []RuntimeActionRequest `json:"runtime_actions,omitempty"`
	EnvironmentManifest *EnvironmentManifest   `json:"environment_manifest,omitempty"`
	Nodes               []*TaskNode            `json:"nodes"`
	Edges               []*TaskEdge            `json:"edges"`
	Artifacts           map[string]Artifact    `json:"artifacts"`
	Meta                GraphMeta              `json:"meta"`
	// Paused stops the scheduler from dispatching new tasks. Running tasks are
	// allowed to finish so pause never corrupts an in-flight execution.
	Paused     bool       `json:"paused,omitempty"`
	PausedAt   *time.Time `json:"paused_at,omitempty"`
	RunTrigger string     `json:"run_trigger,omitempty"`
	// Verification is the final advisory verdict of the plan. Legacy plans have
	// no field and are treated as skipped by the UI.
	Verification *Verification `json:"verification,omitempty"`
	// ModelSnapshot records the profile ids/revisions resolved at creation so a
	// run stays reproducible after a runtime profile edit.
	ModelSnapshot *ModelSnapshot `json:"model_snapshot,omitempty"`
	// CurrentRevision is the revision bound to the latest execution. Draft
	// revisions live in the revision store; 0 means legacy/never executed.
	CurrentRevision int `json:"current_revision,omitempty"`
	// Review is the execution-before-review gate state.
	Review ReviewState `json:"review,omitempty"`
	// Questions are structured agent questions for this plan.
	Questions []PlanQuestion `json:"questions,omitempty"`
	// ToolCalls is the durable audit trail of every tool invocation in this
	// plan. It travels with export/replay so a run can be reconstructed.
	ToolCalls []ToolCallRecord `json:"tool_calls,omitempty"`
	// AgentSnapshot freezes profile/prompt/model versions at plan creation.
	AgentSnapshot []AgentProfileSnapshot `json:"agent_snapshot,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
}

// TaskExecutionResult is the normalized scheduler-facing result of one task execution.
type TaskExecutionResult struct {
	Status              TaskStatus            `json:"status"`
	Result              string                `json:"result,omitempty"`
	Code                string                `json:"code,omitempty"`
	StructuredData      string                `json:"structured_data,omitempty"`
	ImageBase64         string                `json:"image_base64,omitempty"`
	ImageBlobRef        string                `json:"image_blob_ref,omitempty"`
	Error               string                `json:"error,omitempty"`
	Logs                []string              `json:"logs,omitempty"`
	Artifacts           []Artifact            `json:"artifacts,omitempty"`
	MissingRequirements []MissingRequirement  `json:"missing_requirements,omitempty"`
	RuntimeAction       *RuntimeActionRequest `json:"runtime_action,omitempty"`
	Usage               *TaskUsage            `json:"usage,omitempty"`
	// Metadata carries non-secret task diagnostics (for example the prompt
	// budget report) from the agent back to the scheduler.
	Metadata map[string]any `json:"metadata,omitempty"`
	// Verification is the advisory verdict produced by a verify_result task.
	Verification *Verification `json:"verification,omitempty"`
	// Question is set when a task pauses on a structured question. The
	// scheduler persists it and moves the task to awaiting_input instead of
	// treating the pause as a failure.
	Question *PlanQuestion `json:"question,omitempty"`
	// ToolCall is the audit record of a tool_call node. The scheduler merges it
	// into PlanGraph.ToolCalls inside the same state transition that completes
	// or fails the task, avoiding a second concurrent plan write.
	ToolCall *ToolCallRecord `json:"tool_call,omitempty"`
}

// IsEnabled reports whether the node should be executed. A nil flag means
// enabled, preserving the behaviour of plans created before the field existed.
func (n *TaskNode) IsEnabled() bool {
	return n == nil || n.Enabled == nil || *n.Enabled
}

// SetEnabled stores an explicit enabled flag.
func (n *TaskNode) SetEnabled(enabled bool) {
	if n == nil {
		return
	}
	value := enabled
	n.Enabled = &value
}
