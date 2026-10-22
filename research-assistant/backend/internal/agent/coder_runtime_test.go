package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSandboxImageForWorkspaceUsesPyprojectPythonRequirement(t *testing.T) {
	workspace := t.TempDir()
	pyproject := `[project]
name = "runtime-probe"
requires-python = ">=3.11,<3.14"
`
	if err := os.WriteFile(filepath.Join(workspace, "pyproject.toml"), []byte(pyproject), 0o644); err != nil {
		t.Fatal(err)
	}

	image, reason := sandboxImageForWorkspace("python:3.9-bullseye", workspace)
	if image != "python:3.11-bullseye" {
		t.Fatalf("image=%q, want python:3.11-bullseye", image)
	}
	if reason == "" {
		t.Fatal("expected repository runtime selection reason")
	}
}

func TestSandboxImageForWorkspaceDoesNotDowngradeConfiguredImage(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(workspace, "pyproject.toml"),
		[]byte("[project]\nrequires-python = \">=3.10\"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	image, _ := sandboxImageForWorkspace("python:3.12-bookworm", workspace)
	if image != "python:3.12-bookworm" {
		t.Fatalf("configured image was unexpectedly changed: %q", image)
	}
}

func TestSandboxImageForWorkspaceIgnoresInvalidMetadata(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "pyproject.toml"), []byte("not toml = ["), 0o644); err != nil {
		t.Fatal(err)
	}

	image, reason := sandboxImageForWorkspace("python:3.9-bullseye", workspace)
	if image != "python:3.9-bullseye" || reason != "" {
		t.Fatalf("invalid metadata changed runtime: image=%q reason=%q", image, reason)
	}
}

func TestPipInstallArgsUsesConfiguredIndex(t *testing.T) {
	t.Setenv("PIP_INDEX_URL", "https://mirrors.example/simple/")
	args := pipInstallArgs("numpy==2.1.3")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--index-url https://mirrors.example/simple/") || !strings.HasSuffix(joined, "numpy==2.1.3") {
		t.Fatalf("unexpected pip args: %s", joined)
	}
}

func TestMinimumSupportedPythonMinor(t *testing.T) {
	tests := map[string]int{
		">=3.11,<3.14": 11,
		"~=3.10":       10,
		"==3.12.*":     12,
		">3.9":         10,
		"<3.13":        0,
		"":             0,
	}
	for constraint, expected := range tests {
		if actual := minimumSupportedPythonMinor(constraint); actual != expected {
			t.Fatalf("minimumSupportedPythonMinor(%q)=%d, want %d", constraint, actual, expected)
		}
	}
}
