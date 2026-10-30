package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/prompts"
	"research-assistant-backend/internal/tools"
	"research-assistant-backend/internal/workspace"
	"strings"
	"time"
)

// TaskExecutor executes one task node for the scheduler.
type TaskExecutor interface {
	ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error)
}

type AgentRunner interface {
	ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error
}

// networkAccessApproved reports whether the plan explicitly approved network
// access for this task. Code execution stays offline unless the approval exists.
func networkAccessApproved(plan *models.PlanGraph, taskID string) bool {
	if plan == nil {
		return false
	}
	for _, action := range plan.RuntimeActions {
		if action.TaskID != taskID {
			continue
		}
		if action.Kind == models.RuntimeActionNetworkAccess && action.Status == models.RuntimeActionApproved {
			return true
		}
	}
	return false
}

// DefaultTaskExecutor remains as a lightweight fallback when no real agent router is injected.
type DefaultTaskExecutor struct{}

func NewDefaultTaskExecutor() *DefaultTaskExecutor {
	return &DefaultTaskExecutor{}
}

func (e *DefaultTaskExecutor) ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
	_ = ctx
	_ = plan
	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}

	return nil, fmt.Errorf("no task executor configured for task %s", task.Name)
}

// RoutedTaskExecutor dispatches plan nodes to the existing specialized agents.
type RoutedTaskExecutor struct {
	Librarian      AgentRunner
	Data           AgentRunner
	Coder          AgentRunner
	General        AgentRunner
	ResearchCoding AgentRunner
	Benchmark      AgentRunner
	Answer         AgentRunner
	// Profiles resolves custom AgentProfile records for profile:<id> nodes.
	Profiles *agentprofile.Registry
	// ProfileRunner executes profile-bound LLM tasks.
	ProfileRunner AgentRunner
	// ToolInvoker executes tool_call nodes.
	ToolInvoker *tools.Invoker
}

// SetAgentRuntime wires the tool and agent-profile runtime. It is separate from
// the constructor so existing callers (tests, embedded runners) keep working.
func (e *RoutedTaskExecutor) SetAgentRuntime(profiles *agentprofile.Registry, profileRunner AgentRunner, invoker *tools.Invoker) {
	e.Profiles = profiles
	e.ProfileRunner = profileRunner
	e.ToolInvoker = invoker
}

func NewRoutedTaskExecutor(librarian, data, coder AgentRunner, specialized ...AgentRunner) *RoutedTaskExecutor {
	executor := &RoutedTaskExecutor{
		Librarian: librarian,
		Data:      data,
		Coder:     coder,
	}
	if len(specialized) > 0 {
		executor.ResearchCoding = specialized[0]
	}
	if len(specialized) > 1 {
		executor.Benchmark = specialized[1]
	}
	if len(specialized) > 2 {
		executor.General = specialized[2]
	}
	if len(specialized) > 3 {
		executor.Answer = specialized[3]
	}
	return executor
}

