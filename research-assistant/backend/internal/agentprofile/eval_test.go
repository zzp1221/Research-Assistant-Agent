package agentprofile

import (
	"context"
	"errors"
	"strings"
	"testing"

	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
)

type evalCompleter struct {
	response  *oaichat.Response
	err       error
	panicCall bool
	calls     int
}

func (f *evalCompleter) Complete(context.Context, string, []oaichat.Message, oaichat.Options) (*oaichat.Response, modelprofile.Resolved, error) {
	f.calls++
	if f.panicCall {
		panic("grader infrastructure")
	}
	return f.response, modelprofile.Resolved{}, f.err
}
func (f *evalCompleter) ResolveProfile(string, string) (modelprofile.Resolved, error) {
	return modelprofile.Resolved{}, nil
}
func (f *evalCompleter) CompleteWithResolved(ctx context.Context, r modelprofile.Resolved, m []oaichat.Message, o oaichat.Options) (*oaichat.Response, error) {
	v, _, err := f.Complete(ctx, "", m, o)
	return v, err
}

func TestEvaluatorRejectsEmptyCasesAndMissingGraders(t *testing.T) {
	r := newTestRegistry(t)
	p, _ := r.CreatePrompt("owner", "eval", "SUCCESS {{word}}", "owner")
	e := Evaluator{Registry: r}
	if _, err := e.Evaluate(context.Background(), "owner", p.ID, nil, false, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty cases: %v", err)
	}
	result, err := e.Evaluate(context.Background(), "owner", p.ID, []models.PromptEvalCase{{Variables: map[string]string{"word": "test"}}}, false, "")
	if err != nil || result.Passed != 0 || result.Errors != 1 || result.Scored != 0 || result.ScoredRate != nil || result.ScoreAvailable {
		t.Fatalf("missing grader passed: %+v err=%v", result, err)
	}
	stored, _ := r.GetPrompt(p.ID)
	if !strings.Contains(stored.EvalDetail, "N/A") {
		t.Fatalf("zero score denominator not recorded: %+v", stored)
	}
}

func TestEvaluatorModelCannotPassUsingTemplateOrEmptyResponse(t *testing.T) {
	r := newTestRegistry(t)
	p, _ := r.CreatePrompt("owner", "eval", "SUCCESS", "owner")
	var typedNil *evalCompleter
	for _, tc := range []struct {
		name      string
		completer Completer
	}{
		{"missing", nil}, {"typed-nil", typedNil}, {"nil-response", &evalCompleter{}}, {"empty-response", &evalCompleter{response: &oaichat.Response{Content: "  "}}}, {"provider-error", &evalCompleter{err: errors.New("unavailable")}}, {"panic", &evalCompleter{panicCall: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Evaluator{Registry: r, Completer: tc.completer}
			result, err := e.Evaluate(context.Background(), "owner", p.ID, []models.PromptEvalCase{{ExpectedContains: "SUCCESS"}}, true, "")
			if err != nil || result.Passed != 0 || result.Errors != 1 || result.Scored != 0 || result.ScoredRate != nil {
				t.Fatalf("model falsely scored: %+v err=%v", result, err)
			}
			if result.Cases[0].Mode != "model" || result.Cases[0].Status != "error" || result.GraderVersion == "" {
				t.Fatalf("missing mode/status/version: %+v", result)
			}
		})
	}
}

func TestEvaluatorReportsBothDenominators(t *testing.T) {
	r := newTestRegistry(t)
	p, _ := r.CreatePrompt("owner", "eval", "SUCCESS", "owner")
	e := Evaluator{Registry: r, Completer: &evalCompleter{response: &oaichat.Response{Content: "SUCCESS"}}}
	result, err := e.Evaluate(context.Background(), "owner", p.ID, []models.PromptEvalCase{{ExpectedContains: "SUCCESS"}, {ExpectedContains: "wrong"}, {}}, true, "")
	if err != nil || result.Passed != 1 || result.Failed != 1 || result.Errors != 1 || result.Total != 3 || result.Scored != 2 || result.Attempted != 2 || result.Score != .5 || result.PlannedRate == nil || *result.PlannedRate != 1.0/3 {
		t.Fatalf("denominator lost ungraded case: %+v %v", result, err)
	}
}

func TestEvaluatorExplicitTemplateBindingCheck(t *testing.T) {
	r := newTestRegistry(t)
	p, _ := r.CreatePrompt("owner", "eval", "Answer {{question}}", "owner")
	e := Evaluator{Registry: r}
	result, err := e.EvaluateMode(context.Background(), "owner", p.ID, []models.PromptEvalCase{{CheckRendering: true, Variables: map[string]string{"question": "hello"}}}, "template", "", "")
	if err != nil || result.Passed != 1 || result.Cases[0].ModelInvoked {
		t.Fatalf("explicit template check: %+v %v", result, err)
	}
}
