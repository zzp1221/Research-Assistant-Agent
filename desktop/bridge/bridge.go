// Package bridge exposes desktop shell capabilities to the desktop frontend.
// The same methods are bound by the MyGo entrypoint (mygo.Bind) and the Wails
// fallback (window.go.main.API).
package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"research-assistant-desktop/onboarding"
	"research-assistant-desktop/settings"
	"research-assistant-desktop/shell"
	"research-assistant-desktop/updater"
)

// Credentials mirrors the onboarding form payload.
type Credentials struct {
	BaseURL  string `json:"baseUrl"`
	Model    string `json:"model"`
	APIKey   string `json:"apiKey"`
	Provider string `json:"provider,omitempty"`
}

// ConnectionResult is returned to the onboarding "test connection" button.
type ConnectionResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// RuntimeSettingsView is the subset of settings the runtime panel edits.
type RuntimeSettingsView struct {
	Provider       string `json:"provider"`
	NetworkDefault string `json:"network_default"`
	WindowsRunner  string `json:"windows_runner"`
}

// API is bound into the desktop shell: mygo.Bind(api) in the MyGo entrypoint,
// window.go.main.API in the Wails fallback.
type API struct {
	shell           *shell.Shell
	settingsPath    string
	dataDir         string
	logDir          string
	secretStore     settings.SecretStore
	openLogs        func(string) error
	chooseDirectory func(string) (string, error)
	showInFolder    func(string) error
	updates         *updater.Manager
	notify          func(string, string)
	client          *onboarding.Client
}

// New builds the desktop bridge.
func New(desktopShell *shell.Shell, settingsPath, dataDir, logDir string, secretStore settings.SecretStore, openLogs func(string) error, chooseDirectory func(string) (string, error), showInFolder func(string) error, updates *updater.Manager, notify func(string, string)) *API {
	return &API{
		shell:           desktopShell,
		settingsPath:    settingsPath,
		dataDir:         dataDir,
		logDir:          logDir,
		secretStore:     secretStore,
		openLogs:        openLogs,
		chooseDirectory: chooseDirectory,
		showInFolder:    showInFolder,
		updates:         updates,
		notify:          notify,
		client:          &onboarding.Client{},
	}
}

// NeedsOnboarding reports whether the first-run wizard should be shown.
func (a *API) NeedsOnboarding() bool {
	cfg, err := settings.Load(a.settingsPath)
	if err != nil {
		return true
	}
	return !cfg.OnboardingCompleted
}

// GetRuntimeSettings reads the persisted provider preferences.
func (a *API) GetRuntimeSettings() (RuntimeSettingsView, error) {
	cfg, err := settings.Load(a.settingsPath)
	if err != nil {
		return RuntimeSettingsView{}, err
	}
	return RuntimeSettingsView{
		Provider:       cfg.Sandbox.Provider,
		NetworkDefault: cfg.Sandbox.NetworkDefault,
		WindowsRunner:  cfg.Sandbox.WindowsRunner,
	}, nil
}

// UpdateProvider persists the sandbox provider preference and restarts the
// child services so SANDBOX_PROVIDER takes effect.
func (a *API) UpdateProvider(provider string) error {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch provider {
	case "auto", "native", "docker", "vm", "remote":
	default:
		return fmt.Errorf("invalid sandbox provider %q", provider)
	}
	cfg, err := settings.Load(a.settingsPath)
	if err != nil {
		return err
	}
	cfg.Sandbox.Provider = provider
	if err := settings.Save(a.settingsPath, cfg); err != nil {
		return err
	}
	if a.shell != nil && a.shell.Status().State == shell.StateRunning {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := a.shell.Restart(ctx); err != nil {
			return fmt.Errorf("restart services: %w", err)
		}
	}
	return nil
}

