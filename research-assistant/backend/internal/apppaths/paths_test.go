package apppaths

import (
	"path/filepath"
	"testing"
)

func TestEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_DATA_DIR", dir)
	t.Setenv("LOG_DIR", filepath.Join(dir, "custom-logs"))
	t.Setenv("RA_CONFIG_PATH", filepath.Join(dir, "config.toml"))
	if got := DataDir(); got != dir {
		t.Fatalf("DataDir() = %q, want %q", got, dir)
	}
	if got := LogDir(); got != filepath.Join(dir, "custom-logs") {
		t.Fatalf("LogDir() = %q", got)
	}
	if got := ConfigPath(); got != filepath.Join(dir, "config.toml") {
		t.Fatalf("ConfigPath() = %q", got)
	}
}

func TestLogDirFallsBackToDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APP_DATA_DIR", dir)
	t.Setenv("LOG_DIR", "")
	if got := LogDir(); got != filepath.Join(dir, "logs") {
		t.Fatalf("LogDir() = %q, want %q", got, filepath.Join(dir, "logs"))
	}
}
