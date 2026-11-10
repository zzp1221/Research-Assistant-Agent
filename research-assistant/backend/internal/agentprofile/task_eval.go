package agentprofile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/tools"
	"research-assistant-backend/internal/usage"
)

func validateTaskGrader(g *models.TaskEvalGrader) error {
	if g == nil || (g.OutputSchema == "" && len(g.ExpectedJSON) == 0 && len(g.RequiredTools) == 0 && len(g.RequiredArtifacts) == 0 && len(g.ForbiddenTools) == 0 && g.MaxModelSteps == 0 && g.MaxToolCalls == 0 && g.ExpectedStopReason == "") {
		return fmt.Errorf("task mode requires a deterministic schema, reference JSON, tool or artifact grader")
	}
	if g.OutputSchema != "" {
		var schema map[string]any
		if json.Unmarshal([]byte(g.OutputSchema), &schema) != nil || len(schema) == 0 {
			return fmt.Errorf("task output schema grader must be a nonempty JSON object")
		}
		if schema["type"] == nil && schema["required"] == nil && schema["const"] == nil && schema["enum"] == nil {
			return fmt.Errorf("task output schema has no supported assertion")
		}
	}
	for key := range g.ExpectedJSON {
		if key != "" && !strings.HasPrefix(key, "/") {
			return fmt.Errorf("expected_json key %q must be a JSON pointer", key)
		}
	}
	for key, hash := range g.RequiredArtifacts {
		if key == "" || !strings.HasPrefix(hash, "sha256:") || len(hash) != 71 {
			return fmt.Errorf("artifact grader requires key and sha256 content hash")
		}
		if _, err := hex.DecodeString(strings.TrimPrefix(hash, "sha256:")); err != nil {
			return err
		}
	}
	for _, tool := range g.RequiredTools {
		if strings.TrimSpace(tool) == "" {
			return fmt.Errorf("required tool ID is empty")
		}
	}
	if g.MaxModelSteps < 0 || g.MaxToolCalls < 0 {
		return fmt.Errorf("grader limits must be positive")
	}
	return nil
}

func (e *Evaluator) evaluateTaskCase(ctx context.Context, owner, profileID string, c models.PromptEvalCase, v models.PromptEvalCaseResult) models.PromptEvalCaseResult {
	if err := validateTaskGrader(c.Grader); err != nil {
		v.Detail = err.Error()
		return v
	}
	if c.Task == nil || strings.TrimSpace(c.Task.Description) == "" {
		v.Detail = "task mode requires an explicit task description"
		return v
	}
	if nilInterface(e.TaskRunner) {
		v.Status, v.Detail = "skipped", "task runner is not configured"
		return v
	}
	// Only task inputs cross this boundary. Reference answers are never exposed
	// to the production runner, its compaction state or memory stores.
	raw, err := json.Marshal(c.Task)
	if err != nil {
		v.Detail = "task input cannot be serialized: " + err.Error()
		return v
	}
	var input models.PromptEvalTask
	if err := json.Unmarshal(raw, &input); err != nil {
		v.Detail = err.Error()
		return v
	}
	if c.Task.Contract.AllowedTools != nil {
		input.Contract.AllowedTools = append([]string{}, c.Task.Contract.AllowedTools...)
	}
	id := "eval-" + uuid.NewString()
	task := &models.Task{ID: id + "/task", Name: c.Name, Type: "agent_evaluation", Description: input.Description, AssignedTo: profileID, Inputs: input.Inputs, Contract: input.Contract,
		Metadata: map[string]any{"tenant_id": owner, "agent_profile_id": profileID, "plan_id": id, "session_id": id, "evaluation": true, "prompt_variables": c.Variables}}
	ctx = modelresolver.WithBranch(modelresolver.WithOwner(ctx, owner), owner, "agent-evaluation", id, id+"/task")
	recorder := usage.NewRecorder()
	ctx = usage.WithRecorder(ctx, recorder)
	started := time.Now()
	v.Attempted = true
	err = e.TaskRunner.ExecuteTask(ctx, task, map[string]interface{}{"context_managed": true})
	observed := recorder.Snapshot()
	v.Usage = &models.TaskUsage{LLMCalls: observed.LLMCalls, LLMPromptTokens: observed.PromptTokens, LLMCompletionTokens: observed.CompletionTokens, LLMCostUSD: observed.CostUSD, LLMEstimated: observed.Estimated, CostSource: observed.CostSource, DurationSeconds: time.Since(started).Seconds()}
	v.ModelProfileID, _ = task.Metadata["model_profile_id"].(string)
	v.ModelRevision, _ = task.Metadata["model_profile_revision"].(int)
	if task.AgentRun != nil {
		v.ModelInvoked = task.AgentRun.Steps > 0
		v.RunID = task.AgentRun.ID
	}
	if err != nil {
		v.Status, v.Detail = "error", "agent infrastructure error: "+err.Error()
		if task.Status == models.StatusFailed || task.Status == models.StatusBlocked || task.Status == models.StatusCanceled {
			v.Status, v.Detail = "failed", "agent execution failed: "+err.Error()
		}
		if task.Failure != nil && (task.Failure.Class == "transient" || task.Failure.Class == "environment") {
			v.Status, v.Detail = "error", "execution infrastructure unavailable: "+err.Error()
		}
		if c.Grader.ExpectedStatus == "" || c.Grader.ExpectedStatus == models.StatusCompleted || task.Status != c.Grader.ExpectedStatus || v.Status == "error" {
			return v
		}
	}
	v.Status, v.Detail = "failed", "task did not satisfy the frozen grader"
	if err := gradeTask(task, c.Grader); err != nil {
		v.Detail = err.Error()
		return v
	}
	v.Status, v.Passed, v.Score, v.Detail = "passed", true, 1, "deterministic task contract passed"
	for _, call := range task.ToolCalls {
		v.EvidenceRefs = append(v.EvidenceRefs, "tool_call:"+call.ID)
	}
	for _, artifact := range task.Artifacts {
		v.EvidenceRefs = append(v.EvidenceRefs, "artifact:"+artifact.Key)
	}
	return v
}

