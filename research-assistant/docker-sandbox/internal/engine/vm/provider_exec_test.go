package vm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"docker-sandbox/internal/engine"
)

func TestProviderExecWithFakeWSL(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-wsl.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'ARGV:%s\\n' \"$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(dir, "workspace")
	provider := NewProviderWithConfig(context.Background(), Config{
		Detection:                Detection{Available: true, DefaultDistribution: "TestDistro"},
		DataDir:                  dir,
		WorkspaceRoot:            dir,
		AllowUnsupportedPlatform: true,
		WSLPath:                  script,
	})
	session, err := provider.Create(context.Background(), engine.CreateSpec{MountPath: workspace})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(session.ID, "vm-") || session.Provider != "vm" {
		t.Fatalf("session = %#v", session)
	}
	result, err := provider.Exec(context.Background(), session.ID, engine.Command{
		Argv: []string{"python", "--version"}, Cwd: "/workspace",
	}, nil)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit = %d stderr=%s", result.ExitCode, result.Stderr)
	}
	for _, want := range []string{"ARGV:", "-d TestDistro", "--cd " + workspace, "python3 --version"} {
		if !strings.Contains(result.Stdout, want) {
			t.Fatalf("stdout %q missing %q", result.Stdout, want)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "data.bin"), make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	usage, err := provider.Usage(context.Background(), session.ID)
	if err != nil || usage.DiskUsedMB <= 0 {
		t.Fatalf("usage = %#v, %v", usage, err)
	}
	if err := provider.Delete(context.Background(), session.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace should be removed, stat err=%v", err)
	}
}
