package agentprofile

import (
	"context"
	"errors"
	"strings"
	"testing"

	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/prompts"
)

type contextCompleter struct {
	call            modelprofile.Resolved
	resolveErr      error
	profileResolves int
	defaultResolves int
	defaultCalls    int
	resolvedCalls   int
	messages        []oaichat.Message
	options         oaichat.Options
	used            modelprofile.Resolved
}

func (f *contextCompleter) Complete(_ context.Context, _ string, messages []oaichat.Message, opts oaichat.Options) (*oaichat.Response, modelprofile.Resolved, error) {
	f.defaultCalls++
	f.messages, f.options = messages, opts
	return &oaichat.Response{Content: "ok"}, f.call, nil
}

func (f *contextCompleter) ResolveProfile(string, string) (modelprofile.Resolved, error) {
	f.profileResolves++
	return f.call, f.resolveErr
}

func (f *contextCompleter) ResolveCall(context.Context) (modelprofile.Resolved, error) {
	f.defaultResolves++
	return f.call, f.resolveErr
}

func (f *contextCompleter) CompleteWithResolved(_ context.Context, call modelprofile.Resolved, messages []oaichat.Message, opts oaichat.Options) (*oaichat.Response, error) {
	f.resolvedCalls++
	f.messages, f.options, f.used = messages, opts, call
	return &oaichat.Response{Content: "ok"}, nil
}

func profileContextFixture(t *testing.T, modelID, template string) (*ProfileRunner, *models.Task, *contextCompleter) {
	t.Helper()
	registry := newTestRegistry(t)
	promptID := ""
	if template != "" {
		prompt, err := registry.CreatePrompt("owner", "context", template, "owner")
		if err != nil {
			t.Fatal(err)
		}
		promptID = prompt.ID
	}
	_, err := registry.SaveProfile(models.AgentProfile{ID: "profile:context", OwnerID: "owner", Name: "Context", Enabled: true, PromptVersionID: promptID, ModelProfileID: modelID})
	if err != nil {
		t.Fatal(err)
	}
	fake := &contextCompleter{call: modelprofile.Resolved{Profile: modelprofile.Profile{
		ID: "model-small", Model: "small", Revision: 7, ContextWindowTokens: 32000, MaxOutputTokens: 2048,
	}}}
	task := &models.Task{ID: "task", Name: "Report", Type: "custom", AssignedTo: "profile:context", Description: "Analyze the current task", Metadata: map[string]any{"tenant_id": "owner"}}
	return &ProfileRunner{Profiles: registry, Completer: fake}, task, fake
}

func TestProfileRunnerIncludesBoundInputsAndSharedContext(t *testing.T) {
	runner, task, fake := profileContextFixture(t, "model-small", "Summarize {{question}}")
	task.Inputs = map[string]any{"question": "TEMPLATE_VALUE", "upstream": "UPSTREAM_EVIDENCE", "structured": map[string]any{"value": 42}}
	task.Contract.AllowedTools = []string{"native:echo"}
	plan := &models.PlanGraph{ID: "plan", UserIntent: "PLAN_GOAL", Context: &models.ResearchContext{ParentPlanSummary: "PARENT_SUMMARY"}}
	shared := map[string]interface{}{
		"plan_graph": plan, "core_memory": "CORE_REFERENCE", "retrieved_memory": "RETRIEVED_REFERENCE",
		"wiki_context": "WIKI_REFERENCE", "memory_refs": []string{"memory-1"}, "wiki_refs": []string{"paper-1"},
		"prompt_budget":   prompts.PromptBudget{ContextWindow: 128000, MaxOutput: 8192, Safety: 6400},
		"context_managed": true, "internal_secret": "DO_NOT_SEND",
	}
	if err := runner.ExecuteTask(context.Background(), task, shared); err != nil {
		t.Fatal(err)
	}
	if fake.profileResolves != 1 || fake.resolvedCalls != 1 || fake.defaultCalls != 0 {
		t.Fatalf("unexpected calls: %+v", fake)
	}
	if len(fake.messages) != 2 || fake.messages[0].Role != "system" || fake.messages[1].Role != "user" {
		t.Fatalf("source roles missing: %+v", fake.messages)
	}
	text := fake.messages[1].Text
	for _, marker := range []string{"PLAN_GOAL", "PARENT_SUMMARY", "Analyze the current task", "Summarize TEMPLATE_VALUE", "UPSTREAM_EVIDENCE", "42", "CORE_REFERENCE", "RETRIEVED_REFERENCE", "WIKI_REFERENCE", "native:echo"} {
		if !strings.Contains(text, marker) {
			t.Errorf("context missing %q: %s", marker, text)
		}
		if strings.Contains(fake.messages[0].Text, marker) {
			t.Errorf("source material promoted to system: %q", marker)
		}
	}
	if strings.Count(text, "TEMPLATE_VALUE") != 1 || strings.Contains(text, "DO_NOT_SEND") {
		t.Fatal("template input duplicated or unrelated metadata exposed")
	}
	report := task.Metadata["prompt_budget"].(prompts.PromptReport)
	if report.Budget != 32000-2048-1600 || fake.options.MaxOutputTokens != 2048 {
		t.Fatalf("budget did not use actual profile: %+v opts=%+v", report, fake.options)
	}
	if task.Metadata["memory_refs"] == nil || task.Metadata["wiki_refs"] == nil {
		t.Fatal("source references were not retained")
	}
}

