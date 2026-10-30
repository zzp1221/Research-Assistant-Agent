package api

import (
	"net/http"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/tools"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type replayToolCallPayload struct {
	AllowSideEffects bool   `json:"allow_side_effects"`
	ApprovalID       string `json:"approval_id"`
}

func (r *planRuntime) listToolCalls(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"tool_calls": plan.ToolCalls})
}

// replayToolCall performs a dry-run comparison by default and requires an
// approved tool_call action before any side effect is repeated.
func (r *planRuntime) replayToolCall(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if r.toolInvoker == nil {
		respondError(c, http.StatusInternalServerError, "tool_invoker_unavailable", "工具运行时未配置", "检查后端初始化")
		return
	}
	var payload replayToolCallPayload
	_ = c.ShouldBindJSON(&payload)
	callID := c.Param("callId")
	var record *models.ToolCallRecord
	for i := range plan.ToolCalls {
		if plan.ToolCalls[i].ID == callID {
			record = &plan.ToolCalls[i]
			break
		}
	}
	if record == nil {
		respondError(c, http.StatusNotFound, "tool_call_not_found", "工具调用记录不存在", "确认 call id")
		return
	}
	if record.Status != models.ToolCallCompleted && record.Status != models.ToolCallFailed {
		respondError(c, http.StatusConflict, "tool_call_not_replayable", "只有已完成或失败的调用可以回放", "等待调用结束")
		return
	}
	def, err := r.toolRegistry.GetToolForOwner(plan.OwnerID, record.ToolID)
	if err != nil {
		respondError(c, http.StatusNotFound, "tool_not_found", "工具已不存在", "重新注册工具")
		return
	}
	if !payload.AllowSideEffects {
		if def.SideEffect && (def.Transport == models.ToolTransportMCPStdio || def.Transport == models.ToolTransportMCPHTTP) {
			respondError(c, http.StatusConflict, "dry_run_unsupported", "该 MCP 工具不支持 dry-run", "使用 allow_side_effects=true 并完成审批")
			return
		}
		result := r.toolInvoker.Invoke(c.Request.Context(), tools.InvokeRequest{
			PlanID:         plan.ID,
			TaskID:         record.TaskID,
			ToolID:         record.ToolID,
			ToolVersion:    record.ToolVersion,
			Arguments:      record.Arguments,
			AgentProfileID: record.AgentProfileID,
			OwnerID:        plan.OwnerID,
			DryRun:         true,
			CallID:         "replay-" + uuid.NewString(),
			ReplayOf:       record.ID,
		})
		c.JSON(http.StatusOK, gin.H{
			"dry_run":       true,
			"match":         result.Record.ResultHash == record.ResultHash,
			"expected_hash": record.ResultHash,
			"actual_hash":   result.Record.ResultHash,
			"record":        result.Record,
		})
		return
	}
	if strings.TrimSpace(payload.ApprovalID) == "" {
		action, err := r.createReplayApproval(plan, record)
		if err != nil {
			respondError(c, http.StatusConflict, "plan_not_replayable", err.Error(), "刷新计划后重试")
			return
		}
		c.JSON(http.StatusAccepted, gin.H{"status": "pending_approval", "approval_id": action.ID, "action": action})
		return
	}
	approved := false
	for i := range plan.RuntimeActions {
		action := plan.RuntimeActions[i]
		if action.ID != payload.ApprovalID || action.Kind != models.RuntimeActionToolCall || action.Status != models.RuntimeActionApproved {
			continue
		}
		if replayOf, _ := action.Details["replay_of"].(string); replayOf == record.ID {
			approved = true
			break
		}
	}
	if !approved {
		respondError(c, http.StatusConflict, "approval_required", "真实重放需要已批准的 tool_call action", "先申请审批并批准")
		return
	}
	result := r.toolInvoker.Invoke(c.Request.Context(), tools.InvokeRequest{
		PlanID:         plan.ID,
		TaskID:         record.TaskID,
		ToolID:         record.ToolID,
		ToolVersion:    record.ToolVersion,
		Arguments:      record.Arguments,
		AgentProfileID: record.AgentProfileID,
		OwnerID:        plan.OwnerID,
		ApprovalID:     payload.ApprovalID,
		CallID:         "replay-" + uuid.NewString(),
		ReplayOf:       record.ID,
	})
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		upsertPlanToolCall(current, &result.Record)
		return nil
	}); err != nil {
		respondError(c, http.StatusInternalServerError, "plan_store_error", err.Error(), "重试或检查计划存储")
		return
	}
	c.JSON(http.StatusOK, gin.H{"replayed": true, "record": result.Record, "match": result.Record.ResultHash == record.ResultHash})
}

func (r *planRuntime) createReplayApproval(plan *models.PlanGraph, record *models.ToolCallRecord) (*models.RuntimeActionRequest, error) {
	now := time.Now()
	action := models.RuntimeActionRequest{
		ID:          "ra-" + uuid.NewString(),
		PlanID:      plan.ID,
		TaskID:      record.TaskID,
		NodeType:    "tool_call",
		Kind:        models.RuntimeActionToolCall,
		Title:       "重放工具调用 " + record.ToolID,
		Details:     map[string]any{"tool_id": record.ToolID, "call_id": record.ID, "replay_of": record.ID, "arguments": tools.PreviewArguments(record.Arguments)},
		Risk:        "medium",
		Status:      models.RuntimeActionPending,
		RequestedAt: now,
	}
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		// A replay approval is an audit-only decision on a finished plan; it must
		// not resurrect or mutate execution state.
		current.RuntimeActions = append(current.RuntimeActions, action)
		return nil
	}); err != nil {
		return nil, err
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		TaskID:    record.TaskID,
		EventType: "runtime_action_requested",
		Payload:   map[string]any{"action": action, "replay_of": record.ID},
		Timestamp: now,
	}
	_ = r.store.AppendEvent(plan.ID, event)
	r.events.Publish(plan.ID, event)
	return &action, nil
}

func upsertPlanToolCall(plan *models.PlanGraph, record *models.ToolCallRecord) {
	if plan == nil || record == nil {
		return
	}
	for i := range plan.ToolCalls {
		if plan.ToolCalls[i].ID == record.ID {
			plan.ToolCalls[i] = *record
			return
		}
	}
	plan.ToolCalls = append(plan.ToolCalls, *record)
}
