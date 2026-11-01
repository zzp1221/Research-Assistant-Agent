//go:build desktop && wails

package main

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"research-assistant-desktop/bridge"
	"research-assistant-desktop/crashdump"
	"research-assistant-desktop/paths"
	"research-assistant-desktop/settings"
	"research-assistant-desktop/shell"
	"research-assistant-desktop/tray"
	"research-assistant-desktop/updater"
)

//go:embed all:webui/dist
var webAssets embed.FS

func main() {
	dataDir := paths.DataDir()
	logDir := paths.LogDir()
	if err := paths.Ensure(logDir); err != nil {
		log.Printf("create log dir: %v", err)
	}
	if err := crashdump.Enable(filepath.Join(dataDir, "crashes")); err != nil {
		log.Printf("enable crash dumps: %v", err)
	}
	staticFS, err := fs.Sub(webAssets, "webui/dist")
	if err != nil {
		log.Fatalf("resolve embedded frontend: %v", err)
	}
	installDir := paths.InstallDir()
	settingsPath := filepath.Join(dataDir, "settings.json")
	var updateManager *updater.Manager
	if endpoint := strings.TrimSpace(os.Getenv("RA_UPDATE_ENDPOINT")); endpoint != "" {
		publicKey, err := updater.PublicKeyFromEnv()
		if err != nil {
			log.Printf("update public key: %v", err)
		} else {
			updateManager = &updater.Manager{
				Checker:     &updater.Checker{Endpoint: endpoint, CurrentVersion: appVersion()},
				Installer:   &updater.Installer{PublicKey: publicKey},
				DownloadDir: filepath.Join(dataDir, "updates"),
			}
		}
	}
	desktopShell := shell.New(shell.Config{
		AppVersion:          appVersion(),
		SettingsPath:        settingsPath,
		DataDir:             dataDir,
		LogDir:              logDir,
		BackendPath:         filepath.Join(installDir, "backend.exe"),
		SandboxPath:         filepath.Join(installDir, "sandbox.exe"),
		StaticFS:            staticFS,
		SecretStore:         settings.NewSecretStore(dataDir),
		HealthTimeout:       30 * time.Second,
		LockPath:            filepath.Join(dataDir, "lock"),
		ListenAddr:          "127.0.0.1:0",
		MaxRestarts:         3,
		RestartBaseDelay:    time.Second,
		HealthCheckInterval: 5 * time.Second,
	})
	trayState := tray.New()
	var appCtx context.Context
	notify := func(title, body string) {
		if appCtx != nil {
			runtime.EventsEmit(appCtx, "ra:notification", map[string]string{"title": title, "body": body})
		}
	}
	openLogs := func(dir string) error {
		return exec.Command("explorer.exe", dir).Start()
	}
	chooseDirectory := func(title string) (string, error) {
		if appCtx == nil {
			return "", fmt.Errorf("desktop is not ready")
		}
		return runtime.OpenDirectoryDialog(appCtx, runtime.OpenDialogOptions{Title: title})
	}
	showInFolder := func(path string) error {
		return exec.Command("explorer.exe", "/select,"+path).Start()
	}
	api := bridge.New(desktopShell, settingsPath, dataDir, logDir, settings.NewSecretStore(dataDir), openLogs, chooseDirectory, showInFolder, updateManager, notify)
	application := &options.App{
		Title:  "ResearchAssistant",
		Width:  1440,
		Height: 900,
		AssetServer: &assetserver.Options{
			Handler: desktopShell.Handler(),
		},
		Bind: []interface{}{api},
		OnStartup: func(ctx context.Context) {
			appCtx = ctx
			if err := desktopShell.Start(ctx); err != nil {
				log.Printf("desktop shell failed to start: %v", err)
			}
			status := desktopShell.Status()
			trayState.SetStatus(tray.Status{Running: status.State == shell.StateRunning, ProxyURL: status.ProxyURL})
			if native, ok := trayState.(tray.Native); ok {
				_ = native.Start(func() {
					if appCtx != nil {
						runtime.WindowShow(appCtx)
					}
				}, func() {
					if appCtx != nil {
						runtime.Quit(appCtx)
					}
				})
			}
			if cfg, err := settings.Load(settingsPath); err == nil && cfg.Update.Auto && cfg.Update.Endpoint != "" {
				go func() {
					manifest, available, err := (&updater.Checker{Endpoint: cfg.Update.Endpoint, CurrentVersion: appVersion()}).Check(context.Background())
					if err != nil || !available || manifest == nil {
						return
					}
					runtime.EventsEmit(ctx, "ra:update-available", manifest)
				}()
			}
		},
		OnShutdown: func(ctx context.Context) {
			if err := desktopShell.Stop(ctx); err != nil {
				log.Printf("desktop shell stop: %v", err)
			}
		},
	}
	if err := wails.Run(application); err != nil {
		log.Fatal(err)
	}
	_ = os.Remove(filepath.Join(dataDir, "lock"))
}

func appVersion() string {
	if version := os.Getenv("RA_APP_VERSION"); version != "" {
		return version
	}
	return "0.0.0-dev"
}
