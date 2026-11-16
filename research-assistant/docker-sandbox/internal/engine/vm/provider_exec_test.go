package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"docker-sandbox/internal/engine"
)

func TestMain(m *testing.M) {
	if os.Getenv("RA_VM_TEST_FIXTURE") == "1" {
		fmt.Println("ARGV:" + strings.Join(os.Args[1:], " "))
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestProviderExecWithFakeWSL(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fake-wsl")
	if runtime.GOOS == "windows" {
		script += ".exe"
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script, data, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RA_VM_TEST_FIXTURE", "1")
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
	workspaceWSL, err := ToWSLPath(workspace)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ARGV:", "-d TestDistro", "--cd " + workspaceWSL, "python3 --version"} {
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
