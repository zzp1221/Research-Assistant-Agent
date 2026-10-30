package agentprofile

import (
	"context"
	"fmt"
	"strings"
	"time"

	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
)

// Completer is the model surface used by evaluation and profile execution.
// modelresolver.Resolver implements it; tests inject a deterministic fake.
type Completer interface {
	Complete(ctx context.Context, ownerID string, messages []oaichat.Message, opts oaichat.Options) (*oaichat.Response, modelprofile.Resolved, error)
	ResolveProfile(ownerID, profileID string) (modelprofile.Resolved, error)
	CompleteWithResolved(ctx context.Context, resolved modelprofile.Resolved, messages []oaichat.Message, opts oaichat.Options) (*oaichat.Response, error)
}

// Evaluator runs deterministic prompt evaluations.
type Evaluator struct {
	Registry  *Registry
	Completer Completer
}

// Evaluate renders the prompt for each case and scores expected substrings.
// Template mode is deterministic and needs no model; model mode exercises the
// real provider and is opt-in.
func (e *Evaluator) Evaluate(ctx context.Context, ownerID, promptID string, cases []models.PromptEvalCase, useModel bool, modelProfileID string) (models.PromptEvalResult, error) {
	prompt, err := e.Registry.GetPromptForOwner(ownerID, promptID)
	if err != nil {
		return models.PromptEvalResult{}, err
	}
	if len(cases) == 0 {
		cases = []models.PromptEvalCase{{Name: "default"}}
	}
	mode := "template"
	if useModel {
		mode = "model"
	}
	result := models.PromptEvalResult{PromptVersionID: prompt.ID, Mode: mode, EvaluatedAt: time.Now().UTC()}
	for _, evalCase := range cases {
		variables := map[string]string{}
		for key, value := range evalCase.Variables {
			variables[key] = value
		}
		rendered, renderErr := RenderTemplate(prompt.Template, variables)
		if renderErr != nil {
			result.Cases = append(result.Cases, models.PromptEvalCaseResult{Name: evalCase.Name, Passed: false, Detail: renderErr.Error()})
			result.Total++
			continue
		}
		output := rendered
		if useModel && e.Completer != nil {
			var response *oaichat.Response
			var callErr error
			if strings.TrimSpace(modelProfileID) != "" {
				resolved, resolveErr := e.Completer.ResolveProfile(ownerID, modelProfileID)
				if resolveErr != nil {
					callErr = resolveErr
				} else {
					response, callErr = e.Completer.CompleteWithResolved(ctx, resolved, []oaichat.Message{{Role: "user", Text: rendered}}, oaichat.Options{MaxOutputTokens: 512})
				}
			} else {
				response, _, callErr = e.Completer.Complete(ctx, ownerID, []oaichat.Message{{Role: "user", Text: rendered}}, oaichat.Options{MaxOutputTokens: 512})
			}
			if callErr != nil {
				result.Cases = append(result.Cases, models.PromptEvalCaseResult{Name: evalCase.Name, Passed: false, Detail: callErr.Error()})
				result.Total++
				continue
			}
			if response != nil {
				output = response.Content
			}
		}
		passed := true
		detail := "rendered"
		if strings.TrimSpace(evalCase.ExpectedContains) != "" {
			passed = strings.Contains(output, evalCase.ExpectedContains)
			if passed {
				detail = "contains expected text"
			} else {
				detail = "missing expected text: " + evalCase.ExpectedContains
			}
		}
		score := 0.0
		if passed {
			score = 1.0
		}
		result.Cases = append(result.Cases, models.PromptEvalCaseResult{Name: evalCase.Name, Passed: passed, Score: score, Detail: detail})
		result.Total++
		if passed {
			result.Passed++
		}
	}
	if result.Total > 0 {
		result.Score = float64(result.Passed) / float64(result.Total)
	}
	detail := fmt.Sprintf("%s: %d/%d cases passed", mode, result.Passed, result.Total)
	_ = e.Registry.UpdatePromptEval(prompt.ID, result.Score, detail)
	return result, nil
}
