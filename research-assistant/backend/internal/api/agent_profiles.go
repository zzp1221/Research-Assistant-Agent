package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/tools"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterAgentProfileRoutes wires custom AgentProfile and PromptVersion CRUD.
func RegisterAgentProfileRoutes(apiGroup *gin.RouterGroup, registry *agentprofile.Registry, toolRegistry *tools.Registry) {
	if registry == nil {
		return
	}
	apiGroup.GET("/agent-profiles", func(c *gin.Context) {
		profiles, err := registry.ListProfiles(resolveUserID(c))
		if err != nil {
			respondError(c, http.StatusInternalServerError, "agent_store_error", err.Error(), "检查 Agent 存储")
			return
		}
		c.JSON(http.StatusOK, gin.H{"profiles": profiles})
	})

	apiGroup.POST("/agent-profiles", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		var payload agentProfilePayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, http.StatusBadRequest, "invalid_agent_profile", "AgentProfile 格式错误", "提供 name/prompt_version_id/model_profile_id")
			return
		}
		owner := resolveUserID(c)
		profile, err := buildAgentProfile(owner, payload)
		if err != nil {
			respondError(c, http.StatusBadRequest, "invalid_agent_profile", err.Error(), "修正 profile 配置")
			return
		}
		if err := validateAgentProfileRefs(owner, profile, registry, toolRegistry); err != nil {
			respondError(c, http.StatusBadRequest, "invalid_agent_profile", err.Error(), "确认 prompt/model/tool 均存在且可见")
			return
		}
		saved, err := registry.SaveProfile(profile)
		if err != nil {
			respondAgentError(c, err)
			return
		}
		recordAuditEvent("agent_profile_created", owner, saved.ID, map[string]any{"prompt_version_id": saved.PromptVersionID})
		c.JSON(http.StatusCreated, gin.H{"profile": saved})
	})

	apiGroup.PUT("/agent-profiles/:id", func(c *gin.Context) {
		var payload agentProfilePayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, http.StatusBadRequest, "invalid_agent_profile", "AgentProfile 格式错误", "提供需要修改的字段")
			return
		}
		owner := resolveUserID(c)
		updated, err := registry.UpdateProfile(owner, isAdminRequest(c), c.Param("id"), func(profile *models.AgentProfile) error {
			if payload.Name != "" {
				profile.Name = payload.Name
			}
			if payload.Description != "" {
				profile.Description = payload.Description
			}
			if payload.PromptVersionID != "" {
				profile.PromptVersionID = payload.PromptVersionID
			}
			if payload.ModelProfileID != "" {
				profile.ModelProfileID = payload.ModelProfileID
			}
			if payload.ToolAllowlist != nil {
				profile.ToolAllowlist = payload.ToolAllowlist
			}
			if payload.MaxSteps > 0 {
				profile.MaxSteps = payload.MaxSteps
			}
			if payload.OutputSchema != "" {
				profile.OutputSchema = payload.OutputSchema
			}
			if payload.Enabled != nil {
				profile.Enabled = *payload.Enabled
			}
			return nil
		})
		if err != nil {
			respondAgentError(c, err)
			return
		}
		if err := validateAgentProfileRefs(owner, updated, registry, toolRegistry); err != nil {
			respondError(c, http.StatusBadRequest, "invalid_agent_profile", err.Error(), "确认 prompt/model/tool 均存在且可见")
			return
		}
		recordAuditEvent("agent_profile_updated", owner, updated.ID, nil)
		c.JSON(http.StatusOK, gin.H{"profile": updated})
	})

	apiGroup.DELETE("/agent-profiles/:id", func(c *gin.Context) {
		owner := resolveUserID(c)
		if err := registry.DeleteProfile(owner, isAdminRequest(c), c.Param("id")); err != nil {
			respondAgentError(c, err)
			return
		}
		recordAuditEvent("agent_profile_disabled", owner, c.Param("id"), nil)
		c.JSON(http.StatusOK, gin.H{"disabled": true, "id": c.Param("id")})
	})

	apiGroup.POST("/agent-profiles/:id/eval", func(c *gin.Context) {
		var payload promptEvalPayload
		_ = c.ShouldBindJSON(&payload)
		owner := resolveUserID(c)
		profile, err := registry.GetProfileForOwner(owner, c.Param("id"))
		if err != nil {
			respondAgentError(c, err)
			return
		}
		if strings.TrimSpace(profile.PromptVersionID) == "" {
			respondError(c, http.StatusBadRequest, "invalid_agent_profile", "profile 没有绑定 PromptVersion", "先绑定 prompt_version_id")
			return
		}
		evaluator := agentprofile.Evaluator{Registry: registry, Completer: modelresolver.Default()}
		result, err := evaluator.Evaluate(c.Request.Context(), owner, profile.PromptVersionID, payload.Cases, payload.UseModel, profile.ModelProfileID)
		if err != nil {
			respondAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	})

	apiGroup.GET("/prompt-versions", func(c *gin.Context) {
		prompts, err := registry.ListPrompts(resolveUserID(c), strings.TrimSpace(c.Query("name")))
		if err != nil {
			respondError(c, http.StatusInternalServerError, "agent_store_error", err.Error(), "检查 Agent 存储")
			return
		}
		c.JSON(http.StatusOK, gin.H{"prompts": prompts})
	})

	apiGroup.POST("/prompt-versions", func(c *gin.Context) {
		var payload promptVersionPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, http.StatusBadRequest, "invalid_prompt", "Prompt 格式错误", "提供 name/template")
			return
		}
		owner := resolveUserID(c)
		prompt, err := registry.CreatePrompt(owner, payload.Name, payload.Template, owner)
		if err != nil {
			respondAgentError(c, err)
			return
		}
		recordAuditEvent("prompt_version_created", owner, prompt.ID, map[string]any{"name": prompt.Name, "version": prompt.Version})
		c.JSON(http.StatusCreated, gin.H{"prompt": prompt})
	})

	apiGroup.GET("/prompt-versions/:id", func(c *gin.Context) {
		prompt, err := registry.GetPromptForOwner(resolveUserID(c), c.Param("id"))
		if err != nil {
			respondAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"prompt": prompt})
	})

	apiGroup.POST("/prompt-versions/:id/activate", func(c *gin.Context) {
		owner := resolveUserID(c)
		prompt, err := registry.ActivatePrompt(owner, c.Param("id"))
		if err != nil {
			respondAgentError(c, err)
			return
		}
		recordAuditEvent("prompt_version_activated", owner, prompt.ID, map[string]any{"name": prompt.Name, "version": prompt.Version})
		c.JSON(http.StatusOK, gin.H{"prompt": prompt})
	})

	apiGroup.POST("/prompt-versions/:id/eval", func(c *gin.Context) {
		var payload promptEvalPayload
		_ = c.ShouldBindJSON(&payload)
		owner := resolveUserID(c)
		evaluator := agentprofile.Evaluator{Registry: registry, Completer: modelresolver.Default()}
		result, err := evaluator.Evaluate(c.Request.Context(), owner, c.Param("id"), payload.Cases, payload.UseModel, payload.ModelProfileID)
		if err != nil {
			respondAgentError(c, err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"result": result})
	})
}

