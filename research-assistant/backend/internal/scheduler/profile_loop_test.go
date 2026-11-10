package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
	"research-assistant-backend/internal/tools"
)

func TestScheduledProfileApprovalPersistsAndResumesWithoutReplay(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	t.Setenv("CONTEXT_STORE_DIR", t.TempDir())
	previous := modelresolver.Default()
	modelresolver.SetDefault(nil)
	t.Cleanup(func() { modelresolver.SetDefault(previous) })
	var modelCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		n := modelCalls.Add(1)
		message := map[string]any{"content": "completed"}
		finish := "stop"
		if n == 1 {
			message = map[string]any{"content": "", "tool_calls": []any{map[string]any{"id": "write-call", "type": "function", "function": map[string]string{"name": "tool_" + contextwindow.Hash([]byte("native:write_file"))[:24], "arguments": `{"path":"approved.txt","content":"written"}`}}}}
			finish = "tool_calls"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 20, "completion_tokens": 10}})
	}))
	defer server.Close()
	profiles := agentprofile.NewRegistry(agentprofile.NewMemoryStore())
	if _, err := profiles.SaveProfile(models.AgentProfile{ID: "profile:loop", OwnerID: "owner", Name: "loop", Enabled: true, ExecutionMode: "tool_loop", MaxSteps: 3, MaxToolCalls: 2, ModelProfileID: "test-model", ToolAllowlist: []string{"native:write_file"}}); err != nil {
		t.Fatal(err)
	}
	registry := tools.NewRegistry(tools.NewMemoryStore())
	if err := registry.EnsureBuiltins(); err != nil {
		t.Fatal(err)
	}
	_, _ = registry.SaveGrant(models.ToolGrant{OwnerID: "owner", AgentProfileID: "profile:loop", ToolID: "native:write_file"})
	invoker := tools.NewInvoker(registry)
	invoker.WorkspaceRoot = t.TempDir()
	profileRunner := &agentprofile.ProfileRunner{Profiles: profiles, Completer: modelresolver.New(nil), Invoker: invoker, Runs: agentprofile.NewRunStore(t.TempDir())}
	executor := NewRoutedTaskExecutor(nil, nil, nil)
	executor.SetAgentRuntime(profiles, profileRunner, invoker)
	plans := store.NewMemoryPlanStore()
	plan := &models.PlanGraph{ID: "plan", OwnerID: "owner", ProjectID: "project", SessionID: "session", Status: models.StatusPending, CurrentRevision: 1, Artifacts: map[string]models.Artifact{}, Nodes: []*models.TaskNode{{ID: "task", Name: "write approved evidence", Type: "general", AssignedTo: "profile:loop", Status: models.StatusPending, Revision: 1}}}
	if err := plans.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	scheduler := NewScheduler(plans, executor, &NoopEventPublisher{}, 1)
	call := modelprofile.Resolved{Profile: modelprofile.Profile{ID: "test-model", OwnerID: "owner", BaseURL: server.URL, Model: "test", Revision: 1, ContextWindowTokens: 16000, MaxOutputTokens: 256, ContextPolicy: modelprofile.ContextPolicy{Capabilities: modelprofile.Capabilities{ToolCalling: "yes"}}}, APIKey: "test"}
	ctx := modelresolver.WithResolvedCall(context.Background(), call)
	result, err := scheduler.ExecuteTask(ctx, plan.ID, "task")
	if err != nil || result.Status != models.StatusAwaitingApproval {
		t.Fatalf("approval failed: %#v %v", result, err)
	}
	saved, _ := plans.GetPlan(plan.ID)
	if len(saved.RuntimeActions) != 1 || saved.Nodes[0].Status != models.StatusAwaitingApproval || saved.Nodes[0].Recovery.UsedActions != 0 {
		t.Fatalf("approval not persisted: %#v", saved)
	}
	if _, err := os.Stat(filepath.Join(invoker.WorkspaceRoot, "approved.txt")); !os.IsNotExist(err) {
		t.Fatal("side effect before approval")
	}
	_ = plans.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		now := time.Now()
		current.RuntimeActions[0].Status = models.RuntimeActionApproved
		current.RuntimeActions[0].DecidedAt = &now
		current.RuntimeActions[0].DecidedBy = "owner"
		current.Nodes[0].Status = models.StatusPending
		current.Status = models.StatusPending
		return nil
	})
	profileRunner.Runs = agentprofile.NewRunStore(profileRunner.Runs.Dir)
	result, err = scheduler.ExecuteTask(ctx, plan.ID, "task")
	if err != nil || result.Status != models.StatusCompleted {
		t.Fatalf("approval resume failed: %#v %v", result, err)
	}
	content, err := os.ReadFile(filepath.Join(invoker.WorkspaceRoot, "approved.txt"))
	if err != nil || string(content) != "written" {
		t.Fatal("approved tool did not execute")
	}
	saved, _ = plans.GetPlan(plan.ID)
	if modelCalls.Load() != 2 || len(saved.ToolCalls) != 1 || saved.Nodes[0].Recovery.UsedActions != 0 {
		t.Fatalf("replayed work or approval consumed recovery: models=%d tools=%d recovery=%+v", modelCalls.Load(), len(saved.ToolCalls), saved.Nodes[0].Recovery)
	}
}

func TestPlanClonePreservesExplicitEmptyToolContract(t *testing.T) {
	plans := store.NewMemoryPlanStore()
	plan := &models.PlanGraph{ID: "empty-tools", Nodes: []*models.TaskNode{{ID: "task", Contract: models.TaskContract{AllowedTools: []string{}}}}}
	if err := plans.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	saved, _ := plans.GetPlan(plan.ID)
	if saved.Nodes[0].Contract.AllowedTools == nil {
		t.Fatal("plan JSON clone broadened explicit empty tool permission")
	}
}
