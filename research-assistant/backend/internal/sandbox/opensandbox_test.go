package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSandboxClientSendsInternalBearerTokenAndIdentity(t *testing.T) {
	t.Setenv("SANDBOX_API_TOKEN", "internal-secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer internal-secret" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		var created SandboxCreateRequest
		if err := json.NewDecoder(request.Body).Decode(&created); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if created.TaskID != "task-1" || created.TenantID != "tenant-1" || created.PlanID != "plan-1" {
			http.Error(w, "missing sandbox identity", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SandboxCreateResponse{SandboxID: "dk-test"})
	}))
	defer server.Close()

	client := NewSandboxClient(server.URL)
	id, err := client.CreatePersistentSandbox(context.Background(), SandboxIdentity{
		TenantID: "tenant-1",
		TaskID:   "task-1",
		PlanID:   "plan-1",
	}, "python:3.11", "")
	if err != nil {
		t.Fatal(err)
	}
	if id != "dk-test" {
		t.Fatalf("sandbox id=%q", id)
	}
}
