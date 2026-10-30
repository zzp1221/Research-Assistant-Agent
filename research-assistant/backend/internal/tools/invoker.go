package tools

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/google/uuid"
)

// InvokeRequest describes one tool invocation.
type InvokeRequest struct {
	PlanID         string
	TaskID         string
	ToolID         string
	ToolVersion    string
	Arguments      map[string]any
	AllowedTools   []string
	AgentProfileID string
	OwnerID        string
	ApprovalID     string
	DryRun         bool
	CallID         string
	ReplayOf       string
}

// InvokeResult is the normalized outcome of a tool call.
type InvokeResult struct {
	Record    models.ToolCallRecord
	Text      string
	JSON      map[string]any
	Artifacts []models.Artifact
	ErrorCode string
	Err       error
}

// Invoker executes registered tools with schema, permission and audit checks.
type Invoker struct {
	Registry      *Registry
	Native        map[string]NativeFunc
	HTTP          *HTTPInvoker
	MCP           *MCPClient
	Now           func() time.Time
	WorkspaceRoot string
}

func NewInvoker(registry *Registry) *Invoker {
	return &Invoker{
		Registry: registry,
		Native:   NativeFunctions(),
		HTTP:     &HTTPInvoker{},
		MCP:      &MCPClient{Registry: registry},
		Now:      time.Now,
	}
}

