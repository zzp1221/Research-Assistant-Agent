package scheduler

import (
	"os"
	"path/filepath"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestValidateRuntimeTaskPathsRejectsWorkspaceOutsideRoots(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOTS", root)

	task := &models.Task{Inputs: map[string]any{
		"workspace_path": t.TempDir(),
	}}
	if err := validateRuntimeTaskPaths(task); err == nil {
		t.Fatal("expected workspace_path outside SANDBOX_WORKSPACE_ROOTS to be rejected")
	}
}

func TestValidateRuntimeTaskPathsAcceptsWorkspaceAndCodeFile(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOTS", root)
	workspace := filepath.Join(root, "run")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	codePath := filepath.Join(workspace, "main.py")
	if err := os.WriteFile(codePath, []byte("print('ok')\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	task := &models.Task{Inputs: map[string]any{
		"workspace_path": workspace,
		"code_file_path": codePath,
	}}
	if err := validateRuntimeTaskPaths(task); err != nil {
		t.Fatalf("expected workspace and code file to be accepted: %v", err)
	}
}

func TestValidateRuntimeTaskPathsRejectsUploadedFileOutsideRoot(t *testing.T) {
	uploadRoot := t.TempDir()
	t.Setenv("UPLOAD_ROOT", uploadRoot)
	outside := filepath.Join(t.TempDir(), "secret.csv")
	if err := os.WriteFile(outside, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	task := &models.Task{Inputs: map[string]any{
		"uploaded_files": []map[string]any{{
			"name":         "secret.csv",
			"storage_path": outside,
		}},
	}}
	if err := validateRuntimeTaskPaths(task); err == nil {
		t.Fatal("expected uploaded file outside UPLOAD_ROOT to be rejected")
	}
}

func TestValidateRuntimeTaskPathsRejectsCodeFileOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOTS", root)
	workspace := filepath.Join(root, "run")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.py")
	if err := os.WriteFile(outside, []byte("print('no')\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	task := &models.Task{Inputs: map[string]any{
		"workspace_path": workspace,
		"code_file_path": outside,
	}}
	if err := validateRuntimeTaskPaths(task); err == nil {
		t.Fatal("expected code_file_path outside workspace roots to be rejected")
	}
}
