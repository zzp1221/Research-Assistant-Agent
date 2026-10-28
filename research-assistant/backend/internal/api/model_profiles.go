package api

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/audit"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/oaichat"

	"github.com/gin-gonic/gin"
)

// probeLimiter throttles probe/test calls per profile so a user cannot hammer a
// paid provider from the settings page.
type probeLimiter struct {
	mu       sync.Mutex
	last     map[string]time.Time
	minEvery time.Duration
}

func newProbeLimiter() *probeLimiter {
	seconds := envIntDefault("MODEL_PROBE_RATE_LIMIT_SECONDS", 5)
	return &probeLimiter{last: map[string]time.Time{}, minEvery: time.Duration(seconds) * time.Second}
}

func (l *probeLimiter) allow(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if previous, ok := l.last[key]; ok && now.Sub(previous) < l.minEvery {
		return false
	}
	l.last[key] = now
	return true
}

type modelProfilePayload struct {
	Name                 string   `json:"name"`
	Provider             string   `json:"provider"`
	BaseURL              string   `json:"base_url"`
	Model                string   `json:"model"`
	APIKey               string   `json:"api_key"`
	Roles                []string `json:"roles"`
	SupportsVision       string   `json:"supports_vision"`
	TimeoutSeconds       int      `json:"timeout_seconds"`
	MaxOutputTokens      int      `json:"max_output_tokens"`
	ContextWindowTokens  int      `json:"context_window_tokens"`
	PromptPricePer1K     float64  `json:"prompt_price_per_1k"`
	CompletionPricePer1K float64  `json:"completion_price_per_1k"`
	Global               bool     `json:"global"`
}

type modelRoutingPayload struct {
	MainProfileID      string   `json:"main_profile_id"`
	VisionProfileIDs   []string `json:"vision_profile_ids"`
	FallbackProfileIDs []string `json:"fallback_profile_ids"`
}

// isAdminRequest accepts the ADMIN_TOKEN bearer token as an operator identity.
func isAdminRequest(c *gin.Context) bool {
	expected := strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))
	if expected == "" {
		return false
	}
	provided := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	if provided == expected {
		return true
	}
	return strings.TrimSpace(c.GetHeader("X-Admin-Token")) == expected
}

func respondModelProfileError(c *gin.Context, err error) {
	switch {
	case errorsIs(err, modelprofile.ErrNotFound):
		respondError(c, http.StatusNotFound, "model_profile_not_found", "模型配置不存在或无权访问", "检查 id 是否属于当前账号，或刷新列表后重试。")
	case errorsIs(err, modelprofile.ErrForbidden):
		respondError(c, http.StatusForbidden, "model_profile_forbidden", "无权修改全局模型配置", "全局模型只能由管理员通过 ADMIN_TOKEN 修改。")
	case errorsIs(err, modelprofile.ErrNameExists):
		respondError(c, http.StatusConflict, "model_profile_invalid", "同名模型配置已存在", "为该配置换一个名称。")
	case errorsIs(err, modelprofile.ErrQuotaExceeded):
		respondError(c, http.StatusTooManyRequests, "rate_limited", "模型配置数量已达上限", "删除不再使用的配置，或调整 MODEL_PROFILE_MAX_PER_USER。")
	case errorsIs(err, modelprofile.ErrEncryptionUnavailable):
		respondError(c, http.StatusServiceUnavailable, "model_profile_encryption_unavailable", "服务端未配置模型密钥加密", "管理员需设置 32 字节的 MODEL_PROFILE_ENCRYPTION_KEY 后重试。")
	case errorsIs(err, modelprofile.ErrInUse):
		respondError(c, http.StatusConflict, "model_profile_invalid", "该配置仍被路由引用", "先在模型路由中移除对该配置的引用，再删除。")
	case errorsIs(err, modelprofile.ErrInvalid):
		respondError(c, http.StatusBadRequest, "model_profile_invalid", err.Error(), "检查名称、base_url、模型标识与密钥是否填写正确。")
	default:
		respondError(c, http.StatusInternalServerError, "model_profile_invalid", "模型配置操作失败", "稍后重试；若持续失败请查看后端日志。")
	}
}

func errorsIs(err, target error) bool {
	return err != nil && target != nil && (err == target || strings.Contains(err.Error(), target.Error()))
}