type agentProfilePayload struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	PromptVersionID string   `json:"prompt_version_id"`
	ModelProfileID  string   `json:"model_profile_id"`
	ToolAllowlist   []string `json:"tool_allowlist"`
	MaxSteps        int      `json:"max_steps"`
	OutputSchema    string   `json:"output_schema"`
	Enabled         *bool    `json:"enabled"`
}

type promptVersionPayload struct {
	Name     string `json:"name"`
	Template string `json:"template"`
}

type promptEvalPayload struct {
	Cases          []models.PromptEvalCase `json:"cases"`
	UseModel       bool                    `json:"use_model"`
	ModelProfileID string                  `json:"model_profile_id"`
}

func buildAgentProfile(owner string, payload agentProfilePayload) (models.AgentProfile, error) {
	if strings.TrimSpace(payload.Name) == "" {
		return models.AgentProfile{}, fmt.Errorf("name is required")
	}
	if strings.TrimSpace(payload.PromptVersionID) == "" {
		return models.AgentProfile{}, fmt.Errorf("prompt_version_id is required for a custom profile")
	}
	if strings.TrimSpace(payload.ModelProfileID) == "" {
		return models.AgentProfile{}, fmt.Errorf("model_profile_id is required for a custom profile")
	}
	if payload.OutputSchema != "" && !json.Valid([]byte(payload.OutputSchema)) {
		return models.AgentProfile{}, fmt.Errorf("output_schema is not valid JSON")
	}
	enabled := true
	if payload.Enabled != nil {
		enabled = *payload.Enabled
	}
	maxSteps := payload.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}
	if maxSteps > 64 {
		return models.AgentProfile{}, fmt.Errorf("max_steps must be <= 64")
	}
	return models.AgentProfile{
		ID:              "profile:" + uuid.NewString(),
		OwnerID:         owner,
		Name:            strings.TrimSpace(payload.Name),
		Description:     payload.Description,
		PromptVersionID: strings.TrimSpace(payload.PromptVersionID),
		ModelProfileID:  strings.TrimSpace(payload.ModelProfileID),
		ToolAllowlist:   payload.ToolAllowlist,
		MaxSteps:        maxSteps,
		OutputSchema:    payload.OutputSchema,
		Builtin:         false,
		Enabled:         enabled,
		CreatedBy:       owner,
	}, nil
}

func validateAgentProfileRefs(owner string, profile models.AgentProfile, registry *agentprofile.Registry, toolRegistry *tools.Registry) error {
	if profile.PromptVersionID != "" {
		if _, err := registry.GetPromptForOwner(owner, profile.PromptVersionID); err != nil {
			return fmt.Errorf("prompt_version_id %s is not visible", profile.PromptVersionID)
		}
	}
	if profile.ModelProfileID != "" && !strings.EqualFold(profile.ModelProfileID, "env-bootstrap") {
		resolver := modelresolver.Default()
		if resolver == nil || resolver.Store() == nil {
			return fmt.Errorf("model profile store is not configured")
		}
		if _, ok := resolver.Store().Get(owner, profile.ModelProfileID); !ok {
			return fmt.Errorf("model_profile_id %s is not visible", profile.ModelProfileID)
		}
	}
	if toolRegistry != nil {
		for _, toolID := range profile.ToolAllowlist {
			if _, err := toolRegistry.GetToolForOwner(owner, toolID); err != nil {
				return fmt.Errorf("tool %s is not visible", toolID)
			}
		}
	}
	return nil
}

func respondAgentError(c *gin.Context, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, agentprofile.ErrNotFound):
		respondError(c, http.StatusNotFound, "agent_not_found", err.Error(), "确认 id")
	case errors.Is(err, agentprofile.ErrForbidden):
		respondError(c, http.StatusForbidden, "agent_forbidden", err.Error(), "只有 owner 或管理员可以修改")
	case errors.Is(err, agentprofile.ErrConflict):
		respondError(c, http.StatusConflict, "agent_conflict", err.Error(), "使用新的名称")
	case errors.Is(err, agentprofile.ErrInvalid):
		respondError(c, http.StatusBadRequest, "invalid_agent_profile", err.Error(), "修正配置")
	default:
		respondError(c, http.StatusInternalServerError, "agent_store_error", err.Error(), "检查 Agent 存储")
	}
}
