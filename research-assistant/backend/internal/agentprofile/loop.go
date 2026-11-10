package agentprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/recovery"
	"research-assistant-backend/internal/tools"
	"research-assistant-backend/internal/usage"
)

func (r *ProfileRunner) executeLoop(ctx context.Context, task *models.Task, profile models.AgentProfile, resolved modelprofile.Resolved, messages []oaichat.Message, opts oaichat.Options, promptHash string) error {
	if err := NormalizeExecution(&profile); err != nil {
		return err
	}
	if resolved.Profile.Capabilities.ToolCalling != modelprofile.VisionYes {
		return loopFail(task, "model_tools_unsupported: native tool calling must be confirmed")
	}
	if r.Invoker == nil || r.Invoker.Registry == nil {
		return loopFail(task, "tool_invoker_unavailable")
	}
	owner, _ := task.Metadata["tenant_id"].(string)
	planID, _ := task.Metadata["plan_id"].(string)
	keyRaw, _ := json.Marshal([]any{owner, task.Metadata["project_id"], task.Metadata["session_id"], planID, task.ID, task.Metadata["plan_revision"]})
	key := string(keyRaw)
	unlock, err := r.Runs.acquire(ctx, key)
	if err != nil {
		return loopFail(task, err.Error())
	}
	defer unlock()
	saved, err := r.Runs.load(key)
	if err != nil {
		return loopFail(task, err.Error())
	}
	bindingRaw, _ := json.Marshal([]any{profile, promptHash, task.Contract, messages})
	binding := contextwindow.Hash(bindingRaw)
	if saved != nil && saved.Binding != binding {
		return loopFail(task, "agent_run_configuration_changed: create a new revision")
	}
	if saved == nil {
		definitions, err := r.loopTools(owner, profile, task.Contract.AllowedTools)
		if err != nil {
			return loopFail(task, err.Error())
		}
		saved = &runCheckpoint{Binding: binding, Profile: profile, Messages: messages, Tools: definitions, Run: models.AgentRunState{Version: "agent.run/v1", ID: contextwindow.Hash(keyRaw), CheckpointRef: contextwindow.Hash(keyRaw), Status: "running", MaxSteps: profile.MaxSteps, MaxToolCalls: profile.MaxToolCalls}}
	}
	if task.ExecutionEpoch < saved.Run.ExecutionEpoch {
		return loopFail(task, "stale_execution")
	}
	saved.Run.ExecutionID = task.ExecutionID
	saved.Run.ExecutionEpoch = task.ExecutionEpoch
	publish := func() {
		run := saved.Run
		task.AgentRun = &run
		task.ToolCalls = append([]models.ToolCallRecord{}, saved.Calls...)
		task.Artifacts = append([]models.Artifact{}, saved.Artifacts...)
	}
	commit := func() error {
		if err := CheckExecution(ctx); err != nil {
			return err
		}
		saved.Run.UpdatedAt = time.Now().UTC()
		publish()
		return r.Runs.save(key, saved)
	}
	stop := func(reason string) error {
		saved.Run.Status = "stopped"
		if reason == context.Canceled.Error() {
			saved.Run.Status = "canceled"
		}
		saved.Run.StopReason = reason
		_ = commit()
		publish()
		return loopFail(task, reason)
	}
	if saved.Run.Status == "completed" {
		publish()
		task.Result = saved.Output
		task.Status = models.StatusCompleted
		return r.validateOutput(task, profile)
	}
	if saved.Run.Status == "stopped" {
		publish()
		return loopFail(task, saved.Run.StopReason)
	}
	if saved.InFlight == "tool" {
		saved.Run.Status = "outcome_unknown"
		saved.Run.StopReason = "tool_outcome_unknown: inspect external state before resuming"
		_ = commit()
		publish()
		task.Status = models.StatusBlocked
		task.Error = saved.Run.StopReason
		return nil
	}
	// An interrupted generation is safe to regenerate within the already charged
	// step limit. No tool dispatch occurs until its complete response is saved.
	saved.InFlight = ""
	saved.Run.Status = "running"
	if err := commit(); err != nil {
		return loopFail(task, err.Error())
	}
	ctx = modelresolver.WithSubBranch(ctx, "profile-tool-loop")
	for {
		if err := CheckExecution(ctx); err != nil {
			return stop(err.Error())
		}
		if saved.Cursor < len(saved.Pending) {
			call := saved.Pending[saved.Cursor]
			definition, ok := saved.Tools[call.Function.Name]
			if !ok {
				return stop("tool_forbidden: model requested an unexposed tool")
			}
			var arguments map[string]any
			if json.Unmarshal([]byte(call.Function.Arguments), &arguments) != nil || arguments == nil {
				return stop("tool_invalid_arguments")
			}
			// A call's local action ID is independent of provider reuse of call IDs.
			actionID := fmt.Sprintf("%s/%d/%d", saved.Run.ID, saved.Run.Steps, saved.Cursor)
			request := tools.InvokeRequest{PlanID: planID, TaskID: task.ID, OwnerID: owner, AgentProfileID: profile.ID, ToolID: definition.ID, ToolVersion: definition.Version, Arguments: arguments, AllowedTools: explicitAllowed(profile.ToolAllowlist, task.Contract.AllowedTools), CallID: actionID}
			for _, prior := range saved.Calls {
				if prior.ToolID == definition.ID && prior.ArgumentsHash == models.HashArguments(arguments) && prior.Status == models.ToolCallCompleted {
					return stop("agent_no_progress: identical tool action already completed; use its saved result")
				}
			}
			current, getErr := r.Invoker.Registry.GetToolForOwner(owner, definition.ID)
			grant, grantErr := r.Invoker.Registry.FindGrant(owner, profile.ID, definition.ID)
			if getErr != nil || grantErr != nil || grant == nil || !current.Enabled || current.Version != definition.Version {
				return stop("tool_forbidden: tool version or authorization changed")
			}
			if current.NeedsApproval() || current.EffectiveApproval(grant) == models.ToolApprovalAlways {
				if reason := tools.ApprovalFailure(ctx, request, time.Now()); reason != "" {
					return stop(reason)
				}
				if tools.ApprovedCallID(ctx, request, time.Now()) == "" {
					// Invoke performs authorization/schema checks even before approval; a
					// forged approval string in model or task metadata is never sufficient.
					denied := r.Invoker.Invoke(ctx, request)
					if denied.ErrorCode != "tool_approval_required" {
						return stop(denied.ErrorCode)
					}
					expires := time.Now().UTC().Add(30 * time.Minute)
					task.Metadata["runtime_action"] = &models.RuntimeActionRequest{ID: "approval-" + contextwindow.Hash([]byte(actionID)), PlanID: planID, TaskID: task.ID, Kind: models.RuntimeActionToolCall, Title: "Approve tool " + definition.Name, Status: models.RuntimeActionPending, RequestedAt: time.Now().UTC(), Details: map[string]any{"tool_id": definition.ID, "tool_version": definition.Version, "call_id": actionID, "arguments_hash": models.HashArguments(arguments), "expires_at": expires.Format(time.RFC3339Nano)}}
					saved.Run.Status = "awaiting_approval"
					saved.Run.PendingCallID = actionID
					if err := commit(); err != nil {
						return stop(err.Error())
					}
					task.Status = models.StatusAwaitingApproval
					return nil
				}
			}
			if saved.Run.ToolCalls >= saved.Run.MaxToolCalls {
				return stop("agent_max_tool_calls")
			}
			saved.Run.ToolCalls++
			saved.InFlight = "tool"
			saved.Run.PendingCallID = actionID
			if err := commit(); err != nil {
				return stop(err.Error())
			}
			result := r.Invoker.Invoke(ctx, request)
			saved.Calls = append(saved.Calls, result.Record)
			if result.Record.Status != models.ToolCallCompleted {
				saved.InFlight = ""
				if err := commit(); err != nil {
					return stop(err.Error())
				}
				task.Failure = recovery.Classify(result.Err, "tool", models.HashArguments(arguments))
				return stop("tool_failed: " + result.ErrorCode)
			}
			text := result.Text
			if result.JSON != nil {
				raw, _ := json.Marshal(result.JSON)
				text += "\n" + string(raw)
			}
			// Persist originals independently; only a bounded, marked view goes back
			// to the model. Neither view may grant permissions or rewrite the task.
			rawText := result.RawText
			if rawText == "" {
				rawText = result.Text
			}
			for index, artifact := range result.Artifacts {
				artifact.Key = fmt.Sprintf("tool_%s_%d", contextwindow.Hash([]byte(actionID))[:16], index)
				if artifact.Metadata == nil {
					artifact.Metadata = map[string]any{}
				}
				artifact.Metadata["tool_call_id"] = result.Record.ID
				artifact.Metadata["result_hash"] = result.Record.ResultHash
				if index == 0 && len(rawText) <= 16<<20 {
					artifact.Value = rawText
					artifact.Metadata["original_available"] = true
				}
				saved.Artifacts = append(saved.Artifacts, artifact)
			}
			if len(text) > 24000 {
				text = text[:24000] + "\n[truncated model view; original is stored as an artifact]"
			}
			if result.Record.Truncated {
				text += "\n[tool transport/output was truncated; evidence is incomplete]"
			}
			saved.Messages = append(saved.Messages, oaichat.Message{Role: "tool", Text: text, ToolCallID: call.ID})
			if err := recovery.Record(ctx, models.ActionLedgerEntry{ID: actionID, Kind: "tool", Action: definition.ID, Arguments: []string{call.Function.Arguments}, BeforeStateHash: models.HashArguments(arguments), AfterStateHash: result.Record.ResultHash, ExecutionID: task.ExecutionID, ArtifactRefs: []string{result.Record.ID}}); err != nil {
				return stop(err.Error())
			}
			saved.Cursor++
			saved.InFlight = ""
			saved.Run.PendingCallID = ""
			if err := commit(); err != nil {
				return stop(err.Error())
			}
			continue
		}
		if saved.Run.Steps >= saved.Run.MaxSteps {
			return stop("agent_max_steps")
		}
		if resolver, ok := r.Completer.(interface {
			ResolveStep(context.Context, string, string, modelprofile.Resolved) (modelprofile.Resolved, error)
		}); ok {
			resolved, err = resolver.ResolveStep(ctx, owner, profile.ModelProfileID, resolved)
			if err != nil {
				return stop("model_profile_unavailable: " + err.Error())
			}
		}
		if resolved.Profile.Capabilities.ToolCalling != modelprofile.VisionYes {
			return stop("model_tools_unsupported")
		}
		budget, err := contextwindow.ResolveBudget(resolved.Profile, 0)
		if err != nil {
			return stop(err.Error())
		}
		opts.MaxOutputTokens = budget.MaxOutput
		ctx = modelresolver.WithResolvedCall(ctx, resolved)
		task.Metadata["model_profile_id"] = resolved.Profile.ID
		task.Metadata["model_profile_revision"] = resolved.Profile.Revision
		task.Metadata["model"] = resolved.Profile.Model
		saved.Pending = nil
		saved.Cursor = 0
		saved.Run.Steps++
		saved.Run.ModelSteps = append(saved.Run.ModelSteps, models.AgentModelStep{Step: saved.Run.Steps, ProfileID: resolved.Profile.ID, ProfileRevision: resolved.Profile.Revision, Model: resolved.Profile.Model, APIFormat: resolved.Profile.APIFormat, MaxOutputTokens: budget.MaxOutput})
		saved.InFlight = "model"
		if err := commit(); err != nil {
			return stop(err.Error())
		}
		opts.Tools = nil
		for _, definition := range saved.Tools {
			raw := json.RawMessage(definition.InputSchema)
			if len(raw) == 0 {
				raw = json.RawMessage(`{"type":"object"}`)
			}
			opts.Tools = append(opts.Tools, oaichat.ToolDefinition{Name: toolAlias(definition.ID), Description: definition.Description, Parameters: raw})
		}
		// Stable ordering matters for prompt hashes and resumable native windows.
		sortTools(opts.Tools)
		response, err := r.Completer.CompleteWithResolved(ctx, resolved, saved.Messages, opts)
		if err != nil {
			saved.InFlight = ""
			task.Failure = recovery.Classify(err, "model", binding)
			if task.Failure.Recoverable {
				saved.Run.Status = "retryable"
				saved.Run.StopReason = err.Error()
				if saveErr := commit(); saveErr != nil {
					return loopFail(task, saveErr.Error())
				}
				return loopFail(task, err.Error())
			}
			return stop(err.Error())
		}
		if response == nil {
			saved.InFlight = ""
			return stop("model returned no response")
		}
		if recorder := usage.FromContext(ctx); recorder != nil && !response.Reused {
			price := usage.Price{PromptPer1K: resolved.Profile.PromptPricePer1K, CompletionPer1K: resolved.Profile.CompletionPricePer1K}
			if response.CostUnknown {
				recorder.RecordLLMUnknownCost(resolved.Profile.Model, response.PromptTokens, response.CompletionTokens, response.EstimatedTokens)
			} else {
				recorder.RecordLLMWithPrice(resolved.Profile.Model, response.PromptTokens, response.CompletionTokens, response.EstimatedTokens, price)
			}
		}
		if err := oaichat.ValidateToolCalls(response.ToolCalls); err != nil {
			return stop(err.Error())
		}
		saved.Messages = append(saved.Messages, oaichat.Message{Role: "assistant", Text: response.Content, ToolCalls: response.ToolCalls})
		saved.Pending = response.ToolCalls
		saved.Cursor = 0
		saved.InFlight = ""
		if err := commit(); err != nil {
			return stop(err.Error())
		}
		if len(response.ToolCalls) > 0 {
			continue
		}
		task.Result = response.Content
		validationErr := r.validateOutput(task, profile)
		if response.FinishReason != "" && response.FinishReason != "stop" {
			validationErr = fmt.Errorf("invalid_output: final response stopped with %s", response.FinishReason)
		}
		if strings.TrimSpace(response.Content) == "" {
			validationErr = fmt.Errorf("empty final output")
		}
		if validationErr == nil {
			validationErr = validateLoopContract(task, saved)
		}
		if validationErr != nil {
			failure := recovery.Classify(validationErr, "output", models.HashResult(response.Content))
			if err := recovery.Reserve(ctx, "local_repair", failure); err != nil {
				return stop(err.Error())
			}
			task.Status = models.StatusInProgress
			task.Error = ""
			saved.Messages = append(saved.Messages, oaichat.Message{Role: "user", Text: "Output validation failed: " + validationErr.Error() + ". Correct the final output within the frozen task contract."})
			if err := commit(); err != nil {
				return stop(err.Error())
			}
			continue
		}
		saved.Output = response.Content
		saved.Run.Status = "completed"
		saved.Run.StopReason = ""
		task.Status = models.StatusCompleted
		task.Error = ""
		if err := commit(); err != nil {
			return stop(err.Error())
		}
		return nil
	}
}

