package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	cfg := Default()
	cfg.LLM.Model = "deepseek-reasoner"
	cfg.Sandbox.Provider = "native"
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LLM.Model != "deepseek-reasoner" || loaded.Sandbox.Provider != "native" {
		t.Fatalf("loaded = %#v", loaded)
	}
}

func TestEnvInjectionIncludesContract(t *testing.T) {
	cfg := Default()
	paths := DefaultPaths("/data", "/data/logs")
	backend := strings.Join(BackendEnv(cfg, paths, Ports{Backend: 1234, Sandbox: 5678}, "api-token", "sandbox-token"), "\n")
	for _, want := range []string{
		"APP_ADDR=127.0.0.1:1234",
		"SANDBOX_URL=http://127.0.0.1:5678",
		"SANDBOX_API_TOKEN=sandbox-token",
		"API_AUTH_TOKEN=api-token",
		"REQUIRE_API_AUTH=true",
		"PLAN_STORE_PATH=" + filepath.FromSlash("/data/plans"),
		"UPLOAD_ROOT=" + filepath.FromSlash("/data/uploads"),
		"SANDBOX_WORKSPACE_ROOTS=" + filepath.FromSlash("/data/workspaces"),
		"LOG_DIR=/data/logs",
		"OPENAI_BASE_URL=https://api.deepseek.com/v1",
		"OPENAI_MODEL_NAME=deepseek-chat",
		"NoDefaultCurrentDirectoryInExePath=1",
	} {
		if !strings.Contains(backend, want) {
			t.Fatalf("backend env missing %q: %s", want, backend)
		}
	}
	sandbox := strings.Join(SandboxEnv(cfg, paths, Ports{Backend: 1234, Sandbox: 5678}, "sandbox-token"), "\n")
	for _, want := range []string{"SANDBOX_LISTEN_ADDR=127.0.0.1:5678", "SANDBOX_API_TOKEN=sandbox-token", "SANDBOX_PROVIDER=auto", "SANDBOX_WINDOWS_RUNNER=auto"} {
		if !strings.Contains(sandbox, want) {
			t.Fatalf("sandbox env missing %q: %s", want, sandbox)
		}
	}
}

func TestLoadMissingReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.json"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 || cfg.Sandbox.Provider != "auto" {
		t.Fatalf("defaults = %#v", cfg)
	}
	if _, err := os.Stat(filepath.Join(t.TempDir(), "missing.json")); !os.IsNotExist(err) {
		t.Fatalf("Load should not create the file")
	}
}
