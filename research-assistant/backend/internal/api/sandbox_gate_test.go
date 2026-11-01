package api

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

type fakeSandboxProbe struct {
	caps *sandbox.SandboxCapabilities
	err  error
}

func (f fakeSandboxProbe) Capabilities(context.Context) (*sandbox.SandboxCapabilities, error) {
	return f.caps, f.err
}

func TestAttachSandboxCapabilitiesConvertsImagePrepareOnNative(t *testing.T) {
	runtime := &planRuntime{sandbox: fakeSandboxProbe{caps: &sandbox.SandboxCapabilities{Provider: "native", Images: false, Isolation: "lowil"}}}
	plan := &models.PlanGraph{ID: "p1", Status: models.StatusPending, Nodes: []*models.TaskNode{{Type: "image_prepare", Name: "Prepare Runtime Image"}}}
	runtime.attachSandboxCapabilities(context.Background(), plan)
	if plan.Meta.SandboxCapabilities == nil || plan.Meta.SandboxCapabilities.Provider != "native" {
		t.Fatalf("snapshot = %#v", plan.Meta.SandboxCapabilities)
	}
	node := plan.Nodes[0]
	if node.Type != "runtime_probe" || node.Name != "Probe Runtime Capabilities" {
		t.Fatalf("node = %#v", node)
	}
	if len(node.OutputArtifacts) != 2 || node.OutputArtifacts[0] != "runtime_capabilities" {
		t.Fatalf("outputs = %#v", node.OutputArtifacts)
	}
	if len(plan.MissingRequirements) != 0 || plan.Status != models.StatusPending {
		t.Fatalf("plan should not be blocked: %#v status=%q", plan.MissingRequirements, plan.Status)
	}
}

func TestAttachSandboxCapabilitiesAllowsDockerImages(t *testing.T) {
	runtime := &planRuntime{sandbox: fakeSandboxProbe{caps: &sandbox.SandboxCapabilities{Provider: "docker", Images: true, Isolation: "container"}}}
	plan := &models.PlanGraph{ID: "p2", Status: models.StatusPending, Nodes: []*models.TaskNode{{Type: "image_prepare"}}}
	runtime.attachSandboxCapabilities(context.Background(), plan)
	if len(plan.MissingRequirements) != 0 {
		t.Fatalf("missing = %#v", plan.MissingRequirements)
	}
	if plan.Status != models.StatusPending {
		t.Fatalf("status = %q", plan.Status)
	}
}

func TestAttachSandboxCapabilitiesIgnoresProbeError(t *testing.T) {
	runtime := &planRuntime{sandbox: fakeSandboxProbe{err: errors.New("offline")}}
	plan := &models.PlanGraph{ID: "p3", Status: models.StatusPending, Nodes: []*models.TaskNode{{Type: "image_prepare"}}}
	runtime.attachSandboxCapabilities(context.Background(), plan)
	if len(plan.MissingRequirements) != 0 || plan.Meta.SandboxCapabilities != nil {
		t.Fatalf("plan mutated on probe failure: %#v", plan)
	}
}
func TestAttachSandboxCapabilitiesGPUAndPython(t *testing.T) {
	spec, _ := json.Marshal(map[string]any{"gpu_required": true, "gpu_type": "cuda", "python_version": "3.14", "system_packages": []string{"libgl1"}})
	plan := &models.PlanGraph{
		ID: "p-gpu", Status: models.StatusPending,
		Artifacts: map[string]models.Artifact{"reproduction_runtime_spec": {Value: string(spec)}},
	}
	runtime := &planRuntime{sandbox: fakeSandboxProbe{caps: &sandbox.SandboxCapabilities{
		Provider: "native", Images: false, GPU: false, GPURuntimeAvailable: false,
		PythonVersions: []string{"3.9", "3.11"},
	}}}
	runtime.attachSandboxCapabilities(context.Background(), plan)
	kinds := map[string]bool{}
	for _, missing := range plan.MissingRequirements {
		kinds[missing.Kind] = true
	}
	if !kinds["gpu_unavailable"] || !kinds["python_version_unavailable"] || !kinds["system_package_unavailable"] {
		t.Fatalf("missing = %#v", plan.MissingRequirements)
	}
	if plan.Status != models.StatusBlocked || plan.Outcome != models.OutcomeBlocked {
		t.Fatalf("status=%q outcome=%q", plan.Status, plan.Outcome)
	}
}

func TestPythonVersionAllowed(t *testing.T) {
	if !pythonVersionAllowed("3.11", []string{"3.9", "3.11"}) {
		t.Fatal("3.11 should be allowed")
	}
	if !pythonVersionAllowed("3.11.8", []string{"3.11"}) {
		t.Fatal("patch version should match major.minor")
	}
	if pythonVersionAllowed("3.14", []string{"3.9", "3.11"}) {
		t.Fatal("3.14 should not be allowed")
	}
}