func loopFail(task *models.Task, reason string) error {
	task.Status = models.StatusFailed
	if reason == context.Canceled.Error() {
		task.Status = models.StatusCanceled
	}
	task.Error = reason
	return fmt.Errorf("%s", reason)
}
func toolAlias(id string) string { return "tool_" + contextwindow.Hash([]byte(id))[:24] }
func sortTools(definitions []oaichat.ToolDefinition) {
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Name < definitions[j].Name })
}
func explicitAllowed(profile, node []string) []string {
	out := []string{}
	for _, id := range profile {
		if node == nil || containsID(node, id) {
			out = append(out, id)
		}
	}
	return out
}
func containsID(list []string, id string) bool {
	for _, v := range list {
		if v == id {
			return true
		}
	}
	return false
}
func (r *ProfileRunner) loopTools(owner string, profile models.AgentProfile, node []string) (map[string]models.ToolDefinition, error) {
	out := map[string]models.ToolDefinition{}
	for _, id := range explicitAllowed(profile.ToolAllowlist, node) {
		definition, err := r.Invoker.Registry.GetToolForOwner(owner, id)
		if err != nil {
			return nil, err
		}
		grant, err := r.Invoker.Registry.FindGrant(owner, profile.ID, id)
		if err != nil {
			return nil, err
		}
		if !definition.Enabled || grant == nil {
			continue
		}
		out[toolAlias(id)] = definition
	}
	return out, nil
}
func validateLoopContract(task *models.Task, saved *runCheckpoint) error {
	var output map[string]any
	if task.Contract.OutputSchema != "" {
		if json.Unmarshal([]byte(task.Result), &output) != nil {
			return fmt.Errorf("contract output must be JSON")
		}
		if issues := tools.ValidateArguments(task.Contract.OutputSchema, output); len(issues) > 0 {
			return issues[0]
		}
	}
	required := task.OutputArtifacts
	if len(task.Contract.OutputArtifacts) > 0 {
		required = task.Contract.OutputArtifacts
	}
	if len(required) == 0 {
		return nil
	}
	if output == nil {
		_ = json.Unmarshal([]byte(task.Result), &output)
	}
	for _, key := range required {
		found := false
		for _, artifact := range saved.Artifacts {
			if artifact.Key == key && (artifact.Value != "" || artifact.Location != "") {
				found = true
				break
			}
		}
		if found {
			continue
		}
		value, ok := output[key]
		if !ok || value == nil {
			return fmt.Errorf("required output artifact missing: %s", key)
		}
		raw, _ := json.Marshal(value)
		if len(raw) == 0 || string(raw) == `""` {
			return fmt.Errorf("empty output artifact: %s", key)
		}
		saved.Artifacts = append(saved.Artifacts, models.Artifact{Key: key, Type: "json", Value: string(raw), EvidenceClass: "model_output", ProducerTaskID: task.ID, CreatedAt: time.Now().UTC()})
	}
	return nil
}
