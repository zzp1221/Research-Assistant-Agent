package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"research-assistant-backend/internal/models"
)

func reproductionSpecTask(t *testing.T, workspace, selected string) *models.Task {
	t.Helper()
	manifest, _ := json.Marshal(map[string]any{
		"workspace_path":     workspace,
		"selected_code_file": selected,
		"repro_entry_kind":   "repository_workspace",
	})
	return &models.Task{
		Type:   "reproduction_spec",
		Inputs: map[string]any{"repo_manifest": string(manifest)},
	}
}

func runReproductionSpec(t *testing.T, task *models.Task) models.ReproductionRuntimeSpec {
	t.Helper()
	agent := &ResearchCodingAgent{Name: "research_coding_agent"}
	if err := agent.executeReproductionSpec(t.Context(), task); err != nil {
		t.Fatalf("executeReproductionSpec: %v", err)
	}
	values, ok := task.Metadata["artifact_values"].(map[string]string)
	if !ok {
		t.Fatalf("missing artifact values: %#v", task.Metadata)
	}
	var spec models.ReproductionRuntimeSpec
	if err := json.Unmarshal([]byte(values["reproduction_runtime_spec"]), &spec); err != nil {
		t.Fatal(err)
	}
	return spec
}

func TestReproductionSpecResolvesSubdirectoryEntry(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(workspace, "src", "main.py")
	if err := os.WriteFile(entry, []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := runReproductionSpec(t, reproductionSpecTask(t, workspace, entry))
	if len(spec.RunCommand) != 2 || spec.RunCommand[1] != "src/main.py" {
		t.Fatalf("run_command=%v, want [python3 src/main.py]", spec.RunCommand)
	}
	if spec.EntrypointRelative != "src/main.py" {
		t.Fatalf("entrypoint_relative=%q", spec.EntrypointRelative)
	}
	if len(spec.PythonPath) != 1 || spec.PythonPath[0] != "/workspace/src" {
		t.Fatalf("python_path=%v", spec.PythonPath)
	}
}

func TestReproductionSpecKeepsRootEntryRelative(t *testing.T) {
	workspace := t.TempDir()
	entry := filepath.Join(workspace, "main.py")
	if err := os.WriteFile(entry, []byte("print('hi')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec := runReproductionSpec(t, reproductionSpecTask(t, workspace, entry))
	if len(spec.RunCommand) != 2 || spec.RunCommand[1] != "main.py" {
		t.Fatalf("run_command=%v", spec.RunCommand)
	}
	if len(spec.PythonPath) != 0 {
		t.Fatalf("python_path=%v, want empty for root entry", spec.PythonPath)
	}
}

func TestReproductionSpecRejectsEscapingAbsoluteEntry(t *testing.T) {
	workspace := t.TempDir()
	task := reproductionSpecTask(t, workspace, "/etc/passwd")
	agent := &ResearchCodingAgent{Name: "research_coding_agent"}
	if err := agent.executeReproductionSpec(t.Context(), task); err == nil {
		t.Fatal("absolute entry outside the workspace must be rejected")
	}
	if task.Status != models.StatusFailed {
		t.Fatalf("task status=%s, want failed", task.Status)
	}
}
