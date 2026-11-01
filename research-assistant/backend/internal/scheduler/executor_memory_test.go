package scheduler

import (
	"context"
	"strings"
	"testing"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

type memoryCaptureRunner struct {
	shared map[string]interface{}
}

func (r *memoryCaptureRunner) ExecuteTask(_ context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	r.shared = sharedContext
	task.Status = models.StatusCompleted
	task.Result = "ok"
	return nil
}

func TestRoutedTaskExecutorInjectsCoreMemory(t *testing.T) {
	store := memory.NewMemoryStore()
	if _, err := store.Put(context.Background(), memory.Record{
		Kind: memory.KindProfile, Scope: memory.Scope{OwnerID: "u1"},
		Title: "用户偏好", Content: "用户喜欢蓝色", Pinned: true, Importance: 10,
		Trust: memory.TrustUserExplicit, Status: memory.StatusActive, Source: memory.Source{Type: "user"},
	}); err != nil {
		t.Fatal(err)
	}
	runner := &memoryCaptureRunner{}
	executor := NewRoutedTaskExecutor(runner, runner, runner)
	executor.General = runner
	executor.SetMemory(store)
	plan := &models.PlanGraph{ID: "p-1", OwnerID: "u1", SessionID: "s-1", UserIntent: "test"}
	task := &models.TaskNode{ID: "t-1", Name: "task", Type: "general", AssignedTo: "general_agent", Description: "do it", Status: models.StatusPending}
	result, err := executor.ExecuteTask(context.Background(), plan, task)
	if err != nil || result == nil || result.Status != models.StatusCompleted {
		t.Fatalf("execute failed: %v %#v", err, result)
	}
	core, _ := runner.shared["core_memory"].(string)
	if !strings.Contains(core, "用户喜欢蓝色") {
		t.Fatalf("core memory not injected: %q", core)
	}
	refs, _ := runner.shared["memory_refs"].([]string)
	if len(refs) == 0 {
		t.Fatal("expected memory refs")
	}
}
