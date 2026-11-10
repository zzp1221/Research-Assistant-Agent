package scheduler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/store"
)

type contextRunnerFunc func(context.Context, *models.Task, map[string]interface{}) error

func (f contextRunnerFunc) ExecuteTask(ctx context.Context, task *models.Task, shared map[string]interface{}) error {
	return f(ctx, task, shared)
}

func TestRoutedContextIsIsolatedByRevisionAndTask(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	t.Setenv("MODEL_PROFILE_ENCRYPTION_KEY", "dGVzdC1rZXktMzItYnl0ZXMtZm9yLWFlcy0yNTYtZ2NtISE=")
	t.Setenv("CONTEXT_STORE_DIR", t.TempDir())
	t.Setenv("PROMPT_CONTEXT_V2", "false")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"choices":[{"message":{"content":"done"}}]}`)
	}))
	defer server.Close()
	profiles, err := modelprofile.NewStore("")
	if err != nil {
		t.Fatal(err)
	}
	_, err = profiles.Create("owner", modelprofile.Input{
		Name: "main", BaseURL: server.URL, Model: "context-model", APIKey: "test-key",
		Roles: []string{modelprofile.RoleMain}, ContextWindowTokens: 4000, MaxOutputTokens: 128,
	}, 5)
	if err != nil {
		t.Fatal(err)
	}
	resolver := modelresolver.New(profiles)
	previous := modelresolver.Default()
	modelresolver.SetDefault(resolver)
	t.Cleanup(func() { modelresolver.SetDefault(previous) })
	runner := contextRunnerFunc(func(ctx context.Context, task *models.Task, shared map[string]interface{}) error {
		if shared["plan_graph"] == nil || shared["context_managed"] != true {
			t.Fatal("managed task lost plan context when legacy prompt flag is off")
		}
		response, _, err := resolver.Complete(ctx, "owner", []oaichat.Message{{Role: "user", Text: "identical task input"}}, oaichat.Options{})
		if err != nil {
			return err
		}
		task.Result = response.Content
		task.Status = models.StatusCompleted
		return nil
	})
	executor := NewRoutedTaskExecutor(runner, runner, runner)
	executor.General = runner
	plan := &models.PlanGraph{ID: "p", OwnerID: "owner", SessionID: "s", CurrentRevision: 1, UserIntent: "frozen goal"}
	task := &models.TaskNode{ID: "t", AssignedTo: "general_agent", Type: "general", Description: "work"}
	run := func() contextwindow.Report {
		result, err := executor.ExecuteTask(context.Background(), plan, task)
		if err != nil || result.Status != models.StatusCompleted {
			t.Fatalf("execute: %#v %v", result, err)
		}
		report, ok := result.Metadata["context_state"].(contextwindow.Report)
		if !ok || report.StateRef == "" || report.Generation == 0 {
			t.Fatalf("missing committed context diagnostics: %#v", result.Metadata)
		}
		return report
	}
	first := run()
	task.ExecutionID, task.ExecutionEpoch = "retry", 2
	retry := run()
	if calls != 1 || first.StateRef != retry.StateRef {
		t.Fatalf("retry lost logical window: requests=%d", calls)
	}
	plan.CurrentRevision = 2
	revised := run()
	if calls != 2 || first.StateRef == revised.StateRef {
		t.Fatal("new revision reused old task window")
	}
	task.ID = "parallel"
	parallel := run()
	if calls != 3 || parallel.StateRef == revised.StateRef {
		t.Fatal("parallel task reused sibling window")
	}
}

func TestRoutedFailurePreservesContextDiagnostics(t *testing.T) {
	runner := contextRunnerFunc(func(ctx context.Context, task *models.Task, _ map[string]interface{}) error {
		if task.ExecutionID != "exec" || task.ExecutionEpoch != 3 {
			t.Fatal("execution identity was dropped")
		}
		vars := task.Metadata["prompt_variables"].(map[string]any)
		vars["word"] = "changed locally"
		task.Metadata["repair_session"].(map[string]any)["session_id"] = "next"
		report := contextwindow.ReportFrom(ctx)
		*report = contextwindow.Report{ProfileID: "model", Generation: 2, StateRef: "saved-window"}
		task.Status = models.StatusFailed
		return errors.New("generation interrupted")
	})
	executor := NewRoutedTaskExecutor(runner, runner, runner)
	executor.General = runner
	task := &models.TaskNode{ID: "t", Type: "general", AssignedTo: "general_agent", ExecutionID: "exec", ExecutionEpoch: 3,
		Metadata: map[string]any{"prompt_variables": map[string]string{"word": "original"}, "repair_session": map[string]any{"session_id": "previous"}},
	}
	result, err := executor.ExecuteTask(context.Background(), &models.PlanGraph{ID: "p"}, task)
	if err != nil || result.Status != models.StatusFailed || !strings.Contains(result.Error, "interrupted") {
		t.Fatalf("expected failed model result: %#v %v", result, err)
	}
	if result.Metadata["context_state"].(contextwindow.Report).StateRef != "saved-window" || result.Metadata["repair_session"].(map[string]any)["session_id"] != "next" {
		t.Fatal("failure discarded recoverable context diagnostics")
	}
	if task.Metadata["prompt_variables"].(map[string]string)["word"] != "original" || task.Metadata["repair_session"].(map[string]any)["session_id"] != "previous" {
		t.Fatal("worker mutated persisted task metadata before fenced commit")
	}
}

func TestFailedContextMetadataRespectsExecutionFence(t *testing.T) {
	plans := store.NewMemoryPlanStore()
	plan := governanceTestPlan("context-failure", 1)
	plan.Nodes[0].RetryLimit = 2
	if err := plans.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(plans, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	execution, owner, epoch, err := scheduler.markTaskStarted(plan.ID, plan.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	failure := &models.TaskExecutionResult{Status: models.StatusFailed, Error: "HTTP 503 interrupted", Metadata: map[string]any{"repair_session": "committed checkpoint", "context_state": "window-2"}}
	retrying, err := scheduler.markTaskFailed(plan.ID, plan.Nodes[0].ID, execution, owner, epoch, failure, true)
	if err != nil || !retrying {
		t.Fatalf("retry: %v %v", retrying, err)
	}
	execution, owner, epoch, err = scheduler.markTaskStarted(plan.ID, plan.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = scheduler.markTaskFailed(plan.ID, plan.Nodes[0].ID, execution, owner, epoch-1,
		&models.TaskExecutionResult{Metadata: map[string]any{"repair_session": "stale checkpoint"}}, true)
	if !errors.Is(err, ErrStaleExecution) {
		t.Fatalf("stale worker accepted: %v", err)
	}
	saved, _ := plans.GetPlan(plan.ID)
	if saved.Nodes[0].Metadata["repair_session"] != "committed checkpoint" || saved.Nodes[0].Metadata["context_state"] != "window-2" {
		t.Fatal("checkpoint was discarded on failure or replaced by a stale worker")
	}
}
