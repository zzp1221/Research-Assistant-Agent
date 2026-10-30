package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

func newTestRegistry(t *testing.T) *Registry {
	t.Helper()
	registry := NewRegistry(NewMemoryStore())
	registry.SetClock(func() time.Time { return time.Unix(1700000000, 0).UTC() })
	if err := registry.EnsureBuiltins(); err != nil {
		t.Fatalf("seed builtins: %v", err)
	}
	return registry
}

func TestValidateArguments(t *testing.T) {
	schema := `{"type":"object","properties":{"message":{"type":"string","minLength":1},"count":{"type":"integer","minimum":1}},"required":["message"],"additionalProperties":false}`
	if errs := ValidateArguments(schema, map[string]any{"message": "hi", "count": float64(2)}); len(errs) != 0 {
		t.Fatalf("expected valid, got %v", errs)
	}
	if errs := ValidateArguments(schema, map[string]any{"count": float64(2)}); len(errs) == 0 || !strings.Contains(errs[0].Error(), "required") {
		t.Fatalf("expected required error, got %v", errs)
	}
	if errs := ValidateArguments(schema, map[string]any{"message": "hi", "count": float64(0)}); len(errs) == 0 || !strings.Contains(errs[0].Error(), ">=") {
		t.Fatalf("expected minimum error, got %v", errs)
	}
	if errs := ValidateArguments(schema, map[string]any{"message": "hi", "extra": true}); len(errs) == 0 || !strings.Contains(errs[0].Error(), "allowed property") {
		t.Fatalf("expected additionalProperties error, got %v", errs)
	}
	if errs := ValidateArguments(schema, map[string]any{"message": ""}); len(errs) == 0 || !strings.Contains(errs[0].Error(), "at least") {
		t.Fatalf("expected minLength error, got %v", errs)
	}
}

func TestRegistryVisibilityAndBuiltins(t *testing.T) {
	registry := newTestRegistry(t)
	defs, err := registry.ListTools("owner-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(defs) < 5 {
		t.Fatalf("expected builtin tools, got %d", len(defs))
	}
	userTool := models.ToolDefinition{
		ID: "native:custom", OwnerID: "owner-a", Name: "Custom", Version: "1.0.0",
		Transport: models.ToolTransportNative, Native: "echo", Enabled: true,
		InputSchema: `{"type":"object"}`,
	}
	if err := registry.SaveTool(userTool); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.GetToolForOwner("owner-b", "native:custom"); err != ErrForbidden {
		t.Fatalf("expected forbidden for other owner, got %v", err)
	}
	if _, err := registry.GetToolForOwner("owner-a", "native:custom"); err != nil {
		t.Fatalf("expected owner access, got %v", err)
	}
	if err := registry.SaveTool(userTool); err == nil {
		t.Fatal("expected duplicate conflict")
	}
}

func TestInvokerEcho(t *testing.T) {
	registry := newTestRegistry(t)
	invoker := NewInvoker(registry)
	result := invoker.Invoke(context.Background(), InvokeRequest{
		PlanID: "plan-1", TaskID: "task-1", ToolID: "native:echo",
		Arguments: map[string]any{"message": "hello"}, OwnerID: "owner-a",
	})
	if result.Record.Status != models.ToolCallCompleted {
		t.Fatalf("expected completed, got %s (%s)", result.Record.Status, result.Record.Error)
	}
	if result.Text != "hello" {
		t.Fatalf("expected hello, got %q", result.Text)
	}
	if result.Record.ResultHash == "" || result.Record.ArgumentsHash == "" || result.Record.DurationMS < 0 {
		t.Fatalf("record missing hashes/duration: %+v", result.Record)
	}
	if len(result.Artifacts) == 0 || result.Artifacts[0].Key != "tool_result" {
		t.Fatalf("expected tool_result artifact, got %+v", result.Artifacts)
	}
}

