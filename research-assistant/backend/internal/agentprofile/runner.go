package agentprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/tools"
	"research-assistant-backend/internal/usage"
)

// ProfileRunner executes an LLM task through an AgentProfile: render the
// profile's PromptVersion, resolve its ModelProfile, call the model and stamp
// the exact versions onto the task so the plan snapshot is reproducible.
type ProfileRunner struct {
	Profiles  *Registry
	Completer Completer
	Now       func() time.Time
	Invoker   *tools.Invoker
	Runs      *RunStore
}

func (r *ProfileRunner) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	if task == nil {
		return nil
	}
	ownerID := ""
	if task.Metadata != nil {
		if value, ok := task.Metadata["tenant_id"].(string); ok {
			ownerID = value
		}
	}
	profileID := ""
	if task.Metadata != nil {
		if value, ok := task.Metadata["agent_profile_id"].(string); ok {
			profileID = value
		}
	}
	if profileID == "" && strings.HasPrefix(task.AssignedTo, "profile:") {
		profileID = task.AssignedTo
	}
	if profileID == "" {
		task.Status = models.StatusFailed
		task.Error = "profile runner requires agent_profile_id"
		return fmt.Errorf("profile runner requires agent_profile_id")
	}
	profile, err := r.Profiles.GetProfileForOwner(ownerID, profileID)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = "agent profile not found: " + profileID
		return err
	}
	if !profile.Enabled {
		task.Status = models.StatusFailed
		task.Error = "agent profile is disabled: " + profileID
		return fmt.Errorf("agent profile is disabled")
	}
	template := ""
	promptVersionID := ""
	promptVersionNumber := 0
	promptHash := ""
	if strings.TrimSpace(profile.PromptVersionID) != "" {
		prompt, promptErr := r.Profiles.GetPromptForOwner(ownerID, profile.PromptVersionID)
		if promptErr != nil {
			task.Status = models.StatusFailed
			task.Error = "prompt version not found: " + profile.PromptVersionID
			return promptErr
		}
		template = prompt.Template
		promptVersionID = prompt.ID
		promptVersionNumber = prompt.Version
		sum := sha256.Sum256([]byte(prompt.Template))
		promptHash = "sha256:" + hex.EncodeToString(sum[:])
	}
	variables := StringVariables(task.Inputs)
	for key, value := range promptVariableOverrides(task) {
		variables[key] = value
	}
	rendered := task.Description
	if strings.TrimSpace(template) != "" {
		rendered, err = RenderTemplate(template, variables)
		if err != nil {
			task.Status = models.StatusFailed
			task.Error = err.Error()
			return err
		}
	}
	opts := oaichat.Options{}
	var response *oaichat.Response
	var resolvedModel string
	var modelPrice usage.Price
	if r.Completer == nil {
		task.Status = models.StatusFailed
		task.Error = "no model completer configured"
		return fmt.Errorf("no model completer configured")
	}
	var resolved modelprofile.Resolved
	var resolveErr error
	hasResolved := false
	if pinned, ok := modelresolver.ResolvedCall(ctx); ok && (profile.ModelProfileID == "" || pinned.Profile.ID == profile.ModelProfileID) {
		resolved, hasResolved = pinned, true
	} else if strings.TrimSpace(profile.ModelProfileID) != "" {
		resolved, resolveErr = r.Completer.ResolveProfile(ownerID, profile.ModelProfileID)
		hasResolved = true
	} else if resolver, ok := r.Completer.(interface {
		ResolveCall(context.Context) (modelprofile.Resolved, error)
	}); ok {
		resolved, resolveErr = resolver.ResolveCall(modelresolver.WithOwner(ctx, ownerID))
		hasResolved = true
	}
	if resolveErr != nil {
		task.Status = models.StatusFailed
		task.Error = "model profile unavailable: " + resolveErr.Error()
		return resolveErr
	}
	budget := profilePromptBudget(sharedContext)
	if hasResolved {
		resolvedBudget, budgetErr := contextwindow.ResolveBudget(resolved.Profile, 0)
		if budgetErr != nil {
			task.Status, task.Error = models.StatusFailed, budgetErr.Error()
			return budgetErr
		}
		budget = resolvedBudget.PromptBudget
		opts.MaxOutputTokens = resolvedBudget.MaxOutput
		ctx = modelresolver.WithResolvedCall(ctx, resolved)
	}
	messages, report, err := buildProfileMessages(task, sharedContext, template, rendered, budget, hasResolved)
	if err != nil {
		task.Status, task.Error = models.StatusFailed, err.Error()
		return err
	}
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["prompt_budget"] = report
	if truncated := report.TruncatedSections(); len(truncated) > 0 {
		task.Metadata["prompt_truncated_sections"] = truncated
	}
	for _, key := range []string{"memory_refs", "wiki_refs"} {
		if refs, ok := sharedContext[key]; ok {
			task.Metadata[key] = refs
		}
	}
	if profile.ExecutionMode == "tool_loop" {
		if !hasResolved {
			return fmt.Errorf("model_tools_unsupported: explicit resolved model is required")
		}
		r.stamp(task, profile, promptVersionID, promptVersionNumber, promptHash, resolved.Profile.Model)
		task.Metadata["model_profile_id"] = resolved.Profile.ID
		task.Metadata["model_profile_revision"] = resolved.Profile.Revision
		return r.executeLoop(ctx, task, profile, resolved, messages, opts, promptHash)
	}
	if hasResolved {
		response, err = r.Completer.CompleteWithResolved(ctx, resolved, messages, opts)
		resolvedModel = resolved.Profile.Model
		modelPrice = usage.Price{PromptPer1K: resolved.Profile.PromptPricePer1K, CompletionPer1K: resolved.Profile.CompletionPricePer1K}
		task.Metadata["model_profile_revision"] = resolved.Profile.Revision
	} else {
		response, resolved, err = r.Completer.Complete(ctx, ownerID, messages, opts)
		resolvedModel = resolved.Profile.Model
		modelPrice = usage.Price{PromptPer1K: resolved.Profile.PromptPricePer1K, CompletionPer1K: resolved.Profile.CompletionPricePer1K}
	}
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
		return err
	}
	if response == nil {
		task.Status = models.StatusFailed
		task.Error = "model returned no response"
		return fmt.Errorf("model returned no response")
	}
	if recorder := usage.FromContext(ctx); recorder != nil && !response.Reused {
		if response.CostUnknown {
			recorder.RecordLLMUnknownCost(resolvedModel, response.PromptTokens, response.CompletionTokens, response.EstimatedTokens)
		} else {
			recorder.RecordLLMWithPrice(resolvedModel, response.PromptTokens, response.CompletionTokens, response.EstimatedTokens, modelPrice)
		}
	}
	task.Result = response.Content
	task.Status = models.StatusCompleted
	r.stamp(task, profile, promptVersionID, promptVersionNumber, promptHash, resolvedModel)
	if resolved.Profile.ID != "" {
		task.Metadata["model_profile_id"] = resolved.Profile.ID
		task.Metadata["model_profile_revision"] = resolved.Profile.Revision
	}
	return r.validateOutput(task, profile)
}

func (r *ProfileRunner) stamp(task *models.Task, profile models.AgentProfile, promptVersionID string, promptVersionNumber int, promptHash, model string) {
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["agent_profile_id"] = profile.ID
	task.Metadata["prompt_version_id"] = promptVersionID
	task.Metadata["prompt_version"] = promptVersionNumber
	task.Metadata["prompt_hash"] = promptHash
	task.Metadata["model_profile_id"] = profile.ModelProfileID
	task.Metadata["model"] = model
	task.Metadata["max_steps"] = profile.MaxSteps
}

func (r *ProfileRunner) validateOutput(task *models.Task, profile models.AgentProfile) error {
	if strings.TrimSpace(profile.OutputSchema) == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(task.Result), &decoded); err != nil {
		task.Status = models.StatusFailed
		task.Error = "output is not valid JSON: " + err.Error()
		return fmt.Errorf("output is not valid JSON")
	}
	if errors := tools.ValidateArguments(profile.OutputSchema, decoded); len(errors) > 0 {
		task.Status = models.StatusFailed
		task.Error = "output schema validation failed: " + errors[0].Error()
		return fmt.Errorf("output schema validation failed")
	}
	task.StructuredData = task.Result
	return nil
}
