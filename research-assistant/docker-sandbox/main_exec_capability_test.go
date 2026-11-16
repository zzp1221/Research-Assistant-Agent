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

type capabilityExecProvider struct {
	engine.Provider
	err   error
	calls int
	argv  []string
}

func (p *capabilityExecProvider) Exec(_ context.Context, _ string, command engine.Command, emit func(engine.Event) error) (*engine.Result, error) {
	p.calls++
	p.argv = command.Argv
	if p.err != nil {
		return nil, p.err
	}
	if emit != nil {
		_ = emit(engine.Event{Type: "chunk", Stream: "stdout", Message: "output"})
	}
	return &engine.Result{Stdout: "output", ExitCode: 0}, nil
}

func TestProviderExecCapabilityRejectionBeforeAnyOutputIsBlocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, route := range []string{"python", "command", "python/stream", "command/stream"} {
		t.Run(route, func(t *testing.T) {
			p := &capabilityExecProvider{err: fmt.Errorf("sandbox_capability: runtime changed; revalidation required")}
			s := &SandboxService{provider: "native", nativeProvider: p}
			r := gin.New()
			r.POST("/sandboxes/:id/python", s.ExecutePython)
			r.POST("/sandboxes/:id/command", s.ExecuteCommand)
			r.POST("/sandboxes/:id/python/stream", s.ExecutePythonStream)
			r.POST("/sandboxes/:id/command/stream", s.ExecuteCommandStream)
			body := `{"code":"print(1)","argv":["python","-c","print(1)"]}`
			req := httptest.NewRequest(http.MethodPost, "/sandboxes/nt-session/"+route, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			var result struct {
				Status, Code string
				Missing      []json.RawMessage `json:"missing_requirements"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusConflict || !strings.Contains(w.Header().Get("Content-Type"), "application/json") || p.calls != 1 || result.Status != "blocked" || result.Code != "sandbox_capability" || len(result.Missing) != 1 {
				t.Fatalf("execution capability rejection lost its contract: status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestNativePythonStreamStillStreamsSuccessfulExecution(t *testing.T) {
	p := &capabilityExecProvider{}
	s := &SandboxService{provider: "native", nativeProvider: p}
	r := gin.New()
	r.POST("/sandboxes/:id/python/stream", s.ExecutePythonStream)
	req := httptest.NewRequest(http.MethodPost, "/sandboxes/nt-session/python/stream", strings.NewReader(`{"code":"print(1)"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || p.calls != 1 || len(p.argv) != 3 || p.argv[0] != "python" || !strings.Contains(w.Body.String(), `"type":"chunk"`) || !strings.Contains(w.Body.String(), `"type":"final"`) {
		t.Fatalf("native Python stream did not use the runner: status=%d body=%s", w.Code, w.Body.String())
	}
}
