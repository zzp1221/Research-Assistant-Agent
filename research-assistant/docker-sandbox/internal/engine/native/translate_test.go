package native

import (
	"errors"
	"path/filepath"
	"testing"

	"docker-sandbox/internal/engine"
)

func TestTranslateCommand(t *testing.T) {
	workspace := t.TempDir()
	venvPython := filepath.Join(t.TempDir(), "python")
	command, err := TranslateCommand(workspace, venvPython, engine.Command{
		Argv: []string{"python", "/workspace/main.py", "--out", "/workspace/out"},
		Cwd:  "/workspace",
		Env:  map[string]string{"PYTHONPATH": "/workspace"},
	})
	if err != nil {
		t.Fatalf("TranslateCommand: %v", err)
	}
	if command.Argv[0] != venvPython {
		t.Fatalf("python was not mapped: %#v", command.Argv)
	}
	if command.Cwd != workspace {
		t.Fatalf("cwd = %q, want %q", command.Cwd, workspace)
	}
	if command.Env["PYTHONPATH"] != workspace {
		t.Fatalf("env = %#v", command.Env)
	}
	if command.Argv[1] != filepath.Join(workspace, "main.py") {
		t.Fatalf("path was not mapped: %#v", command.Argv)
	}
}

func TestTranslateCommandRejectsPOSIXShell(t *testing.T) {
	_, err := TranslateCommand(t.TempDir(), "python", engine.Command{Shell: true, Argv: []string{"sh", "-lc", "echo hi"}})
	if !errors.Is(err, ErrUnsupportedPOSIXShell) {
		t.Fatalf("err = %v, want ErrUnsupportedPOSIXShell", err)
	}
}
