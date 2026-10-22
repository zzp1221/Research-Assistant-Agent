package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSandboxGatewayRequiresTokenAndForwardsRequests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("SANDBOX_API_TOKEN", "gateway-secret")

	forwarded := make(chan [2]string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded <- [2]string{r.URL.Path, r.Header.Get("Authorization")}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer upstream.Close()

	handler, err := newGatewayHandler(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/api/v1/health", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", health.Code, health.Body.String())
	}
	if healthRequest := <-forwarded; healthRequest[0] != "/api/v1/health" {
		t.Fatalf("health was not forwarded: %q", healthRequest[0])
	}

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader(`{}`)))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d body=%s", unauthorized.Code, unauthorized.Body.String())
	}

	authorizedRequest := httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader(`{}`))
	authorizedRequest.Header.Set("Authorization", "Bearer gateway-secret")
	authorized := httptest.NewRecorder()
	handler.ServeHTTP(authorized, authorizedRequest)
	if authorized.Code != http.StatusOK {
		t.Fatalf("authorized status=%d body=%s", authorized.Code, authorized.Body.String())
	}
	forwardedRequest := <-forwarded
	if forwardedRequest[0] != "/api/v1/sandboxes" {
		t.Fatalf("unexpected forwarded path: %q", forwardedRequest[0])
	}
	if forwardedRequest[1] != "Bearer gateway-secret" {
		t.Fatalf("authorization header was not forwarded: %q", forwardedRequest[1])
	}
}