// Invoke performs the full tool pipeline. It never panics and always returns a
// record, so callers can persist a truthful audit trail even on rejection.
func (i *Invoker) Invoke(ctx context.Context, request InvokeRequest) *InvokeResult {
	start := i.Now()
	record := models.ToolCallRecord{
		ID:            firstNonEmpty(request.CallID, "tc-"+uuid.NewString()),
		PlanID:        request.PlanID,
		TaskID:        request.TaskID,
		ToolID:        request.ToolID,
		ToolVersion:   request.ToolVersion,
		Arguments:      cloneArguments(request.Arguments),
		ArgumentsHash:  models.HashArguments(request.Arguments),
		AgentProfileID: request.AgentProfileID,
		Status:        models.ToolCallRunning,
		DryRun:        request.DryRun,
		ReplayOf:      request.ReplayOf,
		StartedAt:     start,
	}
	finish := func(result *InvokeResult, code, message string) *InvokeResult {
		now := i.Now()
		record.FinishedAt = &now
		record.DurationMS = now.Sub(start).Milliseconds()
		if code != "" {
			record.Status = models.ToolCallFailed
			record.ErrorCode = code
			record.Error = message
		}
		result.Record = record
		result.ErrorCode = code
		if code != "" {
			result.Err = &ToolCallError{Code: code, Message: message}
		}
		return result
	}

	tool, err := i.Registry.GetToolForOwner(request.OwnerID, request.ToolID)
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return finish(&InvokeResult{}, "tool_not_found", "tool is not registered: "+request.ToolID)
		case errors.Is(err, ErrForbidden):
			return finish(&InvokeResult{}, "tool_forbidden", "tool is not visible to this owner")
		default:
			return finish(&InvokeResult{}, "tool_unavailable", err.Error())
		}
	}
	record.ToolVersion = tool.Version
	if request.ToolVersion != "" && request.ToolVersion != tool.Version {
		return finish(&InvokeResult{}, "tool_version_mismatch", fmt.Sprintf("tool %s has version %s, requested %s", tool.ID, tool.Version, request.ToolVersion))
	}
	if !tool.Enabled {
		return finish(&InvokeResult{}, "tool_disabled", "tool is disabled")
	}
	// AllowedTools is the planner-visible secondary constraint. An empty list
	// means the node did not declare one (system tool_executor path); an
	// explicit list must contain the tool.
	if request.AllowedTools != nil && !containsString(request.AllowedTools, tool.ID) {
		return finish(&InvokeResult{}, "tool_forbidden", "tool "+tool.ID+" is not in the node's allowed_tools")
	}
	var grant *models.ToolGrant
	if request.AgentProfileID != "" {
		grant, err = i.Registry.FindGrant(request.OwnerID, request.AgentProfileID, tool.ID)
		if err != nil {
			return finish(&InvokeResult{}, "tool_unavailable", err.Error())
		}
		if grant == nil {
			return finish(&InvokeResult{}, "tool_forbidden", "agent profile "+request.AgentProfileID+" has no grant for "+tool.ID)
		}
		if len(grant.Scopes) > 0 {
			for _, scope := range tool.Scopes {
				if !containsString(grant.Scopes, scope) {
					return finish(&InvokeResult{}, "tool_forbidden", "grant does not cover scope "+scope)
				}
			}
		}
	}
	if errors := ValidateArguments(tool.InputSchema, request.Arguments); len(errors) > 0 {
		return finish(&InvokeResult{}, "tool_invalid_arguments", errors[0].Error())
	}
	approval := tool.EffectiveApproval(grant)
	if tool.NeedsApproval() || approval == models.ToolApprovalAlways {
		if strings.TrimSpace(request.ApprovalID) == "" {
			record.Status = models.ToolCallPending
			now := i.Now()
			record.FinishedAt = &now
			record.DurationMS = 0
			return &InvokeResult{Record: record, ErrorCode: "tool_approval_required", Err: &ToolCallError{Code: "tool_approval_required", Message: "tool requires approval"}}
		}
		record.ApprovalID = request.ApprovalID
		record.Status = models.ToolCallApproved
	}
	record.Status = models.ToolCallRunning

	timeout := time.Duration(tool.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var result *NativeResult
	switch tool.Transport {
	case models.ToolTransportNative:
		fn, ok := i.Native[tool.Native]
		if !ok {
			return finish(&InvokeResult{}, "tool_unavailable", "native implementation is not available: "+tool.Native)
		}
		result, err = fn(execCtx, NativeRequest{
			Tool:          tool,
			Arguments:     request.Arguments,
			DryRun:        request.DryRun,
			WorkspaceRoot: i.WorkspaceRoot,
		})
	case models.ToolTransportHTTP:
		result, err = i.HTTP.Invoke(execCtx, tool, request.Arguments, request.DryRun)
	case models.ToolTransportMCPStdio, models.ToolTransportMCPHTTP:
		result, err = i.MCP.Invoke(execCtx, tool, request.Arguments)
	default:
		return finish(&InvokeResult{}, "tool_unavailable", "unsupported transport "+tool.Transport)
	}
	if err != nil {
		code := "tool_execution_failed"
		var nativeErr *NativeError
		if errors.As(err, &nativeErr) {
			code = nativeErr.Code
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			code = "tool_timeout"
			err = fmt.Errorf("tool timed out after %s", timeout)
		}
		if errors.Is(err, context.Canceled) {
			code = "tool_canceled"
		}
		return finish(&InvokeResult{}, code, err.Error())
	}
	if result == nil {
		return finish(&InvokeResult{}, "tool_execution_failed", "tool returned no result")
	}
	text := result.Text
	maxBytes := tool.MaxOutputBytes
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	truncated := result.Truncated
	if int64(len(text)) > maxBytes {
		text = text[:maxBytes] + "\n...[truncated]"
		truncated = true
	}
	record.Truncated = truncated
	record.OutputBytes = int64(len(text))
	record.Status = models.ToolCallCompleted
	record.ResultPreview = preview(text, 512)
	record.ResultHash = models.HashResult(map[string]any{"text": text, "json": result.JSON})
	out := &InvokeResult{Record: record, Text: text, JSON: result.JSON}
	if tool.ID != "" {
		artifact := models.Artifact{
			Key:            "tool_result",
			Type:           "text",
			Kind:           "tool_result",
			ProducerTaskID: request.TaskID,
			Value:          text,
			Metadata: map[string]any{
				"tool_id":      tool.ID,
				"tool_version": tool.Version,
				"truncated":    truncated,
				// Tool output is untrusted data. Consumers must never treat it as
				// instructions (prompt-injection boundary, design 5.4).
				"untrusted": true,
			},
			CreatedAt: i.Now(),
		}
		out.Artifacts = append(out.Artifacts, artifact)
		if encoded, ok := result.JSON["image_base64"].(string); ok && encoded != "" {
			if _, decodeErr := base64.StdEncoding.DecodeString(encoded); decodeErr == nil {
				out.Artifacts = append(out.Artifacts, models.Artifact{
					Key:            "tool_result_image",
					Type:           "image_base64",
					Kind:           "plot",
					ProducerTaskID: request.TaskID,
					Value:          encoded,
					Metadata:       map[string]any{"tool_id": tool.ID},
					CreatedAt:      i.Now(),
				})
			}
		}
	}
	now := i.Now()
	record.FinishedAt = &now
	record.DurationMS = now.Sub(start).Milliseconds()
	out.Record = record
	return out
}

// ToolCallError is the typed error returned to callers.
type ToolCallError struct {
	Code    string
	Message string
}

func (e *ToolCallError) Error() string { return e.Code + ": " + e.Message }

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func cloneArguments(arguments map[string]any) map[string]any {
	if arguments == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(arguments))
	for key, value := range arguments {
		out[key] = value
	}
	return out
}

func preview(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// PreviewArguments returns a redacted, bounded summary safe for approval cards
// and audit events. Secret-looking keys never leak their values.
func PreviewArguments(arguments map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range arguments {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "key") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "authorization") {
			out[key] = "[redacted]"
			continue
		}
		if text, ok := value.(string); ok && len(text) > 200 {
			out[key] = text[:200] + "...[truncated]"
			continue
		}
		out[key] = value
	}
	return out
}
