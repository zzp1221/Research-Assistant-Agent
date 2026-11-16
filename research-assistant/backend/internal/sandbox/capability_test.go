package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const capabilityResponse = `{"status":"blocked","code":"sandbox_capability","error":"sandbox_capability: isolation not verified","missing_requirements":[{"kind":"sandbox_capability","name":"native","detail":"isolation not verified","remediation":"verify isolation or select another provider"}]}`

func TestSandboxClientPreservesStructuredCapabilityRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(capabilityResponse))
	}))
	defer server.Close()
	_, err := NewSandboxClient(server.URL).CreatePersistentSandbox(context.Background(), SandboxIdentity{TaskID: "blocked"}, "", "")
	var capability *CapabilityError
	if !errors.As(fmt.Errorf("outer context: %w", err), &capability) || len(capability.MissingRequirements) != 1 || capability.MissingRequirements[0].Name != "native" {
		t.Fatalf("structured reason lost through transport/wrapping: %v", err)
	}
}

func TestSandboxCreateErrorsDoNotInferCapabilitiesFromText(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"ordinary_failure", http.StatusInternalServerError, `{"error":"sandbox_capability: text in installation log"}`},
		{"wrong_status", http.StatusInternalServerError, capabilityResponse},
		{"wrong_code", http.StatusConflict, `{"status":"blocked","code":"network_control_failed","error":"network unavailable"}`},
		{"missing_requirements", http.StatusConflict, `{"status":"blocked","code":"sandbox_capability","error":"unverified"}`},
		{"malformed", http.StatusConflict, `{"status":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := sandboxCreateError(tc.status, []byte(tc.body))
			var capability *CapabilityError
			if err == nil || errors.As(err, &capability) {
				t.Fatalf("unrelated/malformed error reclassified: %v", err)
			}
		})
	}
}

func TestSandboxExecutionClientsPreserveCapabilityRejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(capabilityResponse))
	}))
	defer server.Close()
	client := NewSandboxClient(server.URL)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func() (*PythonRunResponse, error)
	}{
		{"python", func() (*PythonRunResponse, error) { return client.RunPythonCode(ctx, "nt-existing", "pass") }},
		{"command", func() (*PythonRunResponse, error) { return client.ExecCommand(ctx, "nt-existing", []string{"python"}) }},
		{"command_v2", func() (*PythonRunResponse, error) {
			return client.ExecCommandV2(ctx, "nt-existing", Command{Argv: []string{"python"}})
		}},
		{"python_stream", func() (*PythonRunResponse, error) { return client.RunPythonCodeStream(ctx, "nt-existing", "pass", nil) }},
		{"command_stream", func() (*PythonRunResponse, error) {
			return client.ExecCommandStream(ctx, "nt-existing", []string{"python"}, nil)
		}},
		{"command_stream_v2", func() (*PythonRunResponse, error) {
			return client.ExecCommandStreamV2(ctx, "nt-existing", Command{Argv: []string{"python"}}, nil)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.call()
			var capability *CapabilityError
			if result != nil || !errors.As(err, &capability) || len(capability.MissingRequirements) != 1 {
				t.Fatalf("execution capability rejection lost: result=%#v err=%v", result, err)
			}
		})
	}
}
