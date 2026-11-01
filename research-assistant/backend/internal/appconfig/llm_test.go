package appconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigPathUsesRAConfigPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[llm]\nmodel = \"test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RA_CONFIG_PATH", path)
	got, err := resolveConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != path {
		t.Fatalf("resolveConfigPath() = %q, want %q", got, path)
	}
}

func TestResolveConfigPathRejectsMissingExplicitPath(t *testing.T) {
	t.Setenv("RA_CONFIG_PATH", filepath.Join(t.TempDir(), "missing.toml"))
	if _, err := resolveConfigPath(); err == nil {
		t.Fatalf("expected an error for a missing RA_CONFIG_PATH")
	}
}
