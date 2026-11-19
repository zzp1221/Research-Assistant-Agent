package scheduler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"research-assistant-backend/internal/agent"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

func TestRoutedExecutorPreservesSandboxCapabilityBlockedResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/capabilities" {
			_ = json.NewEncoder(w).Encode(sandbox.SandboxCapabilities{Provider: "docker", Images: true})
			return
		}
		if r.URL.Path == "/api/v1/images/ensure" {
			_ = json.NewEncoder(w).Encode(sandbox.SandboxImageInfo{Image: "python:3.11-bullseye"})
			return
		}
		if r.URL.Path != "/api/v1/sandboxes" {
			t.Errorf("unexpected execution after block: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"status":"blocked","code":"sandbox_capability","error":"sandbox_capability: isolation not verified","missing_requirements":[{"kind":"sandbox_capability","name":"native","detail":"isolation not verified","remediation":"verify isolation or change provider"}]}`))
	}))
	defer server.Close()
	coder := &agent.CoderAgent{Sandbox: sandbox.NewSandboxClient(server.URL)}
	executor := NewRoutedTaskExecutor(nil, nil, coder)
	plan := &models.PlanGraph{ID: "blocked-plan", OwnerID: "test-owner"}
	node := &models.TaskNode{ID: "blocked-task", Type: "prepare_runtime", AssignedTo: "sandbox_agent"}
	result, err := executor.ExecuteTask(context.Background(), plan, node)
	if err != nil || result == nil || result.Status != models.StatusBlocked || result.Failure != nil || len(result.MissingRequirements) != 1 || result.MissingRequirements[0].Kind != "sandbox_capability" || result.MissingRequirements[0].Remediation == "" {
		t.Fatalf("blocked contract lost between service/client/agent/executor: result=%#v err=%v", result, err)
	}
}
