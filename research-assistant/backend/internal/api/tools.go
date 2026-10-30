package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/tools"

	"github.com/gin-gonic/gin"
)

// RegisterToolRoutes wires the tool registry, grants and MCP registration.
func RegisterToolRoutes(apiGroup *gin.RouterGroup, registry *tools.Registry, invoker *tools.Invoker, profiles *agentprofile.Registry) {
	if registry == nil || invoker == nil {
		return
	}
	apiGroup.GET("/tools", func(c *gin.Context) {
		owner := resolveUserID(c)
		defs, err := registry.ListTools(owner)
		if err != nil {
			respondError(c, http.StatusInternalServerError, "tool_store_error", err.Error(), "检查工具存储配置")
			return
		}
		servers, _ := registry.ListServers(owner)
		toolPayload := make([]models.ToolDefinition, 0, len(defs))
		for _, def := range defs {
			toolPayload = append(toolPayload, sanitizeToolDefinition(def))
		}
		serverPayload := make([]models.MCPServer, 0, len(servers))
		for _, server := range servers {
			serverPayload = append(serverPayload, sanitizeMCPServer(server))
		}
		c.JSON(http.StatusOK, gin.H{"tools": toolPayload, "servers": serverPayload})
	})

	apiGroup.POST("/tools", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
		var payload createToolPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, http.StatusBadRequest, "invalid_tool", "工具定义格式错误", "提供 id/name/version/transport")
			return
		}
		owner := resolveUserID(c)
		admin := isAdminRequest(c)
		global := payload.Global && admin
		ownerID := owner
		if global {
			ownerID = ""
		}
		if strings.TrimSpace(payload.Transport) == "" {
			payload.Transport = models.ToolTransportNative
		}
		if payload.Transport == models.ToolTransportMCPStdio || payload.Transport == models.ToolTransportMCPHTTP {
			handleCreateMCPServer(c, registry, invoker, profiles, payload, ownerID, admin)
			return
		}
		def := models.ToolDefinition{
			ID:             strings.TrimSpace(payload.ID),
			OwnerID:        ownerID,
			Name:           strings.TrimSpace(payload.Name),
			Version:        strings.TrimSpace(payload.Version),
			Transport:      payload.Transport,
			Description:    payload.Description,
			InputSchema:    payload.InputSchema,
			OutputSchema:   payload.OutputSchema,
			Scopes:         payload.Scopes,
			SideEffect:     payload.SideEffect,
			TimeoutSec:     payload.TimeoutSec,
			MaxOutputBytes: payload.MaxOutputBytes,
			Approval:       payload.Approval,
			Enabled:        true,
			Native:         payload.Native,
			CreatedBy:      owner,
		}
		if payload.Enabled != nil {
			def.Enabled = *payload.Enabled
		}
		if payload.Transport == models.ToolTransportHTTP {
			if payload.HTTP == nil || strings.TrimSpace(payload.HTTP.URL) == "" {
				respondError(c, http.StatusBadRequest, "invalid_tool", "http.url 必填", "提供 http.url")
				return
			}
			if payload.HTTP.AllowPrivateHosts && !admin {
				respondError(c, http.StatusForbidden, "tool_forbidden", "只有管理员可以允许私网工具地址", "使用 ADMIN_TOKEN，或提供公网 https 地址")
				return
			}
			config := &models.HTTPToolConfig{URL: payload.HTTP.URL, Method: payload.HTTP.Method, AllowPrivateHosts: payload.HTTP.AllowPrivateHosts}
			for key, value := range payload.HTTP.Headers {
				sealed, err := tools.SealSecretValue(value, "http:"+def.ID, ownerID)
				if err != nil {
					respondError(c, http.StatusBadRequest, "secret_unavailable", err.Error(), "配置 MODEL_PROFILE_ENCRYPTION_KEY")
					return
				}
				config.HeaderKeys = append(config.HeaderKeys, key)
				if config.HeaderSealed == nil {
					config.HeaderSealed = map[string]string{}
				}
				config.HeaderSealed[key] = sealed
			}
			def.HTTP = config
		}
		if err := registry.SaveTool(def); err != nil {
			respondToolError(c, err)
			return
		}
		recordAuditEvent("tool_registered", owner, def.ID, map[string]any{"transport": def.Transport})
		c.JSON(http.StatusCreated, gin.H{"tool": sanitizeToolDefinition(def)})
	})

	apiGroup.GET("/tools/:id", func(c *gin.Context) {
		def, err := registry.GetToolForOwner(resolveUserID(c), c.Param("id"))
		if err != nil {
			respondToolError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"tool": sanitizeToolDefinition(def)})
	})

	apiGroup.GET("/tools/:id/schema", func(c *gin.Context) {
		def, err := registry.GetToolForOwner(resolveUserID(c), c.Param("id"))
		if err != nil {
			respondToolError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"id":            def.ID,
			"version":       def.Version,
			"input_schema":  def.InputSchema,
			"output_schema": def.OutputSchema,
		})
	})

	apiGroup.PUT("/tools/:id", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var payload updateToolPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, http.StatusBadRequest, "invalid_tool", "工具更新格式错误", "提供需要修改的字段")
			return
		}
		owner := resolveUserID(c)
		admin := isAdminRequest(c)
		updated, err := registry.UpdateTool(owner, admin, c.Param("id"), func(def *models.ToolDefinition) error {
			if payload.Version != nil && strings.TrimSpace(*payload.Version) != def.Version {
				return fmt.Errorf("%w: tool versions are immutable; register a new tool id for a new version", tools.ErrInvalid)
			}
			if payload.Name != nil {
				def.Name = strings.TrimSpace(*payload.Name)
			}
			if payload.Description != nil {
				def.Description = *payload.Description
			}
			if payload.Enabled != nil {
				def.Enabled = *payload.Enabled
			}
			if payload.Approval != nil {
				def.Approval = *payload.Approval
			}
			if payload.TimeoutSec != nil {
				def.TimeoutSec = *payload.TimeoutSec
			}
			if payload.MaxOutputBytes != nil {
				def.MaxOutputBytes = *payload.MaxOutputBytes
			}
			if payload.Scopes != nil {
				def.Scopes = *payload.Scopes
			}
			if payload.SideEffect != nil {
				def.SideEffect = *payload.SideEffect
			}
			if payload.InputSchema != nil {
				def.InputSchema = *payload.InputSchema
			}
			if payload.OutputSchema != nil {
				def.OutputSchema = *payload.OutputSchema
			}
			return nil
		})
		if err != nil {
			respondToolError(c, err)
			return
		}
		recordAuditEvent("tool_updated", owner, updated.ID, map[string]any{"enabled": updated.Enabled})
		c.JSON(http.StatusOK, gin.H{"tool": sanitizeToolDefinition(updated)})
	})

	apiGroup.DELETE("/tools/:id", func(c *gin.Context) {
		owner := resolveUserID(c)
		admin := isAdminRequest(c)
		if err := registry.DisableTool(owner, admin, c.Param("id")); err != nil {
			// A server id is not a tool id; allow deleting the server and its
			// discovered tools for cleanup.
			if errors.Is(err, tools.ErrNotFound) {
				if serverErr := registry.DeleteServer(owner, admin, c.Param("id")); serverErr == nil {
					recordAuditEvent("mcp_server_deleted", owner, c.Param("id"), nil)
					c.JSON(http.StatusOK, gin.H{"deleted": true, "id": c.Param("id")})
					return
				}
			}
			respondToolError(c, err)
			return
		}
		recordAuditEvent("tool_disabled", owner, c.Param("id"), nil)
		c.JSON(http.StatusOK, gin.H{"disabled": true, "id": c.Param("id")})
	})

	apiGroup.POST("/tools/:id/test", func(c *gin.Context) {
		var payload toolTestPayload
		if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
			respondError(c, http.StatusBadRequest, "invalid_tool_test", "测试参数格式错误", "提供 arguments")
			return
		}
		owner := resolveUserID(c)
		dryRun := true
		if payload.DryRun != nil {
			dryRun = *payload.DryRun
		}
		result := invoker.Invoke(c.Request.Context(), tools.InvokeRequest{
			PlanID:    "tool-test",
			TaskID:    "tool-test",
			ToolID:    c.Param("id"),
			Arguments: payload.Arguments,
			OwnerID:   owner,
			DryRun:    dryRun,
			CallID:    "test-" + fmt.Sprint(time.Now().UnixNano()),
		})
		if result.ErrorCode == "tool_approval_required" {
			c.JSON(http.StatusConflict, gin.H{"error": "tool requires approval", "code": "tool_approval_required", "record": result.Record})
			return
		}
		status := http.StatusOK
		if result.Record.Status != models.ToolCallCompleted {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"ok": result.Record.Status == models.ToolCallCompleted, "record": result.Record, "output": result.Text})
	})

	apiGroup.GET("/tool-grants", func(c *gin.Context) {
		grants, err := registry.ListGrants(resolveUserID(c))
		if err != nil {
			respondError(c, http.StatusInternalServerError, "tool_store_error", err.Error(), "检查工具存储")
			return
		}
		c.JSON(http.StatusOK, gin.H{"grants": grants})
	})

	apiGroup.POST("/tool-grants", func(c *gin.Context) {
		var payload toolGrantPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, http.StatusBadRequest, "invalid_grant", "授权格式错误", "提供 agent_profile_id 与 tool_id")
			return
		}
		owner := resolveUserID(c)
		if profiles != nil {
			if _, err := profiles.GetProfileForOwner(owner, payload.AgentProfileID); err != nil {
				respondError(c, http.StatusBadRequest, "unknown_agent_profile", "AgentProfile 不存在或不可见", "先创建 profile")
				return
			}
		}
		if _, err := registry.GetToolForOwner(owner, payload.ToolID); err != nil {
			respondToolError(c, err)
			return
		}
		grant, err := registry.SaveGrant(models.ToolGrant{
			OwnerID:        owner,
			AgentProfileID: payload.AgentProfileID,
			ToolID:         payload.ToolID,
			Scopes:         payload.Scopes,
			Approval:       payload.Approval,
			CreatedBy:      owner,
		})
		if err != nil {
			respondToolError(c, err)
			return
		}
		recordAuditEvent("tool_granted", owner, grant.ToolID, map[string]any{"agent_profile_id": grant.AgentProfileID})
		c.JSON(http.StatusCreated, gin.H{"grant": grant})
	})

	apiGroup.DELETE("/tool-grants/:id", func(c *gin.Context) {
		owner := resolveUserID(c)
		if err := registry.DeleteGrant(owner, c.Param("id")); err != nil {
			respondToolError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	})
}