func (e *RoutedTaskExecutor) ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
	if task == nil {
		return nil, fmt.Errorf("task is nil")
	}

	inputs, inputErr := (&planner.ContextBinder{}).BuildInputs(plan, task)
	if inputErr != nil {
		return &models.TaskExecutionResult{
			Status: models.StatusFailed,
			Error:  inputErr.Error(),
		}, nil
	}

	// Resolve a custom AgentProfile before building the runtime task so its
	// prompt/model/tool allowlist is the authoritative contract. Unknown
	// profiles are a hard error: silently falling back to general_agent would
	// bypass the profile's permissions (N-13 3.5).
	var resolvedProfile *models.AgentProfile
	if strings.HasPrefix(task.AssignedTo, "profile:") {
		if e.Profiles == nil {
			return &models.TaskExecutionResult{
				Status: models.StatusFailed,
				Error:  "unknown_agent_profile: agent profile registry is not configured",
			}, nil
		}
		profileID := task.AssignedTo
		profile, profileErr := e.Profiles.GetProfileForOwner(plan.OwnerID, profileID)
		if profileErr != nil {
			return &models.TaskExecutionResult{
				Status: models.StatusFailed,
				Error:  "unknown_agent_profile: " + profileID,
			}, nil
		}
		resolvedProfile = &profile
	}
	contract := task.Contract
	if resolvedProfile != nil {
		contract.AllowedTools = intersectAllowedTools(task.Contract.AllowedTools, resolvedProfile.ToolAllowlist)
	}
	runtimeTask := &models.Task{
		ID:                task.ID,
		Name:              task.Name,
		Type:              task.Type,
		Description:       buildTaskDescription(plan, task),
		AssignedTo:        task.AssignedTo,
		Status:            models.StatusPending,
		Dependencies:      append([]string(nil), task.Dependencies...),
		RequiredArtifacts: append([]string(nil), task.RequiredArtifacts...),
		OutputArtifacts:   append([]string(nil), task.OutputArtifacts...),
		Parallelizable:    task.Parallelizable,
		Priority:          task.Priority,
		RetryLimit:        task.RetryLimit,
		RunCount:          task.RunCount,
		Contract:          contract,
		Inputs:            inputs,
		Metadata: map[string]any{
			"plan_id":        plan.ID,
			"tenant_id":      plan.OwnerID,
			"runtime_policy": plan.RuntimePolicy,
			"network_access": networkAccessApproved(plan, task.ID),
		},
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
	}
	if resolvedProfile != nil {
		runtimeTask.Metadata["agent_profile_id"] = resolvedProfile.ID
		runtimeTask.Metadata["prompt_version_id"] = resolvedProfile.PromptVersionID
		runtimeTask.Metadata["model_profile_id"] = resolvedProfile.ModelProfileID
	}

	// Every agent receives host paths. Enforce the allowed roots centrally so a
	// stray artifact/plan input cannot turn into an arbitrary host read or bind
	// mount, regardless of which agent handles the node.
	if err := validateRuntimeTaskPaths(runtimeTask); err != nil {
		return &models.TaskExecutionResult{
			Status: models.StatusFailed,
			Error:  err.Error(),
		}, nil
	}

	// tool_call is a first-class node: it invokes a registered tool through the
	// ToolInvoker, which enforces the profile/node allowlist, schema, grant and
	// approval contract before any side effect happens.
	if task.Type == "tool_call" {
		return e.executeToolCall(ctx, plan, task, runtimeTask), nil
	}

	// ask_user_question is a control node, not an LLM call. It either returns
	// the answer already written back by the answer endpoint, or pauses the
	// task with a structured question for the frontend to render.
	if task.Type == "ask_user_question" {
		if answer, ok := runtimeTask.Inputs["question_answer"]; ok && strings.TrimSpace(fmt.Sprint(answer)) != "" {
			return &models.TaskExecutionResult{
				Status: models.StatusCompleted,
				Result: fmt.Sprint(answer),
				Metadata: map[string]any{
					"question_answered": true,
				},
			}, nil
		}
		question := questionFromTaskInputs(runtimeTask)
		return &models.TaskExecutionResult{
			Status:   models.StatusAwaitingInput,
			Question: question,
		}, nil
	}

	// 特殊节点：repo_discovery 改为后端真实查询 Papers with Code（通过 HuggingFace Papers API），不走 LLM。
	if task.Type == "repo_discovery" {
		if err := executeRepoDiscovery(ctx, runtimeTask); err != nil {
			return &models.TaskExecutionResult{
				Status: models.StatusFailed,
				Result: runtimeTask.Result,
				Error:  chooseNonEmpty(runtimeTask.Error, err.Error()),
			}, nil
		}
		return &models.TaskExecutionResult{
			Status:              runtimeTask.Status,
			Result:              runtimeTask.Result,
			Code:                runtimeTask.Code,
			StructuredData:      runtimeTask.StructuredData,
			ImageBase64:         runtimeTask.ImageBase64,
			Error:               runtimeTask.Error,
			Artifacts:           buildArtifacts(task, runtimeTask),
			MissingRequirements: missingRequirementsFromTask(runtimeTask),
		}, nil
	}
	if task.Type == "repo_prepare" {
		if err := executeRepoPrepare(ctx, runtimeTask); err != nil {
			return &models.TaskExecutionResult{
				Status: models.StatusFailed,
				Result: runtimeTask.Result,
				Error:  chooseNonEmpty(runtimeTask.Error, err.Error()),
			}, nil
		}
		return &models.TaskExecutionResult{
			Status:              runtimeTask.Status,
			Result:              runtimeTask.Result,
			Code:                runtimeTask.Code,
			StructuredData:      runtimeTask.StructuredData,
			ImageBase64:         runtimeTask.ImageBase64,
			Error:               runtimeTask.Error,
			Artifacts:           buildArtifacts(task, runtimeTask),
			MissingRequirements: missingRequirementsFromTask(runtimeTask),
		}, nil
	}

	sharedContext := map[string]interface{}{
		"plan_id":       plan.ID,
		"user_intent":   plan.UserIntent,
		"intent_type":   plan.IntentType,
		"task_type":     task.Type,
		"artifact_keys": append([]string(nil), task.RequiredArtifacts...),
	}
	if promptContextV2Enabled() {
		// Plan-level stable context is injected once here; agents render it as
		// the plan_context section instead of appending the user intent again.
		sharedContext["plan_graph"] = plan
		budget := prompts.DefaultPromptBudget()
		if plan.ModelSnapshot != nil && plan.ModelSnapshot.Text != nil {
			if plan.ModelSnapshot.Text.ContextWindowTokens > 0 {
				budget.ContextWindow = plan.ModelSnapshot.Text.ContextWindowTokens
			}
			if plan.ModelSnapshot.Text.MaxOutputTokens > 0 {
				budget.MaxOutput = plan.ModelSnapshot.Text.MaxOutputTokens
			}
		}
		sharedContext["prompt_budget"] = budget
	}

	runner, err := e.resolveRunner(task.AssignedTo)
	if err != nil {
		return nil, err
	}
	if err := runner.ExecuteTask(ctx, runtimeTask, sharedContext); err != nil {
		status := runtimeTask.Status
		if status != models.StatusCanceled {
			status = models.StatusFailed
		}
		return &models.TaskExecutionResult{
			Status:         status,
			Result:         runtimeTask.Result,
			Code:           runtimeTask.Code,
			StructuredData: runtimeTask.StructuredData,
			Error:          chooseNonEmpty(runtimeTask.Error, err.Error()),
		}, nil
	}

	status := runtimeTask.Status
	switch status {
	case "", models.StatusPending, models.StatusReady, models.StatusInProgress, models.StatusCompleted:
		status = models.StatusCompleted
	}

	return &models.TaskExecutionResult{
		Status:              status,
		Result:              runtimeTask.Result,
		Code:                runtimeTask.Code,
		StructuredData:      runtimeTask.StructuredData,
		ImageBase64:         runtimeTask.ImageBase64,
		Error:               runtimeTask.Error,
		Artifacts:           buildArtifacts(task, runtimeTask),
		MissingRequirements: missingRequirementsFromTask(runtimeTask),
		Verification:        verificationFromTask(runtimeTask),
		Metadata:            promptMetadataFromTask(runtimeTask),
	}, nil
}

