package agentprofile

import (
	"context"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
)

type fakeCompleter struct {
	responses map[string]string
	model     string
}

func (f *fakeCompleter) pick(messages []oaichat.Message) string {
	text := ""
	if len(messages) > 0 {
		text = messages[0].Text
	}
	for marker, response := range f.responses {
		if strings.Contains(text, marker) {
			return response
		}
	}
	return "UNMATCHED:" + text
}

func (f *fakeCompleter) Complete(_ context.Context, _ string, messages []oaichat.Message, _ oaichat.Options) (*oaichat.Response, modelprofile.Resolved, error) {
	return &oaichat.Response{Content: f.pick(messages)}, modelprofile.Resolved{Profile: modelprofile.Profile{Model: f.model}}, nil
}

func (f *fakeCompleter) ResolveProfile(_ string, _ string) (modelprofile.Resolved, error) {
	return modelprofile.Resolved{Profile: modelprofile.Profile{Model: f.model, Revision: 3}}, nil
}

func (f *fakeCompleter) CompleteWithResolved(_ context.Context, _ modelprofile.Resolved, messages []oaichat.Message, _ oaichat.Options) (*oaichat.Response, error) {
	return &oaichat.Response{Content: f.pick(messages)}, nil
}

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry(NewMemoryStore())
	registry.SetClock(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	if err := registry.EnsureBuiltins(); err != nil {
		t.Fatal(err)
	}
	return registry
}

func TestPromptVersionImmutableAndActivate(t *testing.T) {
	registry := newTestRegistry(t)
	v1, err := registry.CreatePrompt("owner-a", "assistant", "Answer {{question}} as v1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != 1 || len(v1.Variables) != 1 || v1.Variables[0] != "question" {
		t.Fatalf("unexpected v1: %+v", v1)
	}
	v2, err := registry.CreatePrompt("owner-a", "assistant", "Answer {{question}} as v2", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if v2.Version != 2 {
		t.Fatalf("expected version 2, got %d", v2.Version)
	}
	original, _ := registry.GetPrompt(v1.ID)
	if original.Template != "Answer {{question}} as v1" {
		t.Fatal("v1 template must stay immutable")
	}
	if _, err := registry.ActivatePrompt("owner-a", v2.ID); err != nil {
		t.Fatal(err)
	}
	first, _ := registry.GetPrompt(v1.ID)
	second, _ := registry.GetPrompt(v2.ID)
	if first.Status != models.PromptStatusArchived || second.Status != models.PromptStatusActive {
		t.Fatalf("unexpected statuses: %s / %s", first.Status, second.Status)
	}
	if _, err := registry.ActivatePrompt("owner-a", v1.ID); err != nil {
		t.Fatal(err)
	}
	first, _ = registry.GetPrompt(v1.ID)
	second, _ = registry.GetPrompt(v2.ID)
	if first.Status != models.PromptStatusActive || second.Status != models.PromptStatusArchived {
		t.Fatalf("rollback should only move the active pointer: %s / %s", first.Status, second.Status)
	}
}

func TestRenderTemplate(t *testing.T) {
	rendered, err := RenderTemplate("Hello {{name}}, task={{task}}", map[string]string{"name": "A", "task": "T"})
	if err != nil || rendered != "Hello A, task=T" {
		t.Fatalf("render: %q %v", rendered, err)
	}
	if _, err := RenderTemplate("Hello {{missing}}", map[string]string{}); err == nil {
		t.Fatal("expected missing variable error")
	}
}

func TestEvaluatorTemplateMode(t *testing.T) {
	registry := newTestRegistry(t)
	prompt, err := registry.CreatePrompt("owner-a", "eval", "Say {{word}}", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	evaluator := Evaluator{Registry: registry}
	result, err := evaluator.Evaluate(context.Background(), "owner-a", prompt.ID, []models.PromptEvalCase{
		{Name: "ok", Variables: map[string]string{"word": "hello"}, ExpectedContains: "hello"},
		{Name: "missing", Variables: map[string]string{}, ExpectedContains: "hello"},
	}, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 || result.Passed != 1 || result.Score != 0.5 {
		t.Fatalf("unexpected eval: %+v", result)
	}
	stored, _ := registry.GetPrompt(prompt.ID)
	if stored.EvalScore != 0.5 || stored.EvalDetail == "" {
		t.Fatalf("eval result not stored: %+v", stored)
	}
}

func TestProfileRunnerBehaviorChangeAndRollback(t *testing.T) {
	registry := newTestRegistry(t)
	v1, err := registry.CreatePrompt("owner-a", "assistant", "Reply with token PROMPT_V1", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := registry.CreatePrompt("owner-a", "assistant", "Reply with token PROMPT_V2", "owner-a")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := registry.SaveProfile(models.AgentProfile{
		ID: "profile:p1", OwnerID: "owner-a", Name: "Custom", PromptVersionID: v1.ID,
		ModelProfileID: "model-x", ToolAllowlist: []string{"native:echo"}, MaxSteps: 4, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	completer := &fakeCompleter{model: "fake-model", responses: map[string]string{
		"PROMPT_V1": "answer-v1",
		"PROMPT_V2": "answer-v2",
	}}
	runner := &ProfileRunner{Profiles: registry, Completer: completer}
	run := func() *models.Task {
		task := &models.Task{
			ID: "task-1", Name: "custom", AssignedTo: "profile:p1",
			Metadata: map[string]any{"tenant_id": "owner-a", "agent_profile_id": "profile:p1"},
		}
		if err := runner.ExecuteTask(context.Background(), task, nil); err != nil {
			t.Fatalf("execute: %v", err)
		}
		return task
	}
	task := run()
	if task.Result != "answer-v1" || task.Metadata["prompt_version"] != 1 {
		t.Fatalf("expected v1 behavior, got %q meta=%+v", task.Result, task.Metadata)
	}
	if _, err := registry.UpdateProfile("owner-a", false, profile.ID, func(p *models.AgentProfile) error {
		p.PromptVersionID = v2.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	task = run()
	if task.Result != "answer-v2" || task.Metadata["prompt_version"] != 2 {
		t.Fatalf("expected v2 behavior, got %q", task.Result)
	}
	// Rollback: activate v1 and point the profile back at it.
	if _, err := registry.ActivatePrompt("owner-a", v1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.UpdateProfile("owner-a", false, profile.ID, func(p *models.AgentProfile) error {
		p.PromptVersionID = v1.ID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	task = run()
	if task.Result != "answer-v1" {
		t.Fatalf("expected rollback to v1, got %q", task.Result)
	}
}

func TestBuiltinProfilesSeeded(t *testing.T) {
	registry := newTestRegistry(t)
	profiles, err := registry.ListProfiles("anyone")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, profile := range profiles {
		if profile.Builtin {
			count++
		}
	}
	if count < 8 {
		t.Fatalf("expected builtin profiles, got %d", count)
	}
}
