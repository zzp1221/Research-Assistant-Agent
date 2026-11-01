package shell

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchRuntimeStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"supported":true,"provider":"native","python_versions":["3.11.0"],"venv_count":2,"disk_used_mb":12.5}`))
	}))
	defer server.Close()
	shell := &Shell{}
	shell.status.SandboxURL = server.URL
	shell.sandboxTok = "token"
	status, err := shell.FetchRuntimeStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Supported || status.Provider != "native" || status.VenvCount != 2 || len(status.PythonVersions) != 1 {
		t.Fatalf("status = %#v", status)
	}
}
func TestInstallRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/runtime/install" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"supported":true,"provider":"native","python_versions":["3.11.0"],"venv_count":1,"disk_used_mb":10}`))
	}))
	defer server.Close()
	shell := &Shell{}
	shell.status.SandboxURL = server.URL
	shell.sandboxTok = "token"
	status, err := shell.InstallRuntime(context.Background(), "3.11")
	if err != nil {
		t.Fatal(err)
	}
	if !status.Supported || status.VenvCount != 1 {
		t.Fatalf("status = %#v", status)
	}
}
func TestCleanupRuntimes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/runtime/cleanup" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"supported":true,"provider":"native","venv_count":0,"disk_used_mb":0}`))
	}))
	defer server.Close()
	shell := &Shell{}
	shell.status.SandboxURL = server.URL
	status, err := shell.CleanupRuntimes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Supported || status.VenvCount != 0 {
		t.Fatalf("status = %#v", status)
	}
}