func TestInvokerAllowlistForbiddenHasNoSideEffect(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOOL_WORKSPACE_ROOT", root)
	registry := newTestRegistry(t)
	invoker := NewInvoker(registry)
	result := invoker.Invoke(context.Background(), InvokeRequest{
		PlanID: "plan-1", TaskID: "task-1", ToolID: "native:write_file",
		Arguments:    map[string]any{"path": "secret.txt", "content": "should not exist"},
		AllowedTools: []string{"native:echo"},
		OwnerID:      "owner-a",
	})
	if result.ErrorCode != "tool_forbidden" {
		t.Fatalf("expected tool_forbidden, got %q", result.ErrorCode)
	}
	if _, err := os.Stat(filepath.Join(root, "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("forbidden call must not create a file, stat err=%v", err)
	}
}

func TestInvokerSchemaAndApproval(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOOL_WORKSPACE_ROOT", root)
	registry := newTestRegistry(t)
	invoker := NewInvoker(registry)
	invalid := invoker.Invoke(context.Background(), InvokeRequest{
		ToolID: "native:echo", Arguments: map[string]any{}, OwnerID: "owner-a",
	})
	if invalid.ErrorCode != "tool_invalid_arguments" {
		t.Fatalf("expected invalid arguments, got %q", invalid.ErrorCode)
	}
	pending := invoker.Invoke(context.Background(), InvokeRequest{
		ToolID: "native:write_file", Arguments: map[string]any{"path": "a.txt", "content": "x"}, OwnerID: "owner-a",
	})
	if pending.Record.Status != models.ToolCallPending || pending.ErrorCode != "tool_approval_required" {
		t.Fatalf("expected pending approval, got %s / %s", pending.Record.Status, pending.ErrorCode)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("unapproved call must not write")
	}
	approved := invoker.Invoke(context.Background(), InvokeRequest{
		ToolID: "native:write_file", Arguments: map[string]any{"path": "a.txt", "content": "x"},
		OwnerID: "owner-a", ApprovalID: "ra-1",
	})
	if approved.Record.Status != models.ToolCallCompleted || approved.Record.ApprovalID != "ra-1" {
		t.Fatalf("expected approved completion, got %+v", approved.Record)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Fatalf("approved call should write: %v", err)
	}
}

func TestInvokerGrantRequiredForProfile(t *testing.T) {
	registry := newTestRegistry(t)
	invoker := NewInvoker(registry)
	denied := invoker.Invoke(context.Background(), InvokeRequest{
		ToolID: "native:echo", Arguments: map[string]any{"message": "hi"},
		OwnerID: "owner-a", AgentProfileID: "profile:demo",
	})
	if denied.ErrorCode != "tool_forbidden" {
		t.Fatalf("expected tool_forbidden without grant, got %q", denied.ErrorCode)
	}
	if _, err := registry.SaveGrant(models.ToolGrant{OwnerID: "owner-a", AgentProfileID: "profile:demo", ToolID: "native:echo"}); err != nil {
		t.Fatal(err)
	}
	allowed := invoker.Invoke(context.Background(), InvokeRequest{
		ToolID: "native:echo", Arguments: map[string]any{"message": "hi"},
		OwnerID: "owner-a", AgentProfileID: "profile:demo",
	})
	if allowed.Record.Status != models.ToolCallCompleted {
		t.Fatalf("expected completed after grant, got %+v", allowed.Record)
	}
}

func TestInvokerBigOutputTruncation(t *testing.T) {
	registry := newTestRegistry(t)
	invoker := NewInvoker(registry)
	result := invoker.Invoke(context.Background(), InvokeRequest{
		ToolID: "native:big_output", Arguments: map[string]any{"bytes": float64(4096)}, OwnerID: "owner-a",
	})
	if !result.Record.Truncated {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(result.Text, "[truncated]") {
		t.Fatal("expected truncation marker")
	}
}

func TestInvokerFail(t *testing.T) {
	registry := newTestRegistry(t)
	invoker := NewInvoker(registry)
	result := invoker.Invoke(context.Background(), InvokeRequest{
		ToolID: "native:fail", Arguments: map[string]any{"message": "boom"}, OwnerID: "owner-a",
	})
	if result.ErrorCode != "tool_execution_failed" || result.Record.Status != models.ToolCallFailed {
		t.Fatalf("expected failure, got %+v", result.Record)
	}
}

func TestNativeWriteFilePathTraversal(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOOL_WORKSPACE_ROOT", root)
	registry := newTestRegistry(t)
	invoker := NewInvoker(registry)
	result := invoker.Invoke(context.Background(), InvokeRequest{
		ToolID: "native:write_file", Arguments: map[string]any{"path": "../escape.txt", "content": "x"},
		OwnerID: "owner-a", ApprovalID: "ra-1",
	})
	if result.ErrorCode != "tool_invalid_arguments" {
		t.Fatalf("expected invalid arguments, got %q", result.ErrorCode)
	}
}