func registerModelProfileRoutes(apiGroup *gin.RouterGroup) {
	store := modelProfileStore()
	if store == nil {
		return
	}
	limiter := newProbeLimiter()

	apiGroup.GET("/model-profiles", func(c *gin.Context) {
		ownerID := resolveUserID(c)
		profiles := store.List(ownerID)
		items := make([]gin.H, 0, len(profiles))
		for _, profile := range profiles {
			items = append(items, profileResponse(profile))
		}
		c.JSON(http.StatusOK, gin.H{
			"profiles":             items,
			"encryption_available": modelprofile.EncryptionAvailable(),
			"is_admin":             isAdminRequest(c),
			"max_per_user":         envIntDefault("MODEL_PROFILE_MAX_PER_USER", 10),
		})
	})

	apiGroup.POST("/model-profiles", func(c *gin.Context) {
		var payload modelProfilePayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, http.StatusBadRequest, "model_profile_invalid", "请求体格式不正确", "提供 name/provider/base_url/model/api_key。")
			return
		}
		ownerID := resolveUserID(c)
		if payload.Global {
			if !isAdminRequest(c) {
				respondError(c, http.StatusForbidden, "model_profile_forbidden", "只有管理员可以创建全局模型", "使用 ADMIN_TOKEN 调用，或创建用户自己的模型配置。")
				return
			}
			ownerID = ""
		}
		if err := validateModelBaseURL(c.Request.Context(), payload.BaseURL); err != nil {
			respondError(c, http.StatusBadRequest, "ssrf_blocked", err.Error(), "使用可公开访问的 https 地址；本地调试可设置 ALLOW_PRIVATE_MODEL_ENDPOINTS=true。")
			return
		}
		profile, err := store.Create(ownerID, profileInput(payload), envIntDefault("MODEL_PROFILE_MAX_PER_USER", 10))
		if err != nil {
			respondModelProfileError(c, err)
			return
		}
		invalidateModelCaches(ownerID)
		metrics.RecordModelProfileChange("create")
		audit.Record("model_profile_created", resolveUserID(c), profile.ID, map[string]any{"global": profile.OwnerID == "", "model": profile.Model})
		c.JSON(http.StatusCreated, profileResponse(profile))
	})

	apiGroup.PUT("/model-profiles/:id", func(c *gin.Context) {
		var payload modelProfilePayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, http.StatusBadRequest, "model_profile_invalid", "请求体格式不正确", "检查字段格式。")
			return
		}
		ownerID := resolveUserID(c)
		existing, ok := store.GetRaw(c.Param("id"))
		if !ok || (existing.OwnerID != "" && existing.OwnerID != ownerID) {
			respondModelProfileError(c, modelprofile.ErrNotFound)
			return
		}
		if existing.OwnerID == "" && !isAdminRequest(c) {
			respondModelProfileError(c, modelprofile.ErrForbidden)
			return
		}
		if strings.TrimSpace(payload.BaseURL) != "" {
			if err := validateModelBaseURL(c.Request.Context(), payload.BaseURL); err != nil {
				respondError(c, http.StatusBadRequest, "ssrf_blocked", err.Error(), "使用可公开访问的 https 地址。")
				return
			}
		}
		profile, err := store.Update(ownerID, existing.ID, profileInput(payload))
		if err != nil {
			respondModelProfileError(c, err)
			return
		}
		invalidateModelCaches(existing.OwnerID)
		metrics.RecordModelProfileChange("update")
		audit.Record("model_profile_updated", ownerID, profile.ID, map[string]any{"revision": profile.Revision})
		c.JSON(http.StatusOK, profileResponse(profile))
	})

	apiGroup.DELETE("/model-profiles/:id", func(c *gin.Context) {
		ownerID := resolveUserID(c)
		existing, ok := store.GetRaw(c.Param("id"))
		if !ok || (existing.OwnerID != "" && existing.OwnerID != ownerID) {
			respondModelProfileError(c, modelprofile.ErrNotFound)
			return
		}
		if existing.OwnerID == "" && !isAdminRequest(c) {
			respondModelProfileError(c, modelprofile.ErrForbidden)
			return
		}
		if err := store.Delete(ownerID, existing.ID); err != nil {
			respondModelProfileError(c, err)
			return
		}
		invalidateModelCaches(existing.OwnerID)
		metrics.RecordModelProfileChange("delete")
		audit.Record("model_profile_deleted", ownerID, existing.ID, nil)
		c.JSON(http.StatusOK, gin.H{"deleted": true})
	})

	apiGroup.POST("/model-profiles/:id/test", func(c *gin.Context) {
		ownerID := resolveUserID(c)
		profile, ok := store.GetRaw(c.Param("id"))
		if !ok || (profile.OwnerID != "" && profile.OwnerID != ownerID) {
			respondModelProfileError(c, modelprofile.ErrNotFound)
			return
		}
		secret, err := store.Secret(profile.ID)
		if err != nil || strings.TrimSpace(secret) == "" {
			respondError(c, http.StatusBadRequest, "model_probe_failed", "无法读取该配置的密钥", "重新保存 API Key 后再试。")
			return
		}
		if !limiter.allow("test:" + profile.ID) {
			respondError(c, http.StatusTooManyRequests, "rate_limited", "测试过于频繁", "等待几秒后再试。")
			return
		}
		result := modelprofile.ProbeText(c.Request.Context(), *profile, secret, oaichat.AllowPrivateFromEnv())
		status := http.StatusOK
		if !result.OK {
			status = http.StatusBadGateway
		}
		c.JSON(status, gin.H{"ok": result.OK, "status": result.Status, "detail": result.Detail, "latency_ms": result.LatencyMS, "model": result.Model})
	})

	apiGroup.POST("/model-profiles/:id/probe-vision", func(c *gin.Context) {
		ownerID := resolveUserID(c)
		profile, ok := store.GetRaw(c.Param("id"))
		if !ok || (profile.OwnerID != "" && profile.OwnerID != ownerID) {
			respondModelProfileError(c, modelprofile.ErrNotFound)
			return
		}
		secret, err := store.Secret(profile.ID)
		if err != nil || strings.TrimSpace(secret) == "" {
			respondError(c, http.StatusBadRequest, "model_probe_failed", "无法读取该配置的密钥", "重新保存 API Key 后再试。")
			return
		}
		if !limiter.allow("probe:" + profile.ID) {
			respondError(c, http.StatusTooManyRequests, "rate_limited", "探测过于频繁", "等待几秒后再试。")
			return
		}
		result := modelprofile.ProbeVision(c.Request.Context(), *profile, secret, oaichat.AllowPrivateFromEnv())
		store.SetSupportsVision(profile.ID, result.Status, result.Detail, time.Now())
		invalidateModelCaches(profile.OwnerID)
		status := http.StatusOK
		if result.Status == modelprofile.VisionNo {
			status = http.StatusOK
		}
		c.JSON(status, gin.H{"status": result.Status, "detail": result.Detail, "latency_ms": result.LatencyMS, "model": result.Model})
	})

	apiGroup.GET("/model-routing", func(c *gin.Context) {
		ownerID := resolveUserID(c)
		routing := store.GetRouting(ownerID)
		c.JSON(http.StatusOK, gin.H{"routing": routing, "global_routing": store.GetRouting("")})
	})

	apiGroup.PUT("/model-routing", func(c *gin.Context) {
		var payload modelRoutingPayload
		if err := c.ShouldBindJSON(&payload); err != nil {
			respondError(c, http.StatusBadRequest, "model_profile_invalid", "请求体格式不正确", "提供 main_profile_id/vision_profile_ids/fallback_profile_ids。")
			return
		}
		ownerID := resolveUserID(c)
		routing, err := store.SaveRouting(ownerID, payload.MainProfileID, payload.VisionProfileIDs, payload.FallbackProfileIDs)
		if err != nil {
			respondModelProfileError(c, err)
			return
		}
		invalidateModelCaches(ownerID)
		audit.Record("model_routing_updated", ownerID, routing.MainProfileID, nil)
		c.JSON(http.StatusOK, gin.H{"routing": routing})
	})
}

