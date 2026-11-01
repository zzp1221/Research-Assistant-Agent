// Package settings stores the desktop settings.json and builds the child
// process environment documented in the Windows client design.
package settings

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Settings is the persisted desktop configuration.
type Settings struct {
	Version             int             `json:"version"`
	OnboardingCompleted bool            `json:"onboarding_completed"`
	LLM                 LLMSettings     `json:"llm"`
	Sandbox             SandboxSettings `json:"sandbox"`
	UI                  UISettings      `json:"ui"`
	Update              UpdateSettings  `json:"update"`
}

type LLMSettings struct {
	BaseURL        string `json:"base_url"`
	Model          string `json:"model"`
	APIKeyRef      string `json:"api_key_ref"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type SandboxSettings struct {
	Provider       string `json:"provider"`
	NetworkDefault string `json:"network_default"`
	CPULimit       int    `json:"cpu_limit"`
	MemoryGB       int    `json:"memory_gb"`
	DiskGB         int    `json:"disk_gb"`
	WindowsRunner  string `json:"windows_runner"`
}

type UISettings struct {
	CloseToTray bool   `json:"close_to_tray"`
	Theme       string `json:"theme"`
}

type UpdateSettings struct {
	Channel  string `json:"channel"`
	Auto     bool   `json:"auto"`
	Endpoint string `json:"endpoint,omitempty"`
}

// Default returns the product defaults.
func Default() Settings {
	return Settings{
		Version: 1,
		LLM: LLMSettings{
			BaseURL: "https://api.deepseek.com/v1", Model: "deepseek-chat",
			APIKeyRef: "dpapi:default", TimeoutSeconds: 120,
		},
		Sandbox: SandboxSettings{Provider: "auto", NetworkDefault: "off", CPULimit: 2, MemoryGB: 4, DiskGB: 20, WindowsRunner: "auto"},
		UI:      UISettings{CloseToTray: true, Theme: "system"},
		Update:  UpdateSettings{Channel: "stable", Auto: true},
	}
}

// Load reads path, returning defaults when it does not exist.
func Load(path string) (Settings, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return Settings{}, err
	}
	cfg := Default()
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Settings{}, fmt.Errorf("decode settings: %w", err)
	}
	return cfg, nil
}

// Save writes settings atomically.
func Save(path string, cfg Settings) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Ports are the loopback ports assigned by the supervisor.
type Ports struct {
	Backend int
	Sandbox int
}

// Paths are the data directories injected into child processes.
type Paths struct {
	DataDir    string
	LogDir     string
	PlanStore  string
	UploadRoot string
	BlobStore  string
	Literature string
	Workspaces string
}

// DefaultPaths derives the documented data layout.
func DefaultPaths(dataDir, logDir string) Paths {
	return Paths{
		DataDir:    dataDir,
		LogDir:     logDir,
		PlanStore:  filepath.Join(dataDir, "plans"),
		UploadRoot: filepath.Join(dataDir, "uploads"),
		BlobStore:  filepath.Join(dataDir, "blobs"),
		Literature: filepath.Join(dataDir, "literature"),
		Workspaces: filepath.Join(dataDir, "workspaces"),
	}
}

// BackendEnv returns the environment injected into backend.exe.
func BackendEnv(cfg Settings, paths Paths, ports Ports, apiToken, sandboxToken string) []string {
	env := os.Environ()
	return append(env, []string{
		"APP_ADDR=127.0.0.1:" + itoa(ports.Backend),
		"SANDBOX_URL=http://127.0.0.1:" + itoa(ports.Sandbox),
		"SANDBOX_API_TOKEN=" + sandboxToken,
		"API_AUTH_TOKEN=" + apiToken,
		"REQUIRE_API_AUTH=true",
		"PLAN_STORE_PATH=" + paths.PlanStore,
		"UPLOAD_ROOT=" + paths.UploadRoot,
		"BLOB_STORE_PATH=" + paths.BlobStore,
		"LITERATURE_LIBRARY_DIR=" + paths.Literature,
		"SANDBOX_WORKSPACE_ROOTS=" + paths.Workspaces,
		"LOG_DIR=" + paths.LogDir,
		"APP_DATA_DIR=" + paths.DataDir,
		"OPENAI_BASE_URL=" + cfg.LLM.BaseURL,
		"OPENAI_MODEL_NAME=" + cfg.LLM.Model,
		"SANDBOX_PROVIDER=" + cfg.Sandbox.Provider,
		"NoDefaultCurrentDirectoryInExePath=1",
	}...)
}

// WithAPIKey appends the decrypted API key to a child environment. The key
// never reaches a file or a command line.
func WithAPIKey(env []string, apiKey string) []string {
	if strings.TrimSpace(apiKey) == "" {
		return env
	}
	return append(env, "OPENAI_API_KEY="+apiKey)
}

// SandboxEnv returns the environment injected into sandbox.exe.
func SandboxEnv(cfg Settings, paths Paths, ports Ports, sandboxToken string) []string {
	env := os.Environ()
	return append(env, []string{
		"SANDBOX_LISTEN_ADDR=127.0.0.1:" + itoa(ports.Sandbox),
		"SANDBOX_API_TOKEN=" + sandboxToken,
		"SANDBOX_PROVIDER=" + cfg.Sandbox.Provider,
		"SANDBOX_WORKSPACE_ROOTS=" + paths.Workspaces,
		"SANDBOX_DEFAULT_IMAGE=",
		"SANDBOX_WINDOWS_RUNNER=" + cfg.Sandbox.WindowsRunner,
		"SANDBOX_MEMORY_MB=" + itoa(cfg.Sandbox.MemoryGB*1024),
		"SANDBOX_CPU_CORES=" + itoa(cfg.Sandbox.CPULimit),
		"SANDBOX_MAX_PROCESSES=64",
		"LOG_DIR=" + paths.LogDir,
		"APP_DATA_DIR=" + paths.DataDir,
		"NoDefaultCurrentDirectoryInExePath=1",
	}...)
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := []byte{}
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
