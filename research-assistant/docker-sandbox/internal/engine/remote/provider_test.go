package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"docker-sandbox/internal/engine"
)

func TestRemoteProviderLifecycle(t *testing.T) {
	var deleted string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/sandboxes":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "abc"})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/v1/sandboxes/"):
			deleted = strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/")
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/commands"):
			_ = json.NewEncoder(w).Encode(map[string]any{"stdout": "hello", "stderr": "", "exit_code": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider, err := NewProvider(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	session, err := provider.Create(context.Background(), engine.CreateSpec{Image: "python:3.11-bullseye"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if session.ID != "os-abc" || session.Provider != "remote" {
		t.Fatalf("session = %#v", session)
	}
	result, err := provider.Exec(context.Background(), session.ID, engine.Command{Argv: []string{"python", "--version"}}, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.Stdout != "hello" || result.ExitCode != 0 {
		t.Fatalf("result = %#v", result)
	}
	if err := provider.Delete(context.Background(), session.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if deleted != "abc" {
		t.Fatalf("deleted = %q", deleted)
	}
}

func TestNewProviderRequiresURL(t *testing.T) {
	if _, err := NewProvider(" "); err == nil {
		t.Fatal("empty URL should fail")
	}
}

func TestCapabilities(t *testing.T) {
	provider, _ := NewProvider("http://127.0.0.1:1")
	caps, err := provider.Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if caps.Provider != "remote" || caps.Isolation != "remote" || !caps.Images {
		t.Fatalf("caps = %#v", caps)
	}
}