func profileInput(payload modelProfilePayload) modelprofile.Input {
	return modelprofile.Input{
		Name:                 payload.Name,
		Provider:             payload.Provider,
		BaseURL:              payload.BaseURL,
		Model:                payload.Model,
		APIKey:               payload.APIKey,
		Roles:                payload.Roles,
		SupportsVision:       payload.SupportsVision,
		TimeoutSeconds:       payload.TimeoutSeconds,
		MaxOutputTokens:      payload.MaxOutputTokens,
		ContextWindowTokens:  payload.ContextWindowTokens,
		PromptPricePer1K:     payload.Price(),
		CompletionPricePer1K: payload.CompletionPrice(),
	}
}

func (p modelProfilePayload) Price() float64 { return p.PromptPricePer1K }

func (p modelProfilePayload) CompletionPrice() float64 { return p.CompletionPricePer1K }

func profileResponse(profile modelprofile.Profile) gin.H {
	payload := gin.H{
		"id":                      profile.ID,
		"owner_id":                profile.OwnerID,
		"global":                  profile.OwnerID == "",
		"name":                    profile.Name,
		"provider":                profile.Provider,
		"base_url":                profile.BaseURL,
		"model":                   profile.Model,
		"api_key_masked":          profile.MaskedKey(),
		"api_key_last4":           profile.APIKeyLast4,
		"roles":                   profile.Roles,
		"supports_vision":         profile.SupportsVision,
		"vision_probe_reason":     profile.VisionProbeReason,
		"vision_probed_at":        profile.VisionProbedAt,
		"vision_overridden_at":    profile.VisionOverriddenAt,
		"timeout_seconds":         profile.TimeoutSeconds,
		"max_output_tokens":       profile.MaxOutputTokens,
		"context_window_tokens":   profile.ContextWindowTokens,
		"prompt_price_per_1k":     profile.PromptPricePer1K,
		"completion_price_per_1k": profile.CompletionPricePer1K,
		"revision":                profile.Revision,
		"bootstrap":               profile.Bootstrap,
		"created_at":              profile.CreatedAt,
		"updated_at":              profile.UpdatedAt,
	}
	return payload
}

func validateModelBaseURL(ctx context.Context, raw string) error {
	if strings.TrimSpace(raw) == "" {
		return fmt.Errorf("base_url is required")
	}
	_, err := oaichat.ValidateEndpoint(ctx, raw, oaichat.AllowPrivateFromEnv())
	return err
}

func invalidateModelCaches(ownerID string) {
	if resolver := modelresolver.Default(); resolver != nil {
		resolver.InvalidateAll()
	}
	_ = ownerID
}

// modelProfileStore returns the process-wide store installed at startup.
func modelProfileStore() *modelprofile.Store {
	resolver := modelresolver.Default()
	if resolver == nil {
		return nil
	}
	return resolver.Store()
}