// InstallRuntime asks the sandbox to prepare a Python runtime.
func (a *API) InstallRuntime(version string) (shell.RuntimeStatusView, error) {
	if a.shell == nil {
		return shell.RuntimeStatusView{}, fmt.Errorf("desktop shell is not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	return a.shell.InstallRuntime(ctx, version)
}

// CleanupRuntimes clears the sandbox runtime/venv cache.
func (a *API) CleanupRuntimes() (shell.RuntimeStatusView, error) {
	if a.shell == nil {
		return shell.RuntimeStatusView{}, fmt.Errorf("desktop shell is not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return a.shell.CleanupRuntimes(ctx)
}

// CompleteOnboarding persists credentials and restarts the services so the new
// LLM configuration takes effect.
func (a *API) CompleteOnboarding(creds Credentials) error {
	converted := onboarding.Credentials{BaseURL: creds.BaseURL, Model: creds.Model, APIKey: creds.APIKey}
	if err := onboarding.Validate(converted); err != nil {
		return err
	}
	cfg, err := settings.Load(a.settingsPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(creds.Provider) != "" {
		cfg.Sandbox.Provider = strings.TrimSpace(creds.Provider)
	}
	if err := onboarding.Complete(a.settingsPath, converted, cfg); err != nil {
		return err
	}
	if a.secretStore != nil {
		ref := strings.TrimPrefix(strings.TrimSpace(cfg.LLM.APIKeyRef), "dpapi:")
		if ref == "" {
			ref = "default"
		}
		if err := a.secretStore.Save(ref, []byte(strings.TrimSpace(creds.APIKey))); err != nil {
			return fmt.Errorf("save api key: %w", err)
		}
	}
	if a.shell != nil && a.shell.Status().State == shell.StateRunning {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if err := a.shell.Restart(ctx); err != nil {
			return fmt.Errorf("restart services: %w", err)
		}
	}
	return nil
}

// TestConnection runs a minimal chat completion against the supplied endpoint.
func (a *API) TestConnection(creds Credentials) ConnectionResult {
	err := a.client.TestConnection(context.Background(), onboarding.Credentials{
		BaseURL: creds.BaseURL, Model: creds.Model, APIKey: creds.APIKey,
	})
	if err != nil {
		return ConnectionResult{OK: false, Error: err.Error()}
	}
	return ConnectionResult{OK: true}
}

// ChooseDirectory opens the native folder picker. Browser mode returns an
// empty string instead of failing.
func (a *API) ChooseDirectory(title string) (string, error) {
	if a.chooseDirectory == nil {
		return "", nil
	}
	return a.chooseDirectory(title)
}

// ShowInFolder reveals path in Explorer.
func (a *API) ShowInFolder(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("path is required")
	}
	if a.showInFolder == nil {
		return fmt.Errorf("show in folder is not supported")
	}
	return a.showInFolder(path)
}

// CheckForUpdate reports whether a newer signed build is available.
func (a *API) CheckForUpdate() (updater.UpdateStatus, error) {
	if a.updates == nil {
		return updater.UpdateStatus{}, fmt.Errorf("update manager is not configured")
	}
	return a.updates.Check(context.Background())
}

// DownloadUpdate downloads and verifies the latest (or requested) update.
func (a *API) DownloadUpdate(version string) (updater.UpdateStatus, error) {
	if a.updates == nil {
		return updater.UpdateStatus{}, fmt.Errorf("update manager is not configured")
	}
	return a.updates.Download(context.Background(), version)
}

// InstallUpdate launches a previously downloaded and verified installer.
func (a *API) InstallUpdate(path string) error {
	if a.updates == nil {
		return fmt.Errorf("update manager is not configured")
	}
	return a.updates.Install(path)
}

// Notify forwards a desktop notification to the shell runtime.
func (a *API) Notify(title, body string) {
	if a.notify != nil {
		a.notify(title, body)
	}
}

// ExportDiagnostics writes and returns a support bundle path.
func (a *API) ExportDiagnostics() (string, error) {
	if a.shell == nil {
		return "", fmt.Errorf("desktop shell is not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return a.shell.BuildDiagnostics(ctx)
}

// OpenLogs opens the log directory in Explorer.
func (a *API) OpenLogs() error {
	if a.openLogs == nil {
		return fmt.Errorf("open logs is not supported")
	}
	if strings.TrimSpace(a.logDir) == "" {
		return fmt.Errorf("log directory is not configured")
	}
	return a.openLogs(a.logDir)
}

// Retry restarts the child services after a failed start.
func (a *API) Retry() error {
	if a.shell == nil {
		return fmt.Errorf("desktop shell is not running")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	return a.shell.Restart(ctx)
}