func handleCreateMCPServer(c *gin.Context, registry *tools.Registry, invoker *tools.Invoker, profiles *agentprofile.Registry, payload createToolPayload, ownerID string, admin bool) {
	config := payload.MCP
	if config == nil {
		config = &mcpServerPayload{}
	}
	serverID := strings.TrimSpace(config.ID)
	if serverID == "" {
		serverID = strings.TrimSpace(payload.ID)
	}
	if serverID == "" {
		respondError(c, http.StatusBadRequest, "invalid_tool", "MCP server id 必填", "提供 id")
		return
	}
	if !strings.HasPrefix(serverID, "mcp:") {
		serverID = "mcp:" + serverID
	}
	if config.AllowPrivateHosts && !admin {
		respondError(c, http.StatusForbidden, "tool_forbidden", "只有管理员可以允许私网 MCP 地址", "使用 ADMIN_TOKEN，或提供公网 https 地址")
		return
	}
	server := models.MCPServer{
		ID:                serverID,
		OwnerID:           ownerID,
		Name:              firstNonEmptyString(config.Name, payload.Name, serverID),
		Transport:         payload.Transport,
		Command:           strings.TrimSpace(config.Command),
		Args:              config.Args,
		WorkingDir:        strings.TrimSpace(config.WorkingDir),
		URL:               strings.TrimSpace(config.URL),
		AllowPrivateHosts: config.AllowPrivateHosts,
		TimeoutSec:        firstPositive(config.TimeoutSec, payload.TimeoutSec, 20),
		MaxOutputBytes:    firstPositiveBytes(config.MaxOutputBytes, payload.MaxOutputBytes, 8<<20),
		CreatedBy:         ownerID,
	}
	for key, value := range config.Env {
		sealed, err := tools.SealSecretValue(value, "mcp:"+server.ID, ownerID)
		if err != nil {
			respondError(c, http.StatusBadRequest, "secret_unavailable", err.Error(), "配置 MODEL_PROFILE_ENCRYPTION_KEY")
			return
		}
		if server.EnvSealed == nil {
			server.EnvSealed = map[string]string{}
		}
		server.EnvSealed[key] = sealed
		server.EnvKeys = append(server.EnvKeys, key)
	}
	for key, value := range config.Headers {
		sealed, err := tools.SealSecretValue(value, "mcp:"+server.ID, ownerID)
		if err != nil {
			respondError(c, http.StatusBadRequest, "secret_unavailable", err.Error(), "配置 MODEL_PROFILE_ENCRYPTION_KEY")
			return
		}
		if server.HeaderSealed == nil {
			server.HeaderSealed = map[string]string{}
		}
		server.HeaderSealed[key] = sealed
		server.HeaderKeys = append(server.HeaderKeys, key)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(server.TimeoutSec)*time.Second)
	defer cancel()
	specs, fingerprint, err := invoker.MCP.Discover(ctx, server)
	if err != nil {
		detail := err.Error()
		if strings.Contains(detail, "private") || strings.Contains(detail, "not allowed") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "mcp url blocked by SSRF policy", "code": "ssrf_blocked", "detail": detail})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "mcp connect failed", "code": "mcp_connect_failed", "detail": detail})
		return
	}
	if len(specs) == 0 {
		respondError(c, http.StatusBadRequest, "mcp_no_tools", "MCP server 没有返回任何工具", "确认 server 实现 tools/list")
		return
	}
	if existing, existingErr := registry.GetServer(server.ID); existingErr == nil && existing.Fingerprint != "" && existing.Fingerprint != fingerprint {
		// Re-registration is the explicit re-review action for an MCP server.
		// Record the schema change so the trust decision is auditable.
		recordAuditEvent("mcp_schema_changed", ownerID, server.ID, map[string]any{
			"previous_fingerprint": existing.Fingerprint,
			"new_fingerprint":      fingerprint,
		})
	}
	server.Fingerprint = fingerprint
	server.Status = "ready"
	if err := registry.SaveServer(server); err != nil {
		respondToolError(c, err)
		return
	}
	if saved, err := registry.GetServer(server.ID); err == nil {
		server = saved
	}
	defs := make([]models.ToolDefinition, 0, len(specs))
	for _, spec := range specs {
		defs = append(defs, models.ToolDefinition{
			ID:             server.ID + "/" + spec.Name,
			OwnerID:        ownerID,
			Name:           spec.Name,
			Version:        "1.0.0",
			Transport:      payload.Transport,
			Description:    spec.Description,
			InputSchema:    spec.InputSchema,
			Scopes:         payload.Scopes,
			SideEffect:     payload.SideEffect,
			TimeoutSec:     firstPositive(payload.TimeoutSec, server.TimeoutSec, 20),
			MaxOutputBytes: firstPositiveBytes(payload.MaxOutputBytes, server.MaxOutputBytes, 8<<20),
			Approval:       payload.Approval,
			Enabled:        true,
			ServerID:       server.ID,
			RemoteName:     spec.Name,
			CreatedBy:      ownerID,
		})
	}
	if err := registry.ReplaceServerTools(server.ID, defs); err != nil {
		respondToolError(c, err)
		return
	}
	recordAuditEvent("mcp_server_registered", ownerID, server.ID, map[string]any{"tools": len(defs), "transport": server.Transport})
	_ = profiles
	c.JSON(http.StatusCreated, gin.H{"server": sanitizeMCPServer(server), "tools": defs})
}