// promptMetadataFromTask copies prompt-budget diagnostics from the runtime task
// so the scheduler can persist them on the plan node.
func promptMetadataFromTask(runtimeTask *models.Task) map[string]any {
	if runtimeTask == nil || runtimeTask.Metadata == nil {
		return nil
	}
	out := map[string]any{}
	for _, key := range []string{
		"prompt_budget", "prompt_truncated_sections",
		"agent_profile_id", "prompt_version_id", "prompt_version", "prompt_hash",
		"model_profile_id", "model", "max_steps",
	} {
		if value, ok := runtimeTask.Metadata[key]; ok {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// verificationFromTask reads the advisory verdict a verify_result agent stored
// on the runtime task.
func verificationFromTask(runtimeTask *models.Task) *models.Verification {
	if runtimeTask == nil {
		return nil
	}
	if runtimeTask.Verification != nil {
		return runtimeTask.Verification
	}
	if runtimeTask.Metadata == nil {
		return nil
	}
	if value, ok := runtimeTask.Metadata["verification"].(*models.Verification); ok {
		return value
	}
	return nil
}

// missingRequirementsFromTask reads the actionable missing-requirement list an
// agent stored on the runtime task.
func missingRequirementsFromTask(runtimeTask *models.Task) []models.MissingRequirement {
	if runtimeTask == nil || runtimeTask.Metadata == nil {
		return nil
	}
	raw, ok := runtimeTask.Metadata["missing_requirements"]
	if !ok || raw == nil {
		return nil
	}
	switch value := raw.(type) {
	case []models.MissingRequirement:
		return value
	case string:
		var parsed []models.MissingRequirement
		if json.Unmarshal([]byte(value), &parsed) == nil {
			return parsed
		}
	}
	return nil
}

func (e *RoutedTaskExecutor) resolveRunner(assignedTo string) (AgentRunner, error) {
	if strings.HasPrefix(assignedTo, "profile:") {
		if e.ProfileRunner != nil {
			return e.ProfileRunner, nil
		}
		return nil, fmt.Errorf("no profile runner configured for %s", assignedTo)
	}
	switch assignedTo {
	case "librarian_agent":
		if e.Librarian != nil {
			return e.Librarian, nil
		}
	case "data_agent":
		if e.Data != nil {
			return e.Data, nil
		}
	case "coder_agent", "sandbox_agent":
		if e.Coder != nil {
			return e.Coder, nil
		}
	case "general_agent":
		if e.General != nil {
			return e.General, nil
		}
		return nil, fmt.Errorf("no general agent runner configured for %s", assignedTo)
	case "research_coding_agent":
		if e.ResearchCoding != nil {
			return e.ResearchCoding, nil
		}
	case "benchmark_agent":
		if e.Benchmark != nil {
			return e.Benchmark, nil
		}
	case "answer_agent":
		if e.Answer != nil {
			return e.Answer, nil
		}
	}
	return nil, fmt.Errorf("no agent runner configured for %s", assignedTo)
}

func buildTaskInputs(plan *models.PlanGraph, task *models.TaskNode) map[string]any {
	inputs, _ := (&planner.ContextBinder{}).BuildInputs(plan, task)
	if inputs == nil {
		return map[string]any{}
	}
	return inputs
}

func taskInputString(inputs map[string]any, key string) string {
	if inputs == nil {
		return ""
	}
	value, ok := inputs[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func validateRuntimeTaskPaths(task *models.Task) error {
	if task == nil {
		return nil
	}
	workspacePath := taskInputString(task.Inputs, "workspace_path")
	if workspacePath != "" {
		validated, err := workspace.ValidateWorkspacePath(workspacePath)
		if err != nil {
			return fmt.Errorf("workspace_path rejected: %w", err)
		}
		workspacePath = validated
	}
	for _, key := range []string{"code_file_path", "validated_code_file_path"} {
		raw := taskInputString(task.Inputs, key)
		if raw == "" {
			continue
		}
		if !filepath.IsAbs(raw) && workspacePath != "" {
			raw = filepath.Join(workspacePath, filepath.FromSlash(raw))
		}
		if _, err := workspace.ValidateWorkspaceFilePath(raw); err != nil {
			return fmt.Errorf("%s rejected: %w", key, err)
		}
	}
	return validateUploadedFilePaths(task.Inputs["uploaded_files"])
}

func validateUploadedFilePaths(raw any) error {
	if raw == nil {
		return nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("encode uploaded_files: %w", err)
	}
	var files []struct {
		Name        string `json:"name"`
		StoragePath string `json:"storage_path"`
	}
	if err := json.Unmarshal(encoded, &files); err != nil {
		return fmt.Errorf("decode uploaded_files: %w", err)
	}
	for _, file := range files {
		path := strings.TrimSpace(file.StoragePath)
		if path == "" {
			continue
		}
		if _, err := workspace.ValidateUploadedFilePath(path); err != nil {
			return fmt.Errorf("uploaded file %q storage_path rejected: %w", file.Name, err)
		}
	}
	return nil
}

// promptContextV2Enabled reports whether the single-assembly prompt path is
// active. PROMPT_CONTEXT_V2=false restores the legacy duplicated prompt.
func promptContextV2Enabled() bool {
	raw := strings.TrimSpace(os.Getenv("PROMPT_CONTEXT_V2"))
	if raw == "" {
		return true
	}
	return !strings.EqualFold(raw, "false") && raw != "0"
}

func buildTaskDescription(plan *models.PlanGraph, task *models.TaskNode) string {
	if task == nil {
		return ""
	}
	if promptContextV2Enabled() {
		// Only the node-local description: plan context and artifacts are
		// injected exactly once by prompts.BuildTaskPrompt.
		return task.Description
	}

	var builder strings.Builder
	builder.WriteString(task.Description)

	if plan != nil {
		builder.WriteString("\n\nGlobal user intent:\n")
		builder.WriteString(plan.UserIntent)
		builder.WriteString("\n\nPlan intent type: ")
		builder.WriteString(plan.IntentType)
	}

	if len(task.RequiredArtifacts) > 0 && plan != nil {
		builder.WriteString("\n\nAvailable upstream artifacts:\n")
		for _, key := range task.RequiredArtifacts {
			artifact, ok := plan.Artifacts[key]
			if !ok {
				continue
			}
			builder.WriteString("- ")
			builder.WriteString(key)
			builder.WriteString(": ")
			builder.WriteString(truncateArtifactValue(artifact.Value))
			builder.WriteString("\n")
		}
	}

	if len(task.OutputArtifacts) > 0 {
		builder.WriteString("\nExpected outputs:\n")
		for _, key := range task.OutputArtifacts {
			builder.WriteString("- ")
			builder.WriteString(key)
			builder.WriteString("\n")
		}
	}

	return builder.String()
}

func buildArtifacts(node *models.TaskNode, runtimeTask *models.Task) []models.Artifact {
	if node == nil || runtimeTask == nil {
		return nil
	}

	artifacts := make([]models.Artifact, 0, len(node.OutputArtifacts))
	for _, key := range node.OutputArtifacts {
		value, artifactType := resolveArtifactPayload(key, runtimeTask)
		artifacts = append(artifacts, models.Artifact{
			Key:            key,
			Type:           artifactType,
			Kind:           artifactKindFor(key),
			EvidenceClass:  artifactEvidenceClassFor(key),
			ProducerTaskID: node.ID,
			Value:          value,
			Metadata: map[string]any{
				"task_name":    node.Name,
				"assigned_to":  node.AssignedTo,
				"source_type":  node.Type,
				"has_code":     runtimeTask.Code != "",
				"has_image":    runtimeTask.ImageBase64 != "",
				"result_error": runtimeTask.Error,
			},
			CreatedAt: time.Now(),
		})
	}
	return artifacts
}

func resolveArtifactPayload(key string, runtimeTask *models.Task) (string, string) {
	if explicit, ok := explicitArtifactValue(runtimeTask, key); ok {
		return explicit, inferArtifactType(key, runtimeTask)
	}

	return inferArtifactValue(key, runtimeTask), inferArtifactType(key, runtimeTask)
}

func explicitArtifactValue(runtimeTask *models.Task, key string) (string, bool) {
	if runtimeTask == nil || runtimeTask.Metadata == nil {
		return "", false
	}

	rawMap, ok := runtimeTask.Metadata["artifact_values"].(map[string]string)
	if ok {
		value, exists := rawMap[key]
		return value, exists
	}

	anyMap, ok := runtimeTask.Metadata["artifact_values"].(map[string]any)
	if ok {
		value, exists := anyMap[key]
		if !exists || value == nil {
			return "", false
		}
		return fmt.Sprint(value), true
	}

	return "", false
}

func inferArtifactValue(key string, runtimeTask *models.Task) string {
	lowerKey := strings.ToLower(key)
	switch {
	case strings.Contains(lowerKey, "plot") || strings.Contains(lowerKey, "image"):
		return chooseNonEmpty(runtimeTask.ImageBase64, runtimeTask.Result)
	case strings.Contains(lowerKey, "code"):
		return chooseNonEmpty(runtimeTask.Code, runtimeTask.Result)
	case strings.Contains(lowerKey, "url"):
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code)
	case strings.Contains(lowerKey, "path") || strings.Contains(lowerKey, "env") || strings.Contains(lowerKey, "workspace") || strings.Contains(lowerKey, "runtime"):
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code)
	case strings.Contains(lowerKey, "metrics"):
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code)
	case strings.Contains(lowerKey, "report"):
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code)
	default:
		return chooseNonEmpty(runtimeTask.Result, runtimeTask.Code, runtimeTask.ImageBase64, nodeArtifactFallback(key))
	}
}

func inferArtifactType(key string, runtimeTask *models.Task) string {
	_ = runtimeTask
	lowerKey := strings.ToLower(key)
	switch {
	case lowerKey == "paper_metadata" || lowerKey == "claim_rubric" || lowerKey == "claim_evidence_graph" || lowerKey == "research_spec" || lowerKey == "research_validation_report" || strings.Contains(lowerKey, "benchmark_contract") || strings.Contains(lowerKey, "split_manifest") || strings.Contains(lowerKey, "trial_ledger") || strings.Contains(lowerKey, "best_candidate"):
		return "json"
	case strings.Contains(lowerKey, "plot") || strings.Contains(lowerKey, "image"):
		return "image_base64"
	case strings.Contains(lowerKey, "code"):
		return "code"
	case strings.Contains(lowerKey, "url"):
		return "url"
	case strings.Contains(lowerKey, "path") || strings.Contains(lowerKey, "env") || strings.Contains(lowerKey, "workspace") || strings.Contains(lowerKey, "runtime"):
		return "text"
	case strings.Contains(lowerKey, "metrics"):
		return "metrics"
	case strings.Contains(lowerKey, "report"):
		return "report"
	case strings.Contains(lowerKey, "dependency"):
		return "dependency_spec"
	default:
		return "text"
	}
}

func artifactKindFor(key string) string {
	lower := strings.ToLower(key)
	switch {
	case lower == "paper_metadata":
		return "paper_metadata"
	case lower == "paper_summary":
		return "paper_summary"
	case strings.Contains(lower, "plot") || strings.Contains(lower, "image"):
		return "plot"
	case strings.Contains(lower, "metrics"):
		return "metrics"
	case lower == "general_response" || lower == "final_answer":
		return "answer"
	case strings.Contains(lower, "report"):
		return "report"
	default:
		return ""
	}
}

func artifactEvidenceClassFor(key string) string {
	lower := strings.ToLower(key)
	switch {
	case lower == "paper_metadata" || lower == "paper_summary" || lower == "parsed_paper":
		return "paper_derived"
	case strings.Contains(lower, "run_metrics") || strings.Contains(lower, "comparison_report") || strings.Contains(lower, "result_plot"):
		return "execution_derived"
	case strings.Contains(lower, "report") || lower == "general_response":
		return "llm_inference"
	default:
		return ""
	}
}

func chooseNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func truncateArtifactValue(value string) string {
	const limit = 500
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func nodeArtifactFallback(key string) string {
	_ = key
	return ""
}

// questionFromTaskInputs builds the question a task wants to ask. The plan
// editor can predefine it through node inputs; otherwise a safe default is
// derived from the task name so the user still gets an actionable prompt.
func questionFromTaskInputs(task *models.Task) *models.PlanQuestion {
	question := &models.PlanQuestion{
		Header:        "需要你的决定",
		Prompt:        "请确认继续执行 " + task.Name + " 的方式。",
		AllowFreeText: true,
	}
	if task == nil {
		return question
	}
	inputs := task.Inputs
	if inputs == nil {
		return question
	}
	if value, ok := inputs["question_header"].(string); ok && strings.TrimSpace(value) != "" {
		question.Header = strings.TrimSpace(value)
	}
	if value, ok := inputs["question_prompt"].(string); ok && strings.TrimSpace(value) != "" {
		question.Prompt = strings.TrimSpace(value)
	}
	if value, ok := inputs["question_default"].(string); ok {
		question.DefaultOption = strings.TrimSpace(value)
	}
	if value, ok := inputs["question_allow_free_text"].(bool); ok {
		question.AllowFreeText = value
	}
	if value, ok := inputs["question_multi_select"].(bool); ok {
		question.MultiSelect = value
	}
	if raw, ok := inputs["question_options"]; ok {
		question.Options = parseQuestionOptions(raw)
	}
	return question
}

func parseQuestionOptions(raw any) []models.QuestionOption {
	options := []models.QuestionOption{}
	switch typed := raw.(type) {
	case []models.QuestionOption:
		return typed
	case []any:
		for index, item := range typed {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			option := models.QuestionOption{}
			if value, ok := entry["id"].(string); ok {
				option.ID = value
			}
			if option.ID == "" {
				option.ID = fmt.Sprintf("option-%d", index+1)
			}
			if value, ok := entry["label"].(string); ok {
				option.Label = value
			}
			if value, ok := entry["description"].(string); ok {
				option.Description = value
			}
			options = append(options, option)
		}
	case []map[string]any:
		for index, entry := range typed {
			option := models.QuestionOption{}
			if value, ok := entry["id"].(string); ok {
				option.ID = value
			}
			if option.ID == "" {
				option.ID = fmt.Sprintf("option-%d", index+1)
			}
			if value, ok := entry["label"].(string); ok {
				option.Label = value
			}
			if value, ok := entry["description"].(string); ok {
				option.Description = value
			}
			options = append(options, option)
		}
	}
	return options
}

// intersectAllowedTools returns the effective tool allowlist. nil means "no
// restriction was declared"; an empty non-nil slice means "nothing allowed",
// which is the correct default for a profile with an empty allowlist.
func intersectAllowedTools(nodeAllowed, profileAllowed []string) []string {
	if profileAllowed == nil {
		return nodeAllowed
	}
	if nodeAllowed == nil {
		return append([]string{}, profileAllowed...)
	}
	out := make([]string, 0, len(nodeAllowed))
	for _, candidate := range nodeAllowed {
		for _, allowed := range profileAllowed {
			if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(allowed)) {
				out = append(out, candidate)
				break
			}
		}
	}
	return out
}

// executeToolCall runs one tool_call node and converts the invoker result into
// the scheduler-facing result. The ToolCallRecord travels back to the scheduler
// so it is merged into the plan inside the same state transition.
func (e *RoutedTaskExecutor) executeToolCall(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode, runtimeTask *models.Task) *models.TaskExecutionResult {
	if e.ToolInvoker == nil {
		return &models.TaskExecutionResult{Status: models.StatusFailed, Error: "tool_invoker_unavailable: tool runtime is not configured"}
	}
	toolID := strings.TrimSpace(taskInputString(runtimeTask.Inputs, "tool_id"))
	if toolID == "" {
		toolID = strings.TrimSpace(taskInputString(task.Inputs, "tool_id"))
	}
	if toolID == "" {
		return &models.TaskExecutionResult{Status: models.StatusFailed, Error: "tool_invalid_arguments: tool_id is required"}
	}
	arguments := toolArguments(task, runtimeTask)
	callID := strings.TrimSpace(taskInputString(runtimeTask.Inputs, "call_id"))
	if callID == "" {
		callID = strings.TrimSpace(taskInputString(task.Inputs, "call_id"))
	}
	profileID := ""
	if runtimeTask.Metadata != nil {
		if value, ok := runtimeTask.Metadata["agent_profile_id"].(string); ok {
			profileID = value
		}
	}
	approvalID := approvedToolActionID(plan, task.ID, toolID, callID)
	invoke := e.ToolInvoker.Invoke(ctx, tools.InvokeRequest{
		PlanID:         plan.ID,
		TaskID:         task.ID,
		ToolID:         toolID,
		ToolVersion:    strings.TrimSpace(taskInputString(runtimeTask.Inputs, "tool_version")),
		Arguments:      arguments,
		AllowedTools:   runtimeTask.Contract.AllowedTools,
		AgentProfileID: profileID,
		OwnerID:        plan.OwnerID,
		ApprovalID:     approvalID,
		CallID:         callID,
	})
	result := &models.TaskExecutionResult{
		Status:   models.StatusCompleted,
		Result:   invoke.Text,
		ToolCall: &invoke.Record,
		Metadata: map[string]any{
			"tool_id":      invoke.Record.ToolID,
			"tool_version": invoke.Record.ToolVersion,
			"tool_status":  invoke.Record.Status,
		},
	}
	if invoke.JSON != nil {
		if encoded, err := json.Marshal(invoke.JSON); err == nil {
			result.StructuredData = string(encoded)
		}
	}
	if invoke.Record.Status != models.ToolCallCompleted {
		result.Status = models.StatusFailed
		result.Error = chooseNonEmpty(invoke.Record.ErrorCode, "tool_execution_failed") + ": " + chooseNonEmpty(invoke.Record.Error, "tool call failed")
		return result
	}
	runtimeTask.Result = invoke.Text
	runtimeTask.Status = models.StatusCompleted
	result.Artifacts = buildArtifacts(task, runtimeTask)
	return result
}

func toolArguments(task *models.TaskNode, runtimeTask *models.Task) map[string]any {
	var raw any
	if task != nil && task.Inputs != nil {
		raw = task.Inputs["arguments"]
	}
	if raw == nil && runtimeTask != nil && runtimeTask.Inputs != nil {
		raw = runtimeTask.Inputs["arguments"]
	}
	switch typed := raw.(type) {
	case map[string]any:
		return typed
	case string:
		var decoded map[string]any
		if json.Unmarshal([]byte(typed), &decoded) == nil {
			return decoded
		}
	}
	return map[string]any{}
}

func approvedToolActionID(plan *models.PlanGraph, taskID, toolID, callID string) string {
	if plan == nil {
		return ""
	}
	for i := range plan.RuntimeActions {
		action := plan.RuntimeActions[i]
		if action.TaskID != taskID || action.Kind != models.RuntimeActionToolCall || action.Status != models.RuntimeActionApproved {
			continue
		}
		if callID != "" {
			if value, _ := action.Details["call_id"].(string); value == callID {
				return action.ID
			}
			continue
		}
		if value, _ := action.Details["tool_id"].(string); value == toolID {
			return action.ID
		}
	}
	return ""
}
