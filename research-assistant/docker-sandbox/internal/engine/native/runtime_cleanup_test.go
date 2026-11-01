package native

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanRuntimeCache(t *testing.T) {
	root := t.TempDir()
	runtimeRoot := filepath.Join(root, "runtimes")
	venvRoot := filepath.Join(root, "venvs")
	if err := os.MkdirAll(filepath.Join(runtimeRoot, "python", "3.11.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(venvRoot, "abc"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(venvRoot, "abc", "python"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CleanRuntimeCache(runtimeRoot, venvRoot); err != nil {
		t.Fatalf("CleanRuntimeCache: %v", err)
	}
	for _, dir := range []string{runtimeRoot, venvRoot} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s not empty: %v", dir, entries)
		}
	}
}

func TestCleanupRuntimeCacheRejectsInstall(t *testing.T) {
	provider := &Provider{installing: true}
	if _, err := provider.CleanupRuntimeCache(context.Background()); err == nil {
		t.Fatal("expected install-in-progress error")
	}
}
