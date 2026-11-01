package diagnostics

import (
	"archive/zip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"research-assistant-desktop/settings"
)

func TestTailTruncatesFromEnd(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.log")
	if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := Tail(path, 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "6789" {
		t.Fatalf("Tail = %q, want 6789", string(data))
	}
	missing, err := Tail(filepath.Join(dir, "missing.log"), 10)
	if err != nil || missing != nil {
		t.Fatalf("missing tail = %v, %v", missing, err)
	}
}

func TestRedactSettingsMasksSecrets(t *testing.T) {
	cfg := settings.Default()
	cfg.LLM.APIKeyRef = "dpapi:default"
	redacted := RedactSettings(cfg)
	llm, ok := redacted["llm"].(map[string]any)
	if !ok {
		t.Fatalf("llm missing: %#v", redacted)
	}
	if llm["api_key_ref"] != "dpapi:default" {
		t.Fatalf("api_key_ref should not be redacted: %#v", llm["api_key_ref"])
	}
	raw, _ := json.Marshal(redacted)
	if strings.Contains(string(raw), "sk-") {
		t.Fatalf("redacted settings contains an API key: %s", raw)
	}
}

func TestBuildWritesExpectedEntries(t *testing.T) {
	dir := t.TempDir()
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(logDir, "shell.log"), []byte("shell log line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", 6<<20)
	if err := os.WriteFile(filepath.Join(logDir, "backend.log"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/plans") {
			_, _ = w.Write([]byte(`{"plans":[{"id":"p1","title":"failed plan","status":"failed","failed_nodes":2}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()
	sandbox := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true,"provider":"native"}`))
	}))
	defer sandbox.Close()

	cfg := settings.Default()
	path, err := Build(context.Background(), Options{
		AppVersion: "1.2.3", DataDir: dir, LogDir: logDir, Settings: cfg,
		BackendURL: backend.URL, SandboxURL: sandbox.URL, MaxLogBytes: 5 << 20,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer reader.Close()
	entries := map[string]*zip.File{}
	for _, file := range reader.File {
		entries[file.Name] = file
	}
	for _, name := range []string{"manifest.json", "settings.redacted.json", "health.json", "system.json", "failed-plans.json", "logs/shell.log", "logs/backend.log"} {
		if entries[name] == nil {
			t.Fatalf("missing entry %s (got %v)", name, entries)
		}
	}
	backendLog, _ := entries["logs/backend.log"].Open()
	data, _ := io.ReadAll(backendLog)
	_ = backendLog.Close()
	if int64(len(data)) > 5<<20 {
		t.Fatalf("backend log tail = %d bytes, want <= 5MB", len(data))
	}
	planFile, _ := entries["failed-plans.json"].Open()
	planRaw, _ := io.ReadAll(planFile)
	_ = planFile.Close()
	if !strings.Contains(string(planRaw), "failed plan") {
		t.Fatalf("failed plans missing: %s", planRaw)
	}
}
func TestCollectCrashFiles(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	files := []struct {
		name     string
		modified time.Time
	}{
		{"old.dmp", now.Add(-4 * time.Hour)},
		{"middle.dmp", now.Add(-3 * time.Hour)},
		{"new.dmp", now.Add(-2 * time.Hour)},
		{"other.txt", now.Add(-1 * time.Hour)},
	}
	for _, item := range files {
		path := filepath.Join(dir, item.name)
		if err := os.WriteFile(path, []byte("dump"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, item.modified, item.modified); err != nil {
			t.Fatal(err)
		}
	}
	metadata, names, err := collectCrashFiles(dir, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata) != 3 || len(names) != 3 {
		t.Fatalf("metadata = %#v names = %#v", metadata, names)
	}
	want := []string{"new.dmp", "middle.dmp", "old.dmp"}
	for index := range want {
		if names[index] != want[index] {
			t.Fatalf("names = %#v, want %#v", names, want)
		}
	}
}

func TestBuildIncludesCrashDumps(t *testing.T) {
	dir := t.TempDir()
	crashDir := filepath.Join(dir, "crashes")
	if err := os.MkdirAll(crashDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(crashDir, "app.dmp"), []byte("dump-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := Build(context.Background(), Options{
		AppVersion: "test", DataDir: dir, LogDir: filepath.Join(dir, "logs"),
		Settings: settings.Default(), CrashDir: crashDir,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	if !names["crashes.json"] || !names["crashes/app.dmp"] {
		t.Fatalf("missing crash entries: %#v", names)
	}
}
