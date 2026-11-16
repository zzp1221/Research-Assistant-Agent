package main

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Capability rejection is an unmet execution requirement, not a retryable
// provider crash. Keep error for older clients while exposing the task shape.
func writeSandboxCapabilityError(c *gin.Context, err error, provider string) bool {
	if err == nil || !strings.HasPrefix(err.Error(), "sandbox_capability:") {
		return false
	}
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Content-Type", "application/json; charset=utf-8")
	c.JSON(http.StatusConflict, gin.H{
		"status": "blocked",
		"code":   "sandbox_capability",
		"error":  err.Error(),
		"missing_requirements": []gin.H{{
			"kind":        "sandbox_capability",
			"name":        provider,
			"detail":      err.Error(),
			"remediation": "验证所需的 AppContainer 与网络隔离能力后重试，或改用满足任务要求的 Docker/远程 provider。",
		}},
	})
	return true
}
