package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/tools"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// importPlanPayload is the programmatic plan-graph import used by clients that
// author explicit tool_call nodes. It goes through the same owner isolation,
// validation, revision and review gate as planner-generated plans.
type importPlanPayload struct {
	Graph         *models.PlanGraph `json:"graph" binding:"required"`
	RequireReview bool              `json:"require_review,omitempty"`
	Title         string            `json:"title,omitempty"`
}

func (r *planRuntime) importPlan(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8<<20)
	var payload importPlanPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		respondError(c, http.StatusBadRequest, "invalid_plan_graph", "计划图格式错误", "提供完整 graph")
		return
	}
	if payload.Graph == nil {
		respondError(c, http.StatusBadRequest, "invalid_plan_graph", "graph 必填", "提供完整 graph")
		return
	}
	userID := resolveUserID(c)
	sessionID := resolveSessionID(c)
	validation := validatePlanGraphWithIntegrations(payload.Graph, userID, r.toolRegistry, r.profileRegistry)
	if !validation.Valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan validation failed", "code": "validation_failed", "validation_errors": validation.Errors})
		return
	}
	now := time.Now()
	plan := payload.Graph
	plan.ID = "plan-" + uuid.NewString()
	plan.OwnerID = userID
	plan.SessionID = sessionID
	plan.TraceID = firstNonEmptyString(plan.TraceID, uuid.NewString())
	plan.Status = models.StatusPending
	plan.CreatedAt = now
	plan.UpdatedAt = now
	if strings.TrimSpace(payload.Title) != "" {
		plan.Title = strings.TrimSpace(payload.Title)
	}
	if plan.Artifacts == nil {
		plan.Artifacts = map[string]models.Artifact{}
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		if node.CreatedAt.IsZero() {
			node.CreatedAt = now
		}
		node.UpdatedAt = now
		if node.Status == "" {
			node.Status = models.StatusPending
		}
	}
	intentContext := models.IntentContext{RawIntent: plan.UserIntent, IntentType: plan.IntentType}
	configurePlanGovernance(plan, intentContext, userID, sessionID)
	applyReviewGate(plan, payload.RequireReview)
	plan.AgentSnapshot = buildAgentSnapshots(plan, userID, r.profileRegistry)
	if resolver := modelresolver.Default(); resolver != nil {
		snapshot := resolver.Snapshot(userID)
		snapshot.CreatedAt = now.UTC()
		plan.ModelSnapshot = &snapshot
	}
	if _, err := r.revisionStore().Create(&models.PlanRevision{
		PlanID:    plan.ID,
		Graph:     plan,
		Status:    models.RevisionDraft,
		CreatedBy: userID,
		Reason:    "imported plan graph",
	}, 0); err != nil {
		respondError(c, http.StatusInternalServerError, "revision_store_unavailable", err.Error(), "检查 revision 存储")
		return
	} else {
		plan.CurrentRevision = 1
	}
	if err := r.store.SavePlan(plan); err != nil {
		respondError(c, http.StatusInternalServerError, "plan_store_error", err.Error(), "检查计划存储")
		return
	}
	_ = r.store.AppendEvent(plan.ID, models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_imported",
		Payload: map[string]any{
			"actor":      userID,
			"session_id": sessionID,
			"nodes":      len(plan.Nodes),
		},
		Timestamp: now,
	})
	recordAuditEvent("plan_imported", userID, plan.ID, map[string]any{"nodes": len(plan.Nodes)})
	c.JSON(http.StatusCreated, gin.H{"plan_graph": plan})
}