type createToolPayload struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Version        string            `json:"version"`
	Transport      string            `json:"transport"`
	Description    string            `json:"description"`
	InputSchema    string            `json:"input_schema"`
	OutputSchema   string            `json:"output_schema"`
	Scopes         []string          `json:"scopes"`
	SideEffect     bool              `json:"side_effect"`
	TimeoutSec     int               `json:"timeout_sec"`
	MaxOutputBytes int64             `json:"max_output_bytes"`
	Approval       string            `json:"approval"`
	Enabled        *bool             `json:"enabled"`
	Global         bool              `json:"global"`
	Native         string            `json:"native"`
	HTTP           *httpToolPayload  `json:"http"`
	MCP            *mcpServerPayload `json:"mcp"`
}

type httpToolPayload struct {
	URL               string            `json:"url"`
	Method            string            `json:"method"`
	Headers           map[string]string `json:"headers"`
	AllowPrivateHosts bool              `json:"allow_private_hosts"`
}

type mcpServerPayload struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Command           string            `json:"command"`
	Args              []string          `json:"args"`
	Env               map[string]string `json:"env"`
	WorkingDir        string            `json:"working_dir"`
	URL               string            `json:"url"`
	Headers           map[string]string `json:"headers"`
	AllowPrivateHosts bool              `json:"allow_private_hosts"`
	TimeoutSec        int               `json:"timeout_sec"`
	MaxOutputBytes    int64             `json:"max_output_bytes"`
}

