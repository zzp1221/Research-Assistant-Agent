package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

// NativeRequest is one invocation of a native Go tool.
type NativeRequest struct {
	Tool          models.ToolDefinition
	Arguments     map[string]any
	DryRun        bool
	WorkspaceRoot string
}

// NativeResult is the normalized output of a native tool.
type NativeResult struct {
	Text      string
	JSON      map[string]any
	Truncated bool
	Bytes     int64
}

// NativeError carries a stable error code back to the invoker.
type NativeError struct {
	Code    string
	Message string
}

func (e *NativeError) Error() string { return e.Message }

// NativeFunc implements one native tool.
type NativeFunc func(ctx context.Context, req NativeRequest) (*NativeResult, error)

// NativeFunctions returns the built-in native implementations. The map is
// intentionally small and code-owned: user code cannot be uploaded as a tool.
func NativeFunctions() map[string]NativeFunc {
	return map[string]NativeFunc{
		"echo":       nativeEcho,
		"write_file": nativeWriteFile,
		"fail":       nativeFail,
		"sleep":      nativeSleep,
		"big_output": nativeBigOutput,
	}
}

func nativeEcho(_ context.Context, req NativeRequest) (*NativeResult, error) {
	message, _ := req.Arguments["message"].(string)
	return &NativeResult{Text: message, JSON: map[string]any{"message": message}, Bytes: int64(len(message))}, nil
}

func nativeWriteFile(_ context.Context, req NativeRequest) (*NativeResult, error) {
	path, _ := req.Arguments["path"].(string)
	content, _ := req.Arguments["content"].(string)
	root := strings.TrimSpace(req.WorkspaceRoot)
	if root == "" {
		root = defaultToolWorkspaceRoot()
	}
	cleaned := filepath.Clean(strings.TrimSpace(path))
	if cleaned == "." || cleaned == string(filepath.Separator) || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, "..") {
		return nil, &NativeError{Code: "tool_invalid_arguments", Message: "path must be a relative path inside the tool workspace"}
	}
	target := filepath.Join(root, cleaned)
	relative, err := filepath.Rel(root, target)
	if err != nil || strings.HasPrefix(relative, "..") {
		return nil, &NativeError{Code: "tool_invalid_arguments", Message: "path escapes the tool workspace"}
	}
	if req.DryRun {
		return &NativeResult{
			Text:  fmt.Sprintf("dry-run: would write %d bytes to %s", len(content), cleaned),
			JSON:  map[string]any{"path": cleaned, "bytes": len(content), "dry_run": true},
			Bytes: int64(len(content)),
		}, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, &NativeError{Code: "tool_execution_failed", Message: "create directory: " + err.Error()}
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		return nil, &NativeError{Code: "tool_execution_failed", Message: "write file: " + err.Error()}
	}
	return &NativeResult{
		Text:  fmt.Sprintf("wrote %d bytes to %s", len(content), cleaned),
		JSON:  map[string]any{"path": cleaned, "bytes": len(content)},
		Bytes: int64(len(content)),
	}, nil
}

func nativeFail(_ context.Context, req NativeRequest) (*NativeResult, error) {
	message, _ := req.Arguments["message"].(string)
	if strings.TrimSpace(message) == "" {
		message = "native:fail was invoked"
	}
	return nil, &NativeError{Code: "tool_execution_failed", Message: message}
}

func nativeSleep(ctx context.Context, req NativeRequest) (*NativeResult, error) {
	ms := intArgument(req.Arguments, "ms", 0)
	if ms <= 0 {
		ms = 1000
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(time.Duration(ms) * time.Millisecond):
	}
	return &NativeResult{Text: fmt.Sprintf("slept %dms", ms), JSON: map[string]any{"ms": ms}}, nil
}

func nativeBigOutput(_ context.Context, req NativeRequest) (*NativeResult, error) {
	n := intArgument(req.Arguments, "bytes", 0)
	if n <= 0 {
		n = 1024
	}
	if n > 10<<20 {
		n = 10 << 20
	}
	text := strings.Repeat("x", n)
	return &NativeResult{Text: text, JSON: map[string]any{"bytes": n}, Bytes: int64(n)}, nil
}

func intArgument(arguments map[string]any, key string, fallback int) int {
	raw, ok := arguments[key]
	if !ok || raw == nil {
		return fallback
	}
	switch typed := raw.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return fallback
	}
}

// defaultToolWorkspaceRoot is a dedicated directory so native:write_file can
// never touch the repository or a user's home directory.
func defaultToolWorkspaceRoot() string {
	if configured := strings.TrimSpace(os.Getenv("TOOL_WORKSPACE_ROOT")); configured != "" {
		return configured
	}
	return filepath.Join(os.TempDir(), "research-assistant-tool-workspace")
}
