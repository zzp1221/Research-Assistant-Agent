package bridge

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"research-assistant-desktop/settings"
)

func TestNeedsOnboarding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	api := New(nil, path, dir, dir, settings.NewMemorySecretStore(), nil, nil, nil, nil, nil)
	if !api.NeedsOnboarding() {
		t.Fatal("missing settings should require onboarding")
	}
	cfg := settings.Default()
	cfg.OnboardingCompleted = true
	if err := settings.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	if api.NeedsOnboarding() {
		t.Fatal("completed onboarding should not be required")
	}
}

func TestCompleteOnboardingPersists(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	store := settings.NewMemorySecretStore()
	api := New(nil, path, dir, dir, store, nil, nil, nil, nil, nil)
	err := api.CompleteOnboarding(Credentials{
		BaseURL: "https://api.example.com/v1", Model: "deepseek-chat", APIKey: "sk-secret", Provider: "native",
	})
	if err != nil {
		t.Fatalf("CompleteOnboarding: %v", err)
	}
	cfg, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OnboardingCompleted || cfg.LLM.Model != "deepseek-chat" || cfg.Sandbox.Provider != "native" {
		t.Fatalf("cfg = %#v", cfg)
	}
	value, err := store.Load("default")
	if err != nil || string(value) != "sk-secret" {
		t.Fatalf("secret = %q, %v", value, err)
	}
}

func TestTestConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	api := New(nil, filepath.Join(t.TempDir(), "settings.json"), t.TempDir(), t.TempDir(), settings.NewMemorySecretStore(), nil, nil, nil, nil, nil)
	result := api.TestConnection(Credentials{BaseURL: server.URL, Model: "m", APIKey: "k"})
	if !result.OK {
		t.Fatalf("result = %#v", result)
	}
}

func TestExportDiagnosticsRequiresShell(t *testing.T) {
	api := New(nil, filepath.Join(t.TempDir(), "settings.json"), t.TempDir(), t.TempDir(), settings.NewMemorySecretStore(), nil, nil, nil, nil, nil)
	if _, err := api.ExportDiagnostics(); err == nil {
		t.Fatal("expected error without a running shell")
	}
}
func TestChooseDirectoryAndShowInFolder(t *testing.T) {
	api := New(nil, filepath.Join(t.TempDir(), "settings.json"), t.TempDir(), t.TempDir(), settings.NewMemorySecretStore(),
		nil,
		func(title string) (string, error) { return "C:\\repo", nil },
		func(path string) error {
			if path != "C:\\repo" {
				t.Fatalf("show path = %q", path)
			}
			return nil
		},
		nil,
		nil,
	)
	path, err := api.ChooseDirectory("repo")
	if err != nil || path != "C:\\repo" {
		t.Fatalf("ChooseDirectory = %q, %v", path, err)
	}
	if err := api.ShowInFolder(path); err != nil {
		t.Fatalf("ShowInFolder: %v", err)
	}
	if err := api.ShowInFolder(""); err == nil {
		t.Fatal("empty path should fail")
	}
}
func TestUpdateMethodsRequireManager(t *testing.T) {
	api := New(nil, filepath.Join(t.TempDir(), "settings.json"), t.TempDir(), t.TempDir(), settings.NewMemorySecretStore(), nil, nil, nil, nil, nil)
	if _, err := api.CheckForUpdate(); err == nil {
		t.Fatal("CheckForUpdate should fail without a manager")
	}
	if _, err := api.DownloadUpdate(""); err == nil {
		t.Fatal("DownloadUpdate should fail without a manager")
	}
}
func TestRuntimeSettingsPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	api := New(nil, path, t.TempDir(), t.TempDir(), settings.NewMemorySecretStore(), nil, nil, nil, nil, nil)
	view, err := api.GetRuntimeSettings()
	if err != nil {
		t.Fatalf("GetRuntimeSettings: %v", err)
	}
	if view.Provider != "auto" {
		t.Fatalf("provider = %q", view.Provider)
	}
	if err := api.UpdateProvider("native"); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	cfg, err := settings.Load(path)
	if err != nil || cfg.Sandbox.Provider != "native" {
		t.Fatalf("saved provider = %q, %v", cfg.Sandbox.Provider, err)
	}
	if err := api.UpdateProvider("invalid"); err == nil {
		t.Fatal("invalid provider should fail")
	}
}
