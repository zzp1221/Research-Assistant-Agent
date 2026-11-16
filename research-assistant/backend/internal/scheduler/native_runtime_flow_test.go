package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"research-assistant-backend/internal/agent"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

func TestNativeRuntimeHTTPFlowThroughCoderAndRoutedExecutor(t *testing.T) {
	for _, blockAt := range []string{"", "create", "execute"} {
		t.Run("block="+blockAt, func(t *testing.T) {
			caps, creates, executes, pulls := 0, 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer synthetic-control-token" {
					t.Error("sandbox client did not retain service authentication")
				}
				block := func() {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"status":"blocked","code":"sandbox_capability","error":"sandbox_capability: isolation not verified","missing_requirements":[{"kind":"sandbox_capability","name":"native","detail":"isolation not verified","remediation":"revalidate isolation or choose a suitable provider"}]}`))
				}
				switch r.URL.Path {
				case "/api/v1/capabilities":
					caps++
					_, _ = w.Write([]byte(`{"provider":"native","images":false,"isolation":"appcontainer","network_enforced":"os"}`))
				case "/api/v1/images/ensure":
					pulls++
					w.WriteHeader(http.StatusInternalServerError)
				case "/api/v1/sandboxes":
					creates++
					var request sandbox.SandboxCreateRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if request.Image != "" || request.TaskID != "prepare" || request.TenantID != "synthetic-owner" || request.NetworkMode != "none" {
						t.Errorf("unexpected native contract: %#v", request)
					}
					if blockAt == "create" {
						block()
						return
					}
					_, _ = w.Write([]byte(`{"sandbox_id":"nt-controlled"}`))
				case "/api/v1/sandboxes/nt-controlled/python/stream":
					executes++
					if blockAt == "execute" {
						block()
						return
					}
					var request sandbox.PythonRunRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					stdout := "controlled-result"
					if strings.Contains(request.Code, "ast.parse") {
						stdout = "syntax_ok"
					}
					_, _ = fmt.Fprintf(w, "{\"type\":\"final\",\"response\":{\"exit_code\":0,\"stdout\":%q}}\n", stdout)
				default:
					t.Errorf("unexpected side effect: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := sandbox.NewSandboxClient(server.URL)
			client.Token = "synthetic-control-token"
			executor := NewRoutedTaskExecutor(nil, nil, &agent.CoderAgent{Sandbox: client})
			prepare := &models.TaskNode{ID: "prepare", Type: "prepare_runtime", AssignedTo: "sandbox_agent", OutputArtifacts: []string{"runtime_session", "runtime_image"}}
			plan := &models.PlanGraph{ID: "controlled-plan", OwnerID: "synthetic-owner", Nodes: []*models.TaskNode{prepare}, Artifacts: map[string]models.Artifact{}}
			result, err := executor.ExecuteTask(context.Background(), plan, prepare)
			if err != nil || result == nil {
				t.Fatalf("prepare: result=%#v err=%v", result, err)
			}
			if blockAt == "create" {
				if result.Status != models.StatusBlocked || len(result.MissingRequirements) != 1 || executes != 0 {
					t.Fatalf("create block lost: %#v executions=%d", result, executes)
				}
			} else {
				if result.Status != models.StatusCompleted {
					t.Fatalf("native prepare failed: %#v", result)
				}
				prepare.Status = models.StatusCompleted
				for _, artifact := range result.Artifacts {
					plan.Artifacts[artifact.Key] = artifact
				}
				if plan.Artifacts["runtime_session"].Value != "nt-controlled" || plan.Artifacts["runtime_image"].Value != "" {
					t.Fatalf("native artifacts misidentified: %#v", plan.Artifacts)
				}
				execute := &models.TaskNode{ID: "execute", Type: "execute_code", AssignedTo: "sandbox_agent", RequiredArtifacts: []string{"runtime_session"}, Inputs: map[string]any{"generated_code": "print('controlled-result')"}}
				result, err = executor.ExecuteTask(context.Background(), plan, execute)
				if err != nil || result == nil {
					t.Fatalf("execute: result=%#v err=%v", result, err)
				}
				if blockAt == "execute" {
					if result.Status != models.StatusBlocked || len(result.MissingRequirements) != 1 || executes != 1 {
						t.Fatalf("execution block retried/lost: %#v executions=%d", result, executes)
					}
				} else if result.Status != models.StatusCompleted || result.Result != "controlled-result" || executes != 2 {
					t.Fatalf("native flow failed: %#v executions=%d", result, executes)
				}
			}
			if caps != 1 || creates != 1 || pulls != 0 || result.Failure != nil {
				t.Fatalf("flow boundary mismatch: caps=%d creates=%d pulls=%d failure=%#v", caps, creates, pulls, result.Failure)
			}
		})
	}
}