type updateToolPayload struct {
	Name           *string   `json:"name"`
	Description    *string   `json:"description"`
	Version        *string   `json:"version"`
	Enabled        *bool     `json:"enabled"`
	Approval       *string   `json:"approval"`
	TimeoutSec     *int      `json:"timeout_sec"`
	MaxOutputBytes *int64    `json:"max_output_bytes"`
	Scopes         *[]string `json:"scopes"`
	SideEffect     *bool     `json:"side_effect"`
	InputSchema    *string   `json:"input_schema"`
	OutputSchema   *string   `json:"output_schema"`
}

type toolTestPayload struct {
	Arguments map[string]any `json:"arguments"`
	DryRun    *bool          `json:"dry_run"`
}

type toolGrantPayload struct {
	AgentProfileID string   `json:"agent_profile_id"`
	ToolID         string   `json:"tool_id"`
	Scopes         []string `json:"scopes"`
	Approval       string   `json:"approval"`
}

func sanitizeToolDefinition(def models.ToolDefinition) models.ToolDefinition {
	if def.HTTP != nil {
		copy := *def.HTTP
		copy.HeaderSealed = nil
		def.HTTP = &copy
	}
	return def
}

func sanitizeMCPServer(server models.MCPServer) models.MCPServer {
	server.EnvSealed = nil
	server.HeaderSealed = nil
	return server
}

func respondToolError(c *gin.Context, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, tools.ErrNotFound):
		respondError(c, http.StatusNotFound, "tool_not_found", err.Error(), "确认工具 id")
	case errors.Is(err, tools.ErrForbidden):
		respondError(c, http.StatusForbidden, "tool_forbidden", err.Error(), "只有 owner 或管理员可以修改")
	case errors.Is(err, tools.ErrConflict):
		respondError(c, http.StatusConflict, "tool_conflict", err.Error(), "使用新的工具 id 或版本")
	case errors.Is(err, tools.ErrInvalid):
		respondError(c, http.StatusBadRequest, "invalid_tool", err.Error(), "修正工具定义")
	default:
		respondError(c, http.StatusInternalServerError, "tool_store_error", err.Error(), "检查工具存储")
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstPositiveBytes(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
