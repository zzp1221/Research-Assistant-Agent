package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"docker-sandbox/internal/engine"

	"github.com/gin-gonic/gin"
)

func TestInvalidImageConfigurationBlocksDockerProvider(t *testing.T) {
	t.Setenv("SANDBOX_PROVIDER", "docker")
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "registry.example/research/")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	service := NewSandboxService(Config{})
	if service.provider != "docker" || !strings.Contains(service.providerError, "image_policy_invalid") {
		t.Fatalf("invalid startup configuration was not rejected: provider=%s error=%s", service.provider, service.providerError)
	}
}

func TestNativeProviderDoesNotConsumeContainerImagePolicy(t *testing.T) {
	t.Setenv("SANDBOX_PROVIDER", "native")
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "invalid://mirror")
	service := NewSandboxService(Config{})
	if strings.Contains(service.providerError, "image_policy_invalid") {
		t.Fatalf("native execution was blocked by unused image configuration: %s", service.providerError)
	}
}

func TestCreateImageRejectionCannotReachFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:3.11-bullseye")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	t.Setenv("SANDBOX_ALLOW_UNLABELED_FALLBACK", "true")
	calls := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"must-not-create"}`))
	}))
	defer remote.Close()
	service := &SandboxService{provider: "docker", dkEngine: engine.NewNativeDockerEngine(), osEngine: engine.NewOpenSandboxEngine(remote.URL), config: Config{EnableFallback: true}}
	router := gin.New()
	router.POST("/sandboxes", service.CreateSandbox)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"image":"evil.example/python:3.11-bullseye","task_id":"policy-test"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "image_not_allowed") || calls != 0 {
		t.Fatalf("policy bypass: status=%d calls=%d body=%s", recorder.Code, calls, recorder.Body.String())
	}
}

func TestEnsureImagePolicyFailureIsBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "")
	service := &SandboxService{dkEngine: engine.NewNativeDockerEngine()}
	router := gin.New()
	router.POST("/images/ensure", service.EnsureImage)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/images/ensure", strings.NewReader(`{"image":"python:3.11-bullseye"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "image_policy_invalid") {
		t.Fatalf("bad image configuration response: %d %s", recorder.Code, recorder.Body.String())
	}
}
