package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecureDockerRunArgsApplyResourceAndPrivilegeLimits(t *testing.T) {
	t.Setenv("SANDBOX_CPU_LIMIT", "1.5")
	t.Setenv("SANDBOX_MEMORY_LIMIT", "2g")
	t.Setenv("SANDBOX_PIDS_LIMIT", "128")
	t.Setenv("SANDBOX_NETWORK_MODE", "none")
	t.Setenv("SANDBOX_CONTAINER_USER", "65534:65534")
	t.Setenv("SANDBOX_READ_ONLY_ROOT", "true")

	args, err := secureDockerRunArgs("python:3.11-slim", map[string]string{
		tenantLabelKey: "tenant-a",
		taskLabelKey:   "task-a",
		planLabelKey:   "plan-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{
		"--cpus 1.5", "--memory 2g", "--pids-limit 128",
		"--security-opt no-new-privileges:true", "--cap-drop ALL",
		"--network research-assistant-offline", "--user 65534:65534", "--read-only", "--workdir /workspace",
		"--label research-assistant.sandbox=true",
		"--label research-assistant.tenant-id=tenant-a",
		"--label research-assistant.task-id=task-a",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("expected %q in docker args: %s", expected, joined)
		}
	}
}

func TestSandboxImageAllowlist(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:3.11,registry.example/research/")
	if _, err := secureDockerRunArgs("python:3.11-slim", nil); err != nil {
		t.Fatalf("expected allowlisted image: %v", err)
	}
	if _, err := secureDockerRunArgs("ubuntu:latest", nil); err == nil {
		t.Fatal("expected image outside allowlist to be rejected")
	}
}

func TestNormalizeDockerMountPathRejectsOutsideWorkspaceRoots(t *testing.T) {
	root := t.TempDir()
	t.Setenv("SANDBOX_WORKSPACE_ROOTS", root)
	inside := filepath.Join(root, "run-1")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	expectedInside, err := filepath.EvalSymlinks(inside)
	if err != nil {
		t.Fatal(err)
	}
	if normalized, err := normalizeDockerMountPath(inside); err != nil || normalized != filepath.Clean(expectedInside) {
		t.Fatalf("expected workspace path to be accepted, got %q, %v", normalized, err)
	}
	if _, err := normalizeDockerMountPath(filepath.Dir(root)); err == nil {
		t.Fatal("expected parent directory mount to be rejected")
	}
	outside := t.TempDir()
	symlink := filepath.Join(root, "linked-outside")
	if err := os.Symlink(outside, symlink); err == nil {
		if _, err := normalizeDockerMountPath(symlink); err == nil {
			t.Fatal("expected symlink escaping workspace root to be rejected")
		}
	}
}