func gradeTask(task *models.Task, g *models.TaskEvalGrader) error {
	expected := g.ExpectedStatus
	if expected == "" {
		expected = models.StatusCompleted
	}
	if task.Status != expected {
		return fmt.Errorf("task status %q, expected %q", task.Status, expected)
	}
	if task.Status == models.StatusCompleted && strings.TrimSpace(task.Result) == "" {
		return fmt.Errorf("completed task has no output")
	}
	if task.Status == models.StatusCompleted && task.Error != "" {
		return fmt.Errorf("completed task retains execution error: %s", task.Error)
	}
	if g.MaxModelSteps > 0 || g.MaxToolCalls > 0 || g.ExpectedStopReason != "" {
		if task.AgentRun == nil {
			return fmt.Errorf("agent execution counters are missing")
		}
		if g.MaxModelSteps > 0 && task.AgentRun.Steps > g.MaxModelSteps {
			return fmt.Errorf("model step budget exceeded")
		}
		if g.MaxToolCalls > 0 && task.AgentRun.ToolCalls > g.MaxToolCalls {
			return fmt.Errorf("tool call budget exceeded")
		}
		if g.ExpectedStopReason != "" && task.AgentRun.StopReason != g.ExpectedStopReason {
			return fmt.Errorf("unexpected stop reason: %s", task.AgentRun.StopReason)
		}
	}
	for _, forbidden := range g.ForbiddenTools {
		for _, call := range task.ToolCalls {
			if call.ToolID == forbidden && call.Status == models.ToolCallCompleted && !call.DryRun {
				return fmt.Errorf("forbidden tool %s executed", forbidden)
			}
		}
	}
	if g.OutputSchema != "" || len(g.ExpectedJSON) > 0 {
		var output any
		if err := json.Unmarshal([]byte(task.Result), &output); err != nil {
			return fmt.Errorf("task output is not JSON: %w", err)
		}
		if violations := tools.ValidateArguments(g.OutputSchema, output); len(violations) > 0 {
			return fmt.Errorf("task output schema: %s", violations[0].Error())
		}
		for path, expected := range g.ExpectedJSON {
			actual, ok := jsonPointer(output, path)
			if !ok || !jsonEquivalent(actual, expected) {
				return fmt.Errorf("reference mismatch at %q", path)
			}
		}
	}
	planID, _ := task.Metadata["plan_id"].(string)
	profileID, _ := task.Metadata["agent_profile_id"].(string)
	for _, toolID := range g.RequiredTools {
		found := false
		for _, call := range task.ToolCalls {
			if call.ToolID != toolID || call.Status != models.ToolCallCompleted || call.DryRun || call.ID == "" || call.PlanID != planID || call.TaskID != task.ID || call.AgentProfileID != profileID || call.ArgumentsHash != models.HashArguments(call.Arguments) || call.ResultHash == "" {
				continue
			}
			for _, artifact := range task.Artifacts {
				callID, _ := artifact.Metadata["tool_call_id"].(string)
				hash, _ := artifact.Metadata["result_hash"].(string)
				if callID == call.ID && hash == call.ResultHash && !call.Truncated && artifact.ProducerTaskID == task.ID && strings.TrimSpace(artifact.EffectiveValue()) != "" {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("required tool %s has no completed invocation and linked evidence", toolID)
		}
	}
	for key, expectedHash := range g.RequiredArtifacts {
		found := false
		for _, a := range task.Artifacts {
			if a.Key != key || a.ProducerTaskID != task.ID {
				continue
			}
			sum := sha256.Sum256([]byte(a.EffectiveValue()))
			if "sha256:"+hex.EncodeToString(sum[:]) == expectedHash {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("artifact %s content or producer does not match", key)
		}
	}
	return nil
}

func jsonEquivalent(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	var aa, bb any
	if json.Unmarshal(left, &aa) != nil || json.Unmarshal(right, &bb) != nil {
		return false
	}
	return reflect.DeepEqual(aa, bb)
}

func jsonPointer(value any, path string) (any, bool) {
	if path == "" {
		return value, true
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		switch current := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = current[part]
			if !ok {
				return nil, false
			}
		case []any:
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || index >= len(current) {
				return nil, false
			}
			value = current[index]
		default:
			return nil, false
		}
	}
	return value, true
}
