package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"research-assistant-backend/internal/audit"

	"github.com/gin-gonic/gin"
)

func TestAuditExportIsScopedToCaller(t *testing.T) {
	gin.SetMode(gin.TestMode)
	audit.Default().Reset()
	audit.Record("plan_created", "user-a", "plan-a", nil)
	audit.Record("plan_canceled", "user-b", "plan-b", nil)

	router := gin.New()
	router.GET("/api/audit/export", exportAuditEvents)

	request := httptest.NewRequest(http.MethodGet, "/api/audit/export?limit=10", nil)
	request.Header.Set(userIDHeaderName, "user-a")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Events []audit.Event `json:"events"`
		Count  int           `json:"count"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Count != 1 || len(payload.Events) != 1 || payload.Events[0].Actor != "user-a" {
		t.Fatalf("payload=%#v", payload)
	}
	if payload.Events[0].Action != "plan_created" {
		t.Fatalf("action=%q", payload.Events[0].Action)
	}
}

func TestAuditExportJSONL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	audit.Default().Reset()
	audit.Record("upload_created", "user-jsonl", "upload-1", map[string]any{"size": 12})
	router := gin.New()
	router.GET("/api/audit/export", exportAuditEvents)
	request := httptest.NewRequest(http.MethodGet, "/api/audit/export?format=jsonl", nil)
	request.Header.Set(userIDHeaderName, "user-jsonl")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d", response.Code)
	}
	if !containsString(response.Body.String(), "upload_created") {
		t.Fatalf("body=%s", response.Body.String())
	}
}

func containsString(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
