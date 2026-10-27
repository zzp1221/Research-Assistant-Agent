package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

func TestPrepareRuntimeRecordsImagePullFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"image_pull_failed: registry unavailable"}`, http.StatusBadGateway)
	}))
	defer server.Close()
	coder := &CoderAgent{Name: "coder_agent", Sandbox: sandbox.NewSandboxClient(server.URL)}
	task := &models.Task{
		Type:     "prepare_runtime",
		Name:     "prepare runtime",
		Inputs:   map[string]any{"workspace_path": t.TempDir()},
		Metadata: map[string]any{},
	}
	if err := coder.prepareRuntime(context.Background(), task); err == nil {
		t.Fatal("expected image preparation failure")
	}
	if task.Status != models.StatusFailed {
		t.Fatalf("status=%s", task.Status)
	}
	missing, ok := task.Metadata["missing_requirements"].([]models.MissingRequirement)
	if !ok || len(missing) != 1 || missing[0].Kind != "image_pull_failed" {
		t.Fatalf("missing=%#v", task.Metadata["missing_requirements"])
	}
	if missing[0].Remediation == "" {
		t.Fatal("missing requirement has no remediation")
	}
}

type recordingNetworkController struct {
	modes []string
	err   error
}

func (c *recordingNetworkController) SetSandboxNetwork(_ context.Context, _ string, mode string) error {
	c.modes = append(c.modes, mode)
	return c.err
}

func TestOpenSandboxNetworkClosesOnExit(t *testing.T) {
	controller := &recordingNetworkController{}
	closeFn, err := openSandboxNetwork(context.Background(), controller, "dk-abcdef", "bridge")
	if err != nil {
		t.Fatal(err)
	}
	closeFn()
	if len(controller.modes) != 2 || controller.modes[0] != "bridge" || controller.modes[1] != "none" {
		t.Fatalf("modes=%v", controller.modes)
	}
}

func TestOpenSandboxNetworkSkipsUnmanagedIDs(t *testing.T) {
	controller := &recordingNetworkController{}
	closeFn, err := openSandboxNetwork(context.Background(), controller, "os-external", "bridge")
	if err != nil {
		t.Fatal(err)
	}
	closeFn()
	if len(controller.modes) != 0 {
		t.Fatalf("unexpected network calls: %v", controller.modes)
	}
}

func TestTaskNeedsNetworkReadsApproval(t *testing.T) {
	if taskNeedsNetwork(&models.Task{}) {
		t.Fatal("task without metadata must not claim network access")
	}
	if !taskNeedsNetwork(&models.Task{Metadata: map[string]any{"network_access": true}}) {
		t.Fatal("approved network task must be detected")
	}
	if taskNeedsNetwork(&models.Task{Metadata: map[string]any{"network_access": false}}) {
		t.Fatal("unapproved task must stay offline")
	}
}
