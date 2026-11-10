package tools

import (
	"context"
	"research-assistant-backend/internal/models"
	"time"
)

type approvalsKey struct{}
type approvals struct {
	owner, plan, task string
	actions           []models.RuntimeActionRequest
}

// WithApprovals must be populated from the server-owned plan, never model data.
func WithApprovals(ctx context.Context, owner, plan, task string, actions []models.RuntimeActionRequest) context.Context {
	return context.WithValue(ctx, approvalsKey{}, approvals{owner, plan, task, actions})
}
func ApprovedCallID(ctx context.Context, request InvokeRequest, now time.Time) string {
	authority, ok := ctx.Value(approvalsKey{}).(approvals)
	if !ok || authority.owner != request.OwnerID || authority.plan != request.PlanID || authority.task != request.TaskID {
		return ""
	}
	for _, action := range authority.actions {
		if action.PlanID != request.PlanID || action.TaskID != request.TaskID || action.Kind != models.RuntimeActionToolCall || action.Status != models.RuntimeActionApproved {
			continue
		}
		if request.ApprovalID != "" && action.ID != request.ApprovalID {
			continue
		}
		if action.Details["tool_id"] != request.ToolID || action.Details["tool_version"] != request.ToolVersion || action.Details["call_id"] != request.CallID || action.Details["arguments_hash"] != models.HashArguments(request.Arguments) {
			continue
		}
		expires, ok := action.Details["expires_at"].(string)
		if !ok {
			continue
		}
		expiry, err := time.Parse(time.RFC3339Nano, expires)
		if err != nil || !now.Before(expiry) {
			continue
		}
		return action.ID
	}
	return ""
}

// ApprovalFailure keeps a rejected or expired action terminal for this intent.
// Resuming cannot silently replace a human decision with a fresh request.
func ApprovalFailure(ctx context.Context, request InvokeRequest, now time.Time) string {
	authority, ok := ctx.Value(approvalsKey{}).(approvals)
	if !ok || authority.owner != request.OwnerID || authority.plan != request.PlanID || authority.task != request.TaskID {
		return ""
	}
	for _, action := range authority.actions {
		if action.PlanID != request.PlanID || action.TaskID != request.TaskID || action.Kind != models.RuntimeActionToolCall || action.Details["call_id"] != request.CallID {
			continue
		}
		if action.Status == models.RuntimeActionRejected {
			return "tool_approval_rejected"
		}
		if raw, ok := action.Details["expires_at"].(string); ok {
			expires, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil || !now.Before(expires) {
				return "tool_approval_expired"
			}
		}
	}
	return ""
}