// validatePlanGraphWithIntegrations extends the graph validator with tool and
// AgentProfile existence checks. It is the planner-visible half of the tool
// contract; allowlist enforcement remains at execution time.
func validatePlanGraphWithIntegrations(plan *models.PlanGraph, ownerID string, toolRegistry *tools.Registry, profileRegistry *agentprofile.Registry) planValidationResult {
	result := validatePlanGraph(plan)
	if plan == nil {
		return result
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		var profile *models.AgentProfile
		if strings.HasPrefix(node.AssignedTo, "profile:") {
			if profileRegistry == nil {
				result.Valid = false
				result.Errors = append(result.Errors, newValidationError(node.ID, "assigned_to", "agent profile registry is not configured", "配置 AgentProfile 存储"))
				continue
			}
			profileID := node.AssignedTo
			resolved, err := profileRegistry.GetProfileForOwner(ownerID, profileID)
			if err != nil {
				result.Valid = false
				result.Errors = append(result.Errors, newValidationError(node.ID, "assigned_to", "unknown agent profile: "+profileID, "先创建并启用该 AgentProfile"))
				continue
			}
			if !resolved.Enabled {
				result.Valid = false
				result.Errors = append(result.Errors, newValidationError(node.ID, "assigned_to", "agent profile is disabled: "+profileID, "启用该 AgentProfile"))
				continue
			}
			profile = &resolved
		}
		if node.Type != "tool_call" {
			continue
		}
		toolID := stringInput(node.Inputs, "tool_id")
		if toolID == "" {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "inputs.tool_id", "tool_call node requires inputs.tool_id", "指定已注册工具"))
			continue
		}
		if toolRegistry == nil {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "inputs.tool_id", "tool registry is not configured", "配置工具存储"))
			continue
		}
		def, err := toolRegistry.GetToolForOwner(ownerID, toolID)
		if err != nil {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "inputs.tool_id", "unknown tool: "+toolID, "先注册该工具"))
			continue
		}
		if !def.Enabled {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "inputs.tool_id", "tool is disabled: "+toolID, "启用该工具"))
			continue
		}
		if version := stringInput(node.Inputs, "tool_version"); version != "" && version != def.Version {
			result.Valid = false
			result.Errors = append(result.Errors, newValidationError(node.ID, "inputs.tool_version", fmt.Sprintf("tool %s has version %s, requested %s", toolID, def.Version, version), "使用当前版本或重新注册"))
		}
		if profile != nil && len(profile.ToolAllowlist) > 0 && !containsFold(profile.ToolAllowlist, toolID) {
			// Deliberately not a validation error: the executor must reject the
			// call with tool_forbidden so the denial is auditable (N-13 A1).
			_ = profile
		}
	}
	return result
}

// buildAgentSnapshots freezes the profile/prompt/model versions used by a plan.
func buildAgentSnapshots(plan *models.PlanGraph, ownerID string, registry *agentprofile.Registry) []models.AgentProfileSnapshot {
	if plan == nil || registry == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]models.AgentProfileSnapshot, 0)
	for _, node := range plan.Nodes {
		if node == nil || !strings.HasPrefix(node.AssignedTo, "profile:") {
			continue
		}
		profileID := node.AssignedTo
		if _, exists := seen[profileID]; exists {
			continue
		}
		profile, err := registry.GetProfileForOwner(ownerID, profileID)
		if err != nil {
			continue
		}
		seen[profileID] = struct{}{}
		snapshot := models.AgentProfileSnapshot{
			AgentProfileID:  profile.ID,
			PromptVersionID: profile.PromptVersionID,
			ModelProfileID:  profile.ModelProfileID,
			Builtin:         profile.Builtin,
		}
		if prompt, err := registry.GetPromptForOwner(ownerID, profile.PromptVersionID); err == nil {
			snapshot.PromptVersion = prompt.Version
			snapshot.PromptHash = promptHash(prompt.Template)
		}
		if resolver := modelresolver.Default(); resolver != nil && resolver.Store() != nil {
			if modelProfile, ok := resolver.Store().Get(ownerID, profile.ModelProfileID); ok {
				snapshot.ModelRevision = modelProfile.Revision
			}
		}
		out = append(out, snapshot)
	}
	return out
}

