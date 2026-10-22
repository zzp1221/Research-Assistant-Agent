package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateUploadedFilePathRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("UPLOAD_ROOT", root)
	inside := filepath.Join(root, "data.csv")
	if err := os.WriteFile(inside, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateUploadedFilePath(inside); err != nil {
		t.Fatalf("expected inside upload root to be accepted: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.csv")
	if err := os.WriteFile(outside, []byte("a,b\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateUploadedFilePath(outside); err == nil {
		t.Fatal("expected file outside upload root to be rejected")
	}
	link := filepath.Join(root, "link.csv")
	if err := os.Symlink(outside, link); err == nil {
		if _, err := ValidateUploadedFilePath(link); err == nil {
			t.Fatal("expected symlink upload file to be rejected")
		}
	}
}

func TestValidateWorkspacePathRejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOTS", root)
	inside := filepath.Join(root, "workspace")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorkspacePath(inside); err != nil {
		t.Fatalf("expected inside workspace root to be accepted: %v", err)
	}
	if _, err := ValidateWorkspacePath(t.TempDir()); err == nil {
		t.Fatal("expected workspace outside configured roots to be rejected")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(t.TempDir(), link); err == nil {
		if _, err := ValidateWorkspacePath(link); err == nil {
			t.Fatal("expected symlink workspace to be rejected")
		}
	}
}