func TestProfileRunnerUsesPinnedModelWithoutResolvingAgain(t *testing.T) {
	runner, task, fake := profileContextFixture(t, "model-small", "Report")
	fake.resolveErr = errors.New("profile edited after scheduler resolution")
	pinned := fake.call
	pinned.Profile.Revision = 3
	ctx := modelresolver.WithResolvedCall(context.Background(), pinned)
	if err := runner.ExecuteTask(ctx, task, nil); err != nil {
		t.Fatal(err)
	}
	if fake.profileResolves != 0 || fake.defaultResolves != 0 || fake.used.Profile.Revision != 3 || task.Metadata["model_profile_revision"] != 3 {
		t.Fatalf("pinned configuration was not preserved: %+v metadata=%+v", fake, task.Metadata)
	}
}

func TestProfileRunnerPreservesEvidenceWhenJSONPromptVariablesOverrideIt(t *testing.T) {
	runner, task, fake := profileContextFixture(t, "model-small", "Summarize {{question}}")
	task.Inputs = map[string]any{"question": "ORIGINAL_EVIDENCE"}
	task.Metadata["prompt_variables"] = map[string]any{"question": "CURRENT_QUESTION", "non_string": 42}
	if err := runner.ExecuteTask(context.Background(), task, nil); err != nil {
		t.Fatal(err)
	}
	text := fake.messages[1].Text
	if !strings.Contains(text, "Summarize CURRENT_QUESTION") || !strings.Contains(text, "ORIGINAL_EVIDENCE") {
		t.Fatalf("JSON-restored prompt variables lost their binding or original evidence: %s", text)
	}
	if _, ok := task.Inputs["question"]; !ok {
		t.Fatal("prompt assembly mutated task inputs")
	}
}

func TestProfileRunnerResolvesDefaultBeforePromptBudget(t *testing.T) {
	runner, task, fake := profileContextFixture(t, "", "")
	task.Inputs = map[string]any{"evidence": "BOUND_INPUT"}
	if err := runner.ExecuteTask(context.Background(), task, map[string]interface{}{"user_intent": "CURRENT_GOAL"}); err != nil {
		t.Fatal(err)
	}
	if fake.defaultResolves != 1 || fake.defaultCalls != 0 || fake.resolvedCalls != 1 || task.Metadata["model_profile_id"] != "model-small" {
		t.Fatalf("default model was not fixed before assembly: %+v metadata=%+v", fake, task.Metadata)
	}
	if !strings.Contains(fake.messages[1].Text, "BOUND_INPUT") || !strings.Contains(fake.messages[1].Text, "CURRENT_GOAL") {
		t.Fatal("untemplated task lost its inputs or goal")
	}
}

func TestProfileRunnerKeepsFullMaterialsForManagedRuntime(t *testing.T) {
	runner, task, fake := profileContextFixture(t, "model-small", "Report")
	task.Inputs = map[string]any{"log": strings.Repeat("log data ", 20000) + "FINAL_EVIDENCE"}
	if err := runner.ExecuteTask(context.Background(), task, map[string]interface{}{"context_managed": true}); err != nil {
		t.Fatal(err)
	}
	report := task.Metadata["prompt_budget"].(prompts.PromptReport)
	if !strings.Contains(fake.messages[1].Text, "FINAL_EVIDENCE") || len(report.TruncatedSections()) != 0 || report.TotalTokens <= report.Budget {
		t.Fatalf("managed runtime did not receive complete input: %+v", report)
	}
}

func TestProfileRunnerRejectsUnknownCapacityBeforeGeneration(t *testing.T) {
	runner, task, fake := profileContextFixture(t, "model-small", "Report")
	fake.call.Profile.ContextWindowTokens = 0
	if err := runner.ExecuteTask(context.Background(), task, nil); err == nil || !strings.Contains(err.Error(), "model_context_unknown") {
		t.Fatalf("unknown model capacity was not rejected: %v", err)
	}
	if task.Status != models.StatusFailed || fake.resolvedCalls != 0 || fake.defaultCalls != 0 {
		t.Fatal("invalid capacity reached generation")
	}
}
