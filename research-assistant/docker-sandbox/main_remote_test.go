package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRemoteProviderServiceLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	deleted := false
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "abc"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/commands"):
			_ = json.NewEncoder(w).Encode(map[string]any{"stdout": "Python 3.11.0", "stderr": "", "exit_code": 0})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/sandboxes/"):
			deleted = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()

	t.Setenv("SANDBOX_PROVIDER", "remote")
	t.Setenv("OPEN_SANDBOX_URL", mock.URL)
	service := NewSandboxService(Config{OpenSandboxURL: mock.URL})
	if service.provider != "remote" {
		t.Fatalf("provider = %q", service.provider)
	}
	if service.providerError != "" {
		t.Fatalf("providerError = %q", service.providerError)
	}

	router := gin.New()
	group := router.Group("/api/v1")
	group.GET("/health", service.Health)
	group.POST("/sandboxes", service.CreateSandbox)
	group.POST("/sandboxes/:id/commands", service.ExecuteCommand)
	group.DELETE("/sandboxes/:id", service.DeleteSandbox)

	healthRecorder := httptest.NewRecorder()
	healthRequest := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	router.ServeHTTP(healthRecorder, healthRequest)
	if healthRecorder.Code != http.StatusOK || !strings.Contains(healthRecorder.Body.String(), "\"provider\":\"remote\"") {
		t.Fatalf("health = %d %s", healthRecorder.Code, healthRecorder.Body.String())
	}

	createRecorder := httptest.NewRecorder()
	createRequest := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader("{\"image\":\"python:3.11-bullseye\",\"task_id\":\"remote-e2e\",\"tenant_id\":\"e2e\"}"))
	createRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(createRecorder, createRequest)
	if createRecorder.Code != http.StatusOK {
		t.Fatalf("create = %d %s", createRecorder.Code, createRecorder.Body.String())
	}
	var created struct {
		SandboxID string `json:"sandbox_id"`
	}
	_ = json.Unmarshal(createRecorder.Body.Bytes(), &created)
	if created.SandboxID != "os-abc" {
		t.Fatalf("sandbox id = %q", created.SandboxID)
	}

	execRecorder := httptest.NewRecorder()
	execRequest := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes/os-abc/commands", strings.NewReader("{\"argv\":[\"python\",\"--version\"]}"))
	execRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(execRecorder, execRequest)
	if execRecorder.Code != http.StatusOK || !strings.Contains(execRecorder.Body.String(), "Python 3.11.0") {
		t.Fatalf("exec = %d %s", execRecorder.Code, execRecorder.Body.String())
	}

	deleteRecorder := httptest.NewRecorder()
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/os-abc", nil)
	router.ServeHTTP(deleteRecorder, deleteRequest)
	if deleteRecorder.Code != http.StatusOK || !deleted {
		t.Fatalf("delete = %d deleted=%v", deleteRecorder.Code, deleted)
	}
}
