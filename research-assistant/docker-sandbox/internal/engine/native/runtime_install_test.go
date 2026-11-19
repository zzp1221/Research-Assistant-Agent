package native

import (
	"context"
	"path/filepath"
	"testing"
)

func TestInstallRuntimeWithFakeUV(t *testing.T) {
	dir := t.TempDir()
	uv := fixtureUV(t)
	manager := NewRuntimeManager(uv, filepath.Join(dir, "runtimes"), filepath.Join(dir, "venvs"), filepath.Join(dir, "logs"))
	provider := &Provider{runtimes: manager, sessions: map[string]*nativeSession{}}
	status, err := provider.InstallRuntime(context.Background(), "3.11")
	if err != nil {
		t.Fatalf("InstallRuntime: %v", err)
	}
	if !status.Supported || status.VenvCount != 1 {
		t.Fatalf("status = %#v", status)
	}
	if status.DiskUsedMB <= 0 {
		t.Fatalf("disk = %v", status.DiskUsedMB)
	}
}
func TestEnsureRuntimeProgressStages(t *testing.T) {
	dir := t.TempDir()
	uv := fixtureUV(t)
	manager := NewRuntimeManager(uv, filepath.Join(dir, "runtimes"), filepath.Join(dir, "venvs"), filepath.Join(dir, "logs"))
	stages := []string{}
	_, err := manager.EnsureRuntimeWithProgress(context.Background(), "3.11", []string{"six==1.0"}, func(stage string) {
		stages = append(stages, stage)
	})
	if err != nil {
		t.Fatalf("EnsureRuntimeWithProgress: %v", err)
	}
	for _, want := range []string{"python", "venv", "dependencies", "freeze", "done"} {
		found := false
		for _, stage := range stages {
			if stage == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("stages %v missing %q", stages, want)
		}
	}
}
