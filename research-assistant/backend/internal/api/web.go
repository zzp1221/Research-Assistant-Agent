package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/webread"

	"github.com/gin-gonic/gin"
)

var (
	webFetcherOnce    sync.Once
	webFetcher        *webread.Fetcher
	webFetcherFactory = defaultWebFetcher
)

func defaultWebFetcher() *webread.Fetcher {
	webFetcherOnce.Do(func() {
		webFetcher = webread.New(webread.Config{
			MaxBytes:      int64(envIntDefault("WEB_FETCH_MAX_MB", 2)) * 1024 * 1024,
			Timeout:       time.Duration(envIntDefault("WEB_FETCH_TIMEOUT_SECONDS", 15)) * time.Second,
			RespectRobots: true,
			CacheTTL:      time.Duration(envIntDefault("WEB_FETCH_CACHE_TTL_SECONDS", 3600)) * time.Second,
		})
	})
	return webFetcher
}

func getWebFetcher() *webread.Fetcher {
	return webFetcherFactory()
}

// RegisterWebRoutes wires the public web-page reader.
func RegisterWebRoutes(apiGroup *gin.RouterGroup) {
	apiGroup.POST("/web/fetch", fetchWebPage)
}

func fetchWebPage(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var payload struct {
		URL      string `json:"url" binding:"required"`
		MaxBytes int64  `json:"max_bytes,omitempty"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "url is required", "code": "invalid_url"})
		return
	}
	payload.URL = strings.TrimSpace(payload.URL)
	if payload.URL == "" || len(payload.URL) > 4096 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid url", "code": "invalid_url"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(envIntDefault("WEB_FETCH_TIMEOUT_SECONDS", 15))*time.Second)
	defer cancel()
	result, err := getWebFetcher().Fetch(ctx, payload.URL)
	if err != nil {
		status := http.StatusBadGateway
		code := "web_fetch_failed"
		switch {
		case webread.IsCode(err, webread.CodeInvalidURL):
			status = http.StatusBadRequest
			code = "invalid_url"
		case webread.IsCode(err, webread.CodeSSRFBlocked):
			status = http.StatusForbidden
			code = "ssrf_blocked"
		case webread.IsCode(err, webread.CodeRobotsDenied):
			status = http.StatusForbidden
			code = "robots_denied"
		case webread.IsCode(err, webread.CodeUnsupportedContentType):
			status = http.StatusUnsupportedMediaType
			code = "unsupported_content_type"
		case webread.IsCode(err, webread.CodeContentTooLarge):
			status = http.StatusRequestEntityTooLarge
			code = "content_too_large"
		case webread.IsCode(err, webread.CodeFetchTimeout):
			status = http.StatusGatewayTimeout
			code = "fetch_timeout"
		}
		c.JSON(status, gin.H{"error": err.Error(), "code": code, "remediation": "确认 URL 为公开网页；私网地址、登录页和动态渲染页面不支持。"})
		return
	}
	c.JSON(http.StatusOK, result)
}
