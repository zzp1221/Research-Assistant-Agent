package scheduler

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
	"research-assistant-backend/internal/tools"
)

// approvalInvoker builds a tool registry with one always-approval tool and the
// invoker the routed executor uses.
func approvalInvoker(t *testing.T) (*tools.Registry, *tools.Invoker) {
	t.Helper()
	registry := tools.NewRegistry(tools.NewMemoryStore())
	definition := models.ToolDefinition{
		ID:           "native:demo_echo",
		Name:         "native:demo_echo",
		Version:      "1.0.0",
		Enabled:      true,
		Transport:    models.ToolTransportNative,
		Native:       "demo_echo",
		Approval:     models.ToolApprovalAlways,
		InputSchema:  `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`,
		OutputSchema: `{"type":"object"}`,
		Scopes:       []string{"fs.write"},
		TimeoutSec:   5,
	}
	if err := registry.SaveTool(definition); err != nil {
		t.Fatal(err)
	}
	invoker := tools.NewInvoker(registry)
	invoker.Native["demo_echo"] = func(_ context.Context, request tools.NativeRequest) (*tools.NativeResult, error) {
		return &tools.NativeResult{Text: "echoed"}, nil
	}
	return registry, invoker
}

func TestToolApprovalRequestPausesInsteadOfFailing(t *testing.T) {
	_, invoker := approvalInvoker(t)
	executor := NewRoutedTaskExecutor(nil, nil, nil)
	executor.SetAgentRuntime(nil, nil, invoker)

	now := time.Now()
	plan := &models.PlanGraph{
		ID: "tool-approval", OwnerID: "owner", Status: models.StatusInProgress,
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{{
			ID: "tool", Name: "tool", Type: "tool_call", AssignedTo: "general_agent",
			Status: models.StatusInProgress, CreatedAt: now, UpdatedAt: now,
			Inputs: map[string]any{"tool_id": "native:demo_echo", "arguments": map[string]any{"text": "hi"}},
			Contract: models.TaskContract{
				Version:      models.TaskContractVersion,
				AllowedTools: []string{"native:demo_echo"},
			},
		}},
	}
	result, err := executor.ExecuteTask(context.Background(), plan, plan.Nodes[0])
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != models.StatusAwaitingApproval {
		t.Fatalf("status=%s error=%q, want awaiting_approval", result.Status, result.Error)
	}
	if result.RuntimeAction == nil || result.RuntimeAction.Kind != models.RuntimeActionToolCall || result.RuntimeAction.Status != models.RuntimeActionPending {
		t.Fatalf("runtime action=%#v", result.RuntimeAction)
	}
	if result.Error != "" {
		t.Fatalf("an approval request produced an error: %q", result.Error)
	}

	// The scheduler parks the node without a failure, a retry, or a blocked
	// dependent; approving it afterwards runs the tool exactly once.
	planStore := store.NewMemoryPlanStore()
	plan.Nodes[0].Status = models.StatusReady
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, executor, &NoopEventPublisher{}, 1)
	executionID, owner, epoch, err := s.markTaskStarted(plan.ID, "tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, waiting, err := s.applyTaskResult(plan.ID, "tool", executionID, owner, epoch, result, nil, true); err != nil || !waiting {
		t.Fatalf("waiting=%v err=%v", waiting, err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	node := getNodeByID(stored, "tool")
	if node.Status != models.StatusAwaitingApproval || node.Failure != nil || node.FinishedAt != nil {
		t.Fatalf("node=%#v", node)
	}
	if len(stored.RuntimeActions) != 1 || stored.RuntimeActions[0].Status != models.RuntimeActionPending {
		t.Fatalf("runtime actions=%#v", stored.RuntimeActions)
	}
	if stored.Outcome != "" {
		t.Fatalf("waiting plan has an outcome: %q", stored.Outcome)
	}

	// Approve, re-dispatch and confirm the tool really ran.
	if err := planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.RuntimeActions[0].Status = models.RuntimeActionApproved
		current.RuntimeActions[0].DecidedBy = "tester"
		task := getNodeByID(current, "tool")
		task.Status = models.StatusReady
		current.Status = models.StatusInProgress
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	executionID, owner, epoch, err = s.markTaskStarted(plan.ID, "tool")
	if err != nil {
		t.Fatal(err)
	}
	approved := &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "echoed"}
	current, _ := planStore.GetPlan(plan.ID)
	approved, execErr := executor.ExecuteTask(context.Background(), current, getNodeByID(current, "tool"))
	if execErr != nil {
		t.Fatal(execErr)
	}
	if approved.Status != models.StatusCompleted {
		t.Fatalf("approved tool did not run: status=%s error=%q", approved.Status, approved.Error)
	}
	if _, _, err := s.applyTaskResult(plan.ID, "tool", executionID, owner, epoch, approved, nil, true); err != nil {
		t.Fatal(err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if getNodeByID(final, "tool").Status != models.StatusCompleted {
		t.Fatalf("task status=%s", getNodeByID(final, "tool").Status)
	}
}
