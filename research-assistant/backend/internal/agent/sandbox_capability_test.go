package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

func TestCoderCapabilityRejectionBlocksWithoutExecutionFailure(t *testing.T) {
	for _, kind := range []string{"prepare_runtime", "execute_code"} {
		for _, blocked := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/blocked=%t", kind, blocked), func(t *testing.T) {
				creates, executed := 0, 0
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
						executed++
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					creates++
					if blocked {
						w.WriteHeader(http.StatusConflict)
						_, _ = w.Write([]byte(`{"status":"blocked","code":"sandbox_capability","error":"sandbox_capability: OS isolation not verified","missing_requirements":[{"kind":"sandbox_capability","name":"native","detail":"OS isolation not verified","remediation":"verify required isolation"}]}`))
					} else {
						w.WriteHeader(http.StatusInternalServerError)
						_, _ = w.Write([]byte(`{"error":"runtime installation failed"}`))
					}
				}))
				defer server.Close()
				coder := &CoderAgent{Sandbox: sandbox.NewSandboxClient(server.URL)}
				task := &models.Task{ID: "blocked", Type: kind, AssignedTo: "sandbox_agent", Inputs: map[string]any{"generated_code": "print('must-not-execute')"}}
				err := coder.ExecuteTask(context.Background(), task, nil)
				if creates != 1 || executed != 0 {
					t.Fatalf("calls create=%d execute=%d", creates, executed)
				}
				if blocked {
					missing, ok := task.Metadata["missing_requirements"].([]models.MissingRequirement)
					if err != nil || task.Status != models.StatusBlocked || task.Failure != nil || !ok || len(missing) != 1 || missing[0].Kind != "sandbox_capability" || missing[0].Remediation == "" {
						t.Fatalf("capability rejection became execution failure: task=%#v err=%v", task, err)
					}
				} else if err == nil || task.Status != models.StatusFailed {
					t.Fatalf("ordinary failure changed: task=%#v err=%v", task, err)
				}
			})
		}
	}
}

func TestSandboxCapabilityBlockPreservesWrappedRequirement(t *testing.T) {
	missing := []models.MissingRequirement{{Kind: "sandbox_capability", Detail: "unverified", Remediation: "verify"}}
	err := fmt.Errorf("dependency replacement: %w", &sandbox.CapabilityError{Message: "unverified", MissingRequirements: missing})
	task := &models.Task{}
	if !applySandboxCapabilityBlock(task, err) || task.Status != models.StatusBlocked {
		t.Fatal("wrapped capability error lost")
	}
	missing[0].Detail = "changed"
	if task.Metadata["missing_requirements"].([]models.MissingRequirement)[0].Detail != "unverified" {
		t.Fatal("task retained mutable error metadata")
	}
}

func TestCoderExistingSessionCapabilityRejectionBlocksBeforeRepair(t *testing.T) {
	for _, kind := range []string{"execute_code", "runtime_validate"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v1/sandboxes/nt-existing/python/stream" && r.URL.Path != "/api/v1/sandboxes/nt-existing/commands/stream" {
					t.Errorf("unexpected operation: %s", r.URL.Path)
				}
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"status":"blocked","code":"sandbox_capability","error":"sandbox_capability: session isolation no longer verified","missing_requirements":[{"kind":"sandbox_capability","name":"native","detail":"session isolation no longer verified","remediation":"revalidate isolation"}]}`))
			}))
			defer server.Close()
			coder := &CoderAgent{Sandbox: sandbox.NewSandboxClient(server.URL)}
			task := &models.Task{Type: kind, AssignedTo: "sandbox_agent", Inputs: map[string]any{
				"generated_code": "print('must-not-run')", "prepared_runtime": "nt-existing", "reproduction_runtime_spec": `{"language":"python"}`,
			}}
			if err := coder.ExecuteTask(context.Background(), task, nil); err != nil || task.Status != models.StatusBlocked || task.Failure != nil || calls != 1 {
				t.Fatalf("session capability block was swallowed/retried: task=%#v calls=%d err=%v", task, calls, err)
			}
			missing, ok := task.Metadata["missing_requirements"].([]models.MissingRequirement)
			if !ok || len(missing) != 1 || missing[0].Kind != "sandbox_capability" {
				t.Fatal("session capability requirement lost")
			}
		})
	}
}
