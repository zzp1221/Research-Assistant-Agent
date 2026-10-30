package agentprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/modelprofile"
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
}

func (r *ProfileRunner) ExecuteTask(ctx context.Context, task *models.Task, _ map[string]interface{}) error {
	if task == nil {
		return nil
	}
	if r.Now == nil {
		r.Now = time.Now
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
	if task.Metadata != nil {
		if extra, ok := task.Metadata["prompt_variables"].(map[string]string); ok {
			for key, value := range extra {
				variables[key] = value
			}
		}
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
	messages := []oaichat.Message{{Role: "user", Text: rendered}}
	opts := oaichat.Options{MaxOutputTokens: 1024}
	var response *oaichat.Response
	var resolvedModel string
	if r.Completer == nil {
		task.Status = models.StatusFailed
		task.Error = "no model completer configured"
		return fmt.Errorf("no model completer configured")
	}
	if strings.TrimSpace(profile.ModelProfileID) != "" {
		resolved, resolveErr := r.Completer.ResolveProfile(ownerID, profile.ModelProfileID)
		if resolveErr != nil {
			task.Status = models.StatusFailed
			task.Error = "model profile unavailable: " + resolveErr.Error()
			return resolveErr
		}
		response, err = r.Completer.CompleteWithResolved(ctx, resolved, messages, opts)
		resolvedModel = resolved.Profile.Model
	} else {
		var resolved modelprofile.Resolved
		response, resolved, err = r.Completer.Complete(ctx, ownerID, messages, opts)
		resolvedModel = resolved.Profile.Model
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
	if recorder := usage.FromContext(ctx); recorder != nil {
		recorder.RecordLLM(resolvedModel, response.PromptTokens, response.CompletionTokens, response.EstimatedTokens)
	}
	task.Result = response.Content
	task.Status = models.StatusCompleted
	r.stamp(task, profile, promptVersionID, promptVersionNumber, promptHash, resolvedModel)
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
