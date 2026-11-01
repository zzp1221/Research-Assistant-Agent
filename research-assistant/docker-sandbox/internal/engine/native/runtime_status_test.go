package native

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRuntimeStatus(t *testing.T) {
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "python")
	venvRoot := filepath.Join(root, "venvs")
	for _, name := range []string{"3.11.0", "3.12.0"} {
		if err := os.MkdirAll(filepath.Join(runtimeRoot, "python", name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"abc", "def"} {
		dir := filepath.Join(venvRoot, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "python.exe"), make([]byte, 1024), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	status := ScanRuntimeStatus(runtimeRoot, venvRoot)
	if !status.Supported || status.Provider != "native" {
		t.Fatalf("status = %#v", status)
	}
	if len(status.PythonVersions) != 2 || status.PythonVersions[0] != "3.11.0" {
		t.Fatalf("versions = %#v", status.PythonVersions)
	}
	if status.VenvCount != 2 {
		t.Fatalf("venv count = %d", status.VenvCount)
	}
	if status.DiskUsedMB <= 0 {
		t.Fatalf("disk = %v", status.DiskUsedMB)
	}
}
