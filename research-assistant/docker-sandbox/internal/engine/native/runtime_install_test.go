package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallRuntimeWithFakeUV(t *testing.T) {
	dir := t.TempDir()
	uv := filepath.Join(dir, "fake-uv.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  python) exit 0 ;;\n" +
		"  venv)\n" +
		"    shift\n" +
		"    mkdir -p \"$1/bin\"\n" +
		"    printf '#!/bin/sh\\n' > \"$1/bin/python\"\n" +
		"    chmod +x \"$1/bin/python\"\n" +
		"    ;;\n" +
		"  pip)\n" +
		"    if [ \"$2\" = \"freeze\" ]; then echo 'six==1.0'; fi\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(uv, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
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
	uv := filepath.Join(dir, "fake-uv.sh")
	script := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  python) exit 0 ;;\n" +
		"  venv)\n" +
		"    shift\n" +
		"    mkdir -p \"$1/bin\"\n" +
		"    printf '#!/bin/sh\\n' > \"$1/bin/python\"\n" +
		"    chmod +x \"$1/bin/python\"\n" +
		"    ;;\n" +
		"  pip)\n" +
		"    if [ \"$2\" = \"freeze\" ]; then echo 'six==1.0'; fi\n" +
		"    ;;\n" +
		"esac\n" +
		"exit 0\n"
	if err := os.WriteFile(uv, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
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
