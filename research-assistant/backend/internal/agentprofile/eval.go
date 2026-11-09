package agentprofile

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/usage"
)

type Completer interface {
	Complete(context.Context, string, []oaichat.Message, oaichat.Options) (*oaichat.Response, modelprofile.Resolved, error)
	ResolveProfile(string, string) (modelprofile.Resolved, error)
	CompleteWithResolved(context.Context, modelprofile.Resolved, []oaichat.Message, oaichat.Options) (*oaichat.Response, error)
}
type TaskEvalRunner interface {
	ExecuteTask(context.Context, *models.Task, map[string]interface{}) error
}

const EvalGraderVersion = "agent-eval/v1"

type Evaluator struct {
	Registry   *Registry
	Completer  Completer
	TaskRunner TaskEvalRunner
}

func (e *Evaluator) Evaluate(ctx context.Context, owner, promptID string, cases []models.PromptEvalCase, useModel bool, modelID string) (models.PromptEvalResult, error) {
	mode := "template"
	if useModel {
		mode = "model"
	}
	return e.EvaluateMode(ctx, owner, promptID, cases, mode, modelID, "")
}
func (e *Evaluator) EvaluateMode(ctx context.Context, owner, promptID string, cases []models.PromptEvalCase, mode, modelID, profileID string) (models.PromptEvalResult, error) {
	if mode == "" {
		mode = "template"
	}
	if mode != "template" && mode != "model" && mode != "task" {
		return models.PromptEvalResult{}, fmt.Errorf("%w: evaluation mode must be template, model or task", ErrInvalid)
	}
	if len(cases) == 0 {
		return models.PromptEvalResult{}, fmt.Errorf("%w: evaluation requires explicit cases and graders", ErrInvalid)
	}
	prompt, err := e.Registry.GetPromptForOwner(owner, promptID)
	if err != nil {
		return models.PromptEvalResult{}, err
	}
	if mode == "task" {
		profile, profileErr := e.Registry.GetProfileForOwner(owner, profileID)
		if profileErr != nil {
			return models.PromptEvalResult{}, profileErr
		}
		if profile.PromptVersionID != promptID {
			return models.PromptEvalResult{}, fmt.Errorf("%w: task profile does not bind this prompt", ErrInvalid)
		}
	}
	result := models.PromptEvalResult{PromptVersionID: prompt.ID, ProfileID: profileID, Mode: mode, GraderVersion: EvalGraderVersion, EvaluatedAt: time.Now().UTC(), Cases: []models.PromptEvalCaseResult{}}
	for _, evalCase := range cases {
		verdict := e.evaluateCase(ctx, owner, prompt.Template, modelID, profileID, mode, evalCase)
		result.Cases = append(result.Cases, verdict)
		result.Total++
		if verdict.Attempted {
			result.Attempted++
		}
		switch verdict.Status {
		case "passed":
			result.Passed++
			result.Scored++
		case "failed":
			result.Failed++
			result.Scored++
		case "skipped":
			result.Skipped++
		default:
			result.Errors++
		}
	}
	if result.Scored > 0 {
		score := float64(result.Passed) / float64(result.Scored)
		result.Score, result.ScoredRate, result.ScoreAvailable = score, &score, true
	}
	if result.Total > 0 {
		rate := float64(result.Passed) / float64(result.Total)
		result.PlannedRate = &rate
	}
	detail := fmt.Sprintf("%s [%s]: %d/%d planned, %d scored, %d error, %d skipped", mode, EvalGraderVersion, result.Passed, result.Total, result.Scored, result.Errors, result.Skipped)
	if !result.ScoreAvailable {
		detail += "; score N/A"
	}
	if err := e.Registry.UpdatePromptEval(prompt.ID, result.Score, detail); err != nil {
		return result, err
	}
	return result, nil
}
func (e *Evaluator) evaluateCase(ctx context.Context, owner, template, modelID, profileID, mode string, c models.PromptEvalCase) (v models.PromptEvalCaseResult) {
	v = models.PromptEvalCaseResult{Name: c.Name, Mode: mode, Status: "error", GraderVersion: EvalGraderVersion}
	defer func() {
		if recovered := recover(); recovered != nil {
			v.Status, v.Passed, v.Score, v.Detail = "error", false, 0, fmt.Sprintf("evaluation infrastructure/grader panic: %v", recovered)
		}
	}()
	if mode == "task" {
		return e.evaluateTaskCase(ctx, owner, profileID, c, v)
	}
	if strings.TrimSpace(c.ExpectedContains) == "" && !(mode == "template" && c.CheckRendering) {
		v.Detail = "no effective grader: provide expected_contains or explicit template check_rendering"
		return
	}
	rendered, err := RenderTemplate(template, c.Variables)
	if err != nil {
		v.Status, v.Detail = "failed", err.Error()
		return
	}
	output := rendered
	if mode == "model" {
		if nilInterface(e.Completer) {
			v.Detail = "model completer is not configured"
			return
		}
		var response *oaichat.Response
		var resolved modelprofile.Resolved
		recorder := usage.NewRecorder()
		ctx = usage.WithRecorder(ctx, recorder)
		started := time.Now()
		if modelID != "" {
			var resolveErr error
			resolved, resolveErr = e.Completer.ResolveProfile(owner, modelID)
			if resolveErr != nil {
				v.Detail = resolveErr.Error()
				return
			}
			v.Attempted, v.ModelInvoked = true, true
			response, err = e.Completer.CompleteWithResolved(ctx, resolved, []oaichat.Message{{Role: "user", Text: rendered}}, oaichat.Options{})
		} else {
			v.Attempted, v.ModelInvoked = true, true
			response, resolved, err = e.Completer.Complete(ctx, owner, []oaichat.Message{{Role: "user", Text: rendered}}, oaichat.Options{})
		}
		v.ModelProfileID, v.ModelRevision = resolved.Profile.ID, resolved.Profile.Revision
		if response != nil && !response.Reused {
			if response.CostUnknown {
				recorder.RecordLLMUnknownCost(resolved.Profile.Model, response.PromptTokens, response.CompletionTokens, response.EstimatedTokens)
			} else {
				recorder.RecordLLMWithPrice(resolved.Profile.Model, response.PromptTokens, response.CompletionTokens, response.EstimatedTokens, usage.Price{PromptPer1K: resolved.Profile.PromptPricePer1K, CompletionPer1K: resolved.Profile.CompletionPricePer1K})
			}
		}
		observed := recorder.Snapshot()
		v.Usage = &models.TaskUsage{LLMCalls: observed.LLMCalls, LLMPromptTokens: observed.PromptTokens, LLMCompletionTokens: observed.CompletionTokens, LLMCostUSD: observed.CostUSD, LLMEstimated: observed.Estimated, CostSource: observed.CostSource, DurationSeconds: time.Since(started).Seconds(), Attempts: 1}
		if err != nil {
			v.Detail = "model execution error: " + err.Error()
			return
		}
		if response == nil || strings.TrimSpace(response.Content) == "" {
			v.Detail = "model returned no usable response"
			return
		}
		if response.Reused {
			v.ModelInvoked = false
			v.Detail = "model response reused without a new execution"
			return
		}
		if len(response.ToolCalls) > 0 {
			v.Detail = "model requested tools; use task mode to execute and grade them"
			return
		}
		if response.FinishReason == "length" || response.FinishReason == "content_filter" {
			v.Detail = "model response did not finish normally: " + response.FinishReason
			return
		}
		output = response.Content
	} else {
		v.Attempted = true
	}
	v.Status, v.Detail = "passed", "template rendered with all variable bindings"
	if c.ExpectedContains != "" {
		if strings.Contains(output, c.ExpectedContains) {
			v.Detail = "contains expected text"
		} else {
			v.Status, v.Detail = "failed", "missing expected text: "+c.ExpectedContains
		}
	}
	v.Passed = v.Status == "passed"
	if v.Passed {
		v.Score = 1
	}
	return
}
func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}
