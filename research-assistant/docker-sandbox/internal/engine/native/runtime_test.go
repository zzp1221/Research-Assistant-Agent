package native

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvHashIsOrderIndependent(t *testing.T) {
	a := EnvHash("3.11", []string{"torch", "numpy"}, "windows-amd64")
	b := EnvHash("3.11", []string{"numpy", "torch"}, "windows-amd64")
	if a != b {
		t.Fatalf("EnvHash order changed: %s != %s", a, b)
	}
	if a == EnvHash("3.11", []string{"numpy"}, "windows-amd64") {
		t.Fatalf("EnvHash ignored requirements")
	}
}

func TestEnsureRuntimeUsesFakeUVAndReusesVenv(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "runtimes", ".ra-build", "uv.log")
	uvPath := fixtureUV(t)
	manager := NewRuntimeManager(uvPath, filepath.Join(dir, "runtimes"), filepath.Join(dir, "venvs"), filepath.Join(dir, "logs"))
	first, err := manager.EnsureRuntime(context.Background(), "3.11", []string{"numpy", "torch"})
	if err != nil {
		t.Fatalf("first EnsureRuntime: %v", err)
	}
	if first.Reused {
		t.Fatalf("first runtime should not be reused")
	}
	if _, err := os.Stat(first.PythonPath); err != nil {
		t.Fatalf("fake python was not created: %v", err)
	}
	raw, _ := os.ReadFile(logPath)
	if !strings.Contains(string(raw), "python install 3.11") || !strings.Contains(string(raw), "pip install") {
		t.Fatalf("unexpected uv log: %s", raw)
	}
	before := len(raw)
	second, err := manager.EnsureRuntime(context.Background(), "3.11", []string{"torch", "numpy"})
	if err != nil {
		t.Fatalf("second EnsureRuntime: %v", err)
	}
	if !second.Reused {
		t.Fatalf("second runtime should be reused")
	}
	after, _ := os.ReadFile(logPath)
	if len(after) != before {
		t.Fatalf("reuse should not call uv again, log grew: %s", after[before:])
	}
}