// makeToolGate builds the scheduler-side approval policy for tool_call nodes.
// It also mints a stable call_id and a pending ToolCallRecord so the audit
// trail exists before the human decision.
func makeToolGate(registry *tools.Registry, profiles *agentprofile.Registry) scheduler.ToolGateFunc {
	return func(plan *models.PlanGraph, task *models.TaskNode) (models.RuntimeActionKind, string, map[string]any, string, bool) {
		if plan == nil || task == nil || task.Type != "tool_call" {
			return "", "", nil, "", false
		}
		toolID := stringInput(task.Inputs, "tool_id")
		if toolID == "" || registry == nil {
			return "", "", nil, "", false
		}
		def, err := registry.GetToolForOwner(plan.OwnerID, toolID)
		if err != nil || !def.Enabled {
			return "", "", nil, "", false
		}
		// Never ask a human to approve a call the allowlist or grant will reject:
		// the executor returns tool_forbidden without any side effect, and an
		// approval card would only create noise.
		if strings.HasPrefix(task.AssignedTo, "profile:") && profiles != nil {
			profile, profileErr := profiles.GetProfileForOwner(plan.OwnerID, task.AssignedTo)
			if profileErr == nil {
				effective := intersectAllowedTools(task.Contract.AllowedTools, profile.ToolAllowlist)
				if effective != nil && !containsFold(effective, toolID) {
					return "", "", nil, "", false
				}
				if grant, grantErr := registry.FindGrant(plan.OwnerID, profile.ID, toolID); grantErr != nil || grant == nil {
					return "", "", nil, "", false
				}
			}
		}
		if !def.NeedsApproval() {
			return "", "", nil, "", false
		}
		callID := stringInput(task.Inputs, "call_id")
		if callID == "" {
			callID = "tc-" + uuid.NewString()
			if task.Inputs == nil {
				task.Inputs = map[string]any{}
			}
			task.Inputs["call_id"] = callID
		}
		arguments := map[string]any{}
		if raw, ok := task.Inputs["arguments"].(map[string]any); ok {
			arguments = raw
		}
		details := map[string]any{
			"tool_id":      def.ID,
			"tool_version": def.Version,
			"call_id":      callID,
			"arguments":    tools.PreviewArguments(arguments),
			"scopes":       def.Scopes,
		}
		risk := "medium"
		for _, scope := range def.Scopes {
			switch strings.ToLower(strings.TrimSpace(scope)) {
			case "shell", "db", "fs.write":
				risk = "high"
			}
		}
		exists := false
		for i := range plan.ToolCalls {
			if plan.ToolCalls[i].ID == callID {
				exists = true
				break
			}
		}
		if !exists {
			now := time.Now()
			plan.ToolCalls = append(plan.ToolCalls, models.ToolCallRecord{
				ID:            callID,
				PlanID:        plan.ID,
				TaskID:        task.ID,
				ToolID:        def.ID,
				ToolVersion:   def.Version,
				Arguments:     arguments,
				ArgumentsHash: models.HashArguments(arguments),
				Status:        models.ToolCallPending,
				StartedAt:     now,
			})
		}
		return models.RuntimeActionToolCall, "调用工具 " + def.Name, details, risk, true
	}
}

func stringInput(inputs map[string]any, key string) string {
	if inputs == nil {
		return ""
	}
	value, ok := inputs[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), strings.TrimSpace(target)) {
			return true
		}
	}
	return false
}

func promptHash(template string) string {
	return models.HashResult(template)
}

// intersectAllowedTools mirrors the scheduler helper for the pre-execution
// approval gate: nil means unrestricted, an empty non-nil slice denies all.
func intersectAllowedTools(nodeAllowed, profileAllowed []string) []string {
	if profileAllowed == nil {
		return nodeAllowed
	}
	if nodeAllowed == nil {
		return append([]string{}, profileAllowed...)
	}
	out := make([]string, 0, len(nodeAllowed))
	for _, candidate := range nodeAllowed {
		for _, allowed := range profileAllowed {
			if strings.EqualFold(strings.TrimSpace(candidate), strings.TrimSpace(allowed)) {
				out = append(out, candidate)
				break
			}
		}
	}
	return out
}
