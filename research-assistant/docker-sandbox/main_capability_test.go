package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"docker-sandbox/internal/engine"

	"github.com/gin-gonic/gin"
)

type capabilityCreateProvider struct {
	engine.Provider
	err   error
	calls int
}

func (p *capabilityCreateProvider) Create(context.Context, engine.CreateSpec) (engine.Session, error) {
	p.calls++
	return engine.Session{ID: "nt-created"}, p.err
}

func TestProviderCreateCapabilityRejectionIsStructuredBlocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"capability", fmt.Errorf("sandbox_capability: AppContainer isolation is not verified"), http.StatusConflict},
		{"provider_failure", fmt.Errorf("runtime installation failed"), http.StatusInternalServerError},
		{"message_is_not_code", fmt.Errorf("installation failed while reading sandbox_capability: document"), http.StatusInternalServerError},
		{"success", nil, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &capabilityCreateProvider{err: tc.err}
			service := &SandboxService{provider: "native", nativeProvider: provider}
			router := gin.New()
			router.POST("/sandboxes", service.CreateSandbox)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/sandboxes", strings.NewReader(`{"task_id":"capability-test","network_mode":"none"}`))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			if recorder.Code != tc.status || provider.calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, provider.calls, recorder.Body.String())
			}
			var body struct {
				Status  string                                             `json:"status"`
				Code    string                                             `json:"code"`
				Error   string                                             `json:"error"`
				Missing []struct{ Kind, Name, Detail, Remediation string } `json:"missing_requirements"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if tc.status == http.StatusConflict {
				if body.Status != "blocked" || body.Code != "sandbox_capability" || body.Error != tc.err.Error() || len(body.Missing) != 1 || body.Missing[0].Kind != "sandbox_capability" || body.Missing[0].Name != "native" || body.Missing[0].Detail == "" || body.Missing[0].Remediation == "" {
					t.Fatalf("incomplete blocked response: %s", recorder.Body.String())
				}
			} else if body.Status != "" || body.Code != "" || len(body.Missing) != 0 {
				t.Fatalf("non-capability response was reclassified: %s", recorder.Body.String())
			}
		})
	}
}
