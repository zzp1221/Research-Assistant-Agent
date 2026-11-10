package tools

import (
	"context"
	"os"
	"path/filepath"
	"research-assistant-backend/internal/models"
	"testing"
	"time"
)

func testApprovalContext(owner, id string, args map[string]any) context.Context {
	return WithApprovals(context.Background(), owner, "", "", []models.RuntimeActionRequest{{ID: "ra-1", Kind: models.RuntimeActionToolCall, Status: models.RuntimeActionApproved, Details: map[string]any{"tool_id": id, "tool_version": "1.0.0", "call_id": "", "arguments_hash": models.HashArguments(args), "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}}})
}
func TestForgedApprovalHasNoSideEffect(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TOOL_WORKSPACE_ROOT", root)
	invoker := NewInvoker(newTestRegistry(t))
	args := map[string]any{"path": "forged.txt", "content": "secret"}
	result := invoker.Invoke(context.Background(), InvokeRequest{OwnerID: "owner-a", ToolID: "native:write_file", Arguments: args, ApprovalID: "ra-1"})
	if result.ErrorCode != "tool_approval_required" {
		t.Fatal(result)
	}
	if _, err := os.Stat(filepath.Join(root, "forged.txt")); !os.IsNotExist(err) {
		t.Fatal("forged approval wrote file")
	}
	changed := map[string]any{"path": "forged.txt", "content": "changed"}
	result = invoker.Invoke(testApprovalContext("owner-a", "native:write_file", args), InvokeRequest{OwnerID: "owner-a", ToolID: "native:write_file", Arguments: changed, ApprovalID: "ra-1"})
	if result.ErrorCode != "tool_approval_required" {
		t.Fatal("approval accepted changed arguments")
	}
}
