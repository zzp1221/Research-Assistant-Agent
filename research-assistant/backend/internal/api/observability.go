package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/audit"
	"research-assistant-backend/internal/metrics"

	"github.com/gin-gonic/gin"
)

// metricsMiddleware records request counts/latency. The route template is used
// instead of the raw path so plan ids do not explode label cardinality.
func metricsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		path := c.FullPath()
		if strings.TrimSpace(path) == "" {
			path = "unmatched"
		}
		metrics.RecordHTTPRequest(c.Request.Method, path, c.Writer.Status(), time.Since(started))
	}
}

func registerObservabilityRoutes(engine *gin.Engine, apiGroup *gin.RouterGroup) {
	_ = audit.Default().Configure(strings.TrimSpace(os.Getenv("AUDIT_LOG_PATH")))
	apiGroup.GET("/metrics", gin.WrapH(metrics.Handler()))
	// Prometheus conventionally scrapes /metrics. Require the same bearer token
	// as the API so metrics are never public by default.
	engine.GET("/metrics", APIAuthMiddleware(), gin.WrapH(metrics.Handler()))
	apiGroup.GET("/audit/export", exportAuditEvents)
}

func recordAuditEvent(action, actor, target string, detail map[string]any) {
	audit.Record(action, actor, target, detail)
}

func exportAuditEvents(c *gin.Context) {
	limit := 500
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 && value <= 5000 {
			limit = value
		}
	}
	actor := resolveUserID(c)
	// Operators configure AUDIT_EXPORT_TOKEN to inspect every principal; without
	// it the export is scoped to the caller so users cannot read others' activity.
	if expected := strings.TrimSpace(os.Getenv("AUDIT_EXPORT_TOKEN")); expected != "" {
		provided := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if provided == expected {
			actor = ""
		}
	}
	events := audit.Default().Events(limit, actor)
	if strings.EqualFold(strings.TrimSpace(c.Query("format")), "jsonl") {
		c.Header("Content-Type", "application/x-ndjson")
		c.Header("Content-Disposition", "attachment; filename=audit.jsonl")
		for _, event := range events {
			encoded, err := json.Marshal(event)
			if err != nil {
				continue
			}
			_, _ = c.Writer.Write(append(encoded, '\n'))
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"events":   events,
		"count":    len(events),
		"log_path": audit.Default().Path(),
	})
}
