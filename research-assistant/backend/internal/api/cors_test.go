package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestCORSAllowsIdempotencyKeyHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(CORSMiddleware())
	router.POST("/api/plan", func(c *gin.Context) { c.Status(http.StatusOK) })
	request := httptest.NewRequest(http.MethodOptions, "/api/plan", nil)
	request.Header.Set("Origin", "http://localhost:5173")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "Idempotency-Key")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("preflight status=%d", response.Code)
	}
	allowed := response.Header().Get("Access-Control-Allow-Headers")
	if !strings.Contains(allowed, "Idempotency-Key") {
		t.Fatalf("Access-Control-Allow-Headers=%q", allowed)
	}
	exposed := response.Header().Get("Access-Control-Expose-Headers")
	if !strings.Contains(exposed, "Retry-After") {
		t.Fatalf("Access-Control-Expose-Headers=%q", exposed)
	}
}
