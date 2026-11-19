package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

func TestPrepareRuntimeUsesServiceImageCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name, provider       string
		images, requireImage bool
	}{
		{"native", "native", false, false},
		{"vm_without_images", "vm", false, false},
		{"vm_with_images", "vm", true, true},
		{"docker", "docker", true, true},
		{"docker_false_cannot_bypass", "docker", false, true},
		{"remote_false_cannot_bypass", "remote", false, true},
		{"legacy_unknown", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			images, creates, capabilities := 0, 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/capabilities":
					capabilities++
					_ = json.NewEncoder(w).Encode(sandbox.SandboxCapabilities{Provider: tc.provider, Images: tc.images})
				case "/api/v1/images/ensure":
					images++
					_ = json.NewEncoder(w).Encode(provenanceTestImage())
				case "/api/v1/sandboxes":
					creates++
					var request sandbox.SandboxCreateRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
					}
					if tc.requireImage && request.Image != provenanceTestImage().Image {
						t.Errorf("canonical image was lost: %s", request.Image)
					}
					if !tc.requireImage && request.Image != "" {
						t.Error("non-image provider received a fake OCI identity")
					}
					_ = json.NewEncoder(w).Encode(sandbox.SandboxCreateResponse{SandboxID: "nt-ready"})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			coder := &CoderAgent{Sandbox: sandbox.NewSandboxClient(server.URL)}
			task := &models.Task{ID: "capabilities", Type: "prepare_runtime", AssignedTo: "sandbox_agent", Metadata: map[string]any{"sandbox_provider": "native", "images": false, "runtime_capabilities": `{"provider":"native","images":false}`}}
			if err := coder.ExecuteTask(context.Background(), task, nil); err != nil || task.Status != models.StatusCompleted {
				t.Fatalf("status=%s err=%v", task.Status, err)
			}
			if capabilities != 1 || creates != 1 || (images == 1) != tc.requireImage || images > 1 {
				t.Fatalf("caps=%d images=%d creates=%d", capabilities, images, creates)
			}
			_, provenance := task.Metadata["runtime_image_provenance"]
			if provenance != tc.requireImage {
				t.Fatalf("runtime provenance present=%t images=%t", provenance, tc.requireImage)
			}
			if !tc.requireImage && task.Metadata["artifact_values"].(map[string]string)["runtime_image"] != "" {
				t.Fatal("native session published an OCI image")
			}
		})
	}
}

func TestPrepareRuntimeCapabilitiesFailureCannotBypassImagePolicy(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/capabilities" {
			t.Errorf("unverified capability reached %s", r.URL.Path)
		}
		http.Error(w, "capabilities unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	task := &models.Task{Type: "prepare_runtime", AssignedTo: "sandbox_agent", Metadata: map[string]any{"sandbox_provider": "native", "images": false}}
	if err := (&CoderAgent{Sandbox: sandbox.NewSandboxClient(server.URL)}).ExecuteTask(context.Background(), task, nil); err == nil || task.Status != models.StatusFailed || calls != 1 {
		t.Fatalf("untrusted fallback: status=%s calls=%d err=%v", task.Status, calls, err)
	}
}

func TestNativeRuntimeCannotSatisfyFrozenImageRequirement(t *testing.T) {
	for _, requirement := range []string{`{"base_image":"python:3.11-bullseye"}`, `{"image_digest":"sha256:frozen"}`} {
		t.Run(requirement, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/v1/capabilities" {
					t.Errorf("image requirement reached side effect: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(`{"provider":"native","images":false}`))
			}))
			defer server.Close()
			task := &models.Task{Type: "prepare_runtime", AssignedTo: "sandbox_agent", Inputs: map[string]any{"reproduction_runtime_spec": requirement}}
			err := (&CoderAgent{Sandbox: sandbox.NewSandboxClient(server.URL)}).ExecuteTask(context.Background(), task, nil)
			if err != nil || task.Status != models.StatusBlocked || calls != 1 || !strings.Contains(task.Error, "OCI image requirement") {
				t.Fatalf("frozen identity silently discarded: %#v err=%v calls=%d", task, err, calls)
			}
		})
	}
}
