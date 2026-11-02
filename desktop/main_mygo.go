//go:build !wails && !headless

package main

import (
	"context"
	"embed"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mygo "github.com/egoist/mygo"

	"research-assistant-desktop/bridge"
	"research-assistant-desktop/crashdump"
	mygoapp "research-assistant-desktop/mygo"
	"research-assistant-desktop/paths"
	"research-assistant-desktop/settings"
	"research-assistant-desktop/shell"
	"research-assistant-desktop/tray"
	"research-assistant-desktop/updater"
)

//go:embed all:webui/dist
var webAssets embed.FS

// UpdateAvailable tells the page that a signed update is available.
var UpdateAvailable = mygo.NewEvent[updater.UpdateStatus]("ra:update-available")

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

	mygo.App.SetName("ResearchAssistant")
	mygo.App.SetVersion(appVersion())
	mygo.App.SetPath(mygo.PathUserData, dataDir)
	mygo.App.SetPath(mygo.PathLogs, logDir)
	mygo.App.SetPath(mygo.PathCache, filepath.Join(dataDir, "cache"))

	// mygo generate runs the app to read its bindings; it must not touch the
	// running instance's lock or open a window.
	generating := strings.TrimSpace(os.Getenv("MYGO_GENERATE")) != ""
	if !generating {
		if !mygo.App.RequestSingleInstanceLock() {
			return
		}
	}

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
	// Bind the loopback listener before the window opens: the loading page is
	// served from it while backend and sandbox start.
	if err := desktopShell.Prepare(); err != nil {
		log.Fatalf("prepare desktop shell: %v", err)
	}
	proxyURL := desktopShell.ProxyURL()

	notify := func(title, body string) {
		if err := mygoapp.Notify(title, body); err != nil {
			log.Printf("notify: %v", err)
		}
	}
	api := bridge.New(desktopShell, settingsPath, dataDir, logDir, settings.NewSecretStore(dataDir),
		mygoapp.OpenPath, mygoapp.ChooseDirectory, mygoapp.ShowInFolder, updateManager, notify)
	mygo.Bind(api)

	var (
		mainWindow *mygo.Window
		trayState  tray.Native = mygoapp.NewTray()
		quitting   atomic.Bool
		stopOnce   sync.Once
		shellCtx   = context.Background()
	)

	stopShell := func() {
		stopOnce.Do(func() {
			quitting.Store(true)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := desktopShell.Stop(ctx); err != nil {
				log.Printf("desktop shell stop: %v", err)
			}
		})
	}
	showMain := func() {
		if mainWindow == nil {
			return
		}
		mainWindow.Show()
		mainWindow.Restore()
		mainWindow.Focus()
	}

	cfg, _ := settings.Load(settingsPath)
	closeToTray := cfg.UI.CloseToTray

	mygo.App.WhenReady(func() {
		if err := trayState.Start(showMain, func() { mygo.App.Quit() }); err != nil {
			log.Printf("tray: %v", err)
		}
		status := desktopShell.Status()
		trayState.SetStatus(tray.Status{Running: status.State == shell.StateRunning, ProxyURL: status.ProxyURL})

		mainWindow = mygo.NewWindow(mygo.WindowOptions{
			Title:           "ResearchAssistant",
			URL:             proxyURL,
			Width:           1440,
			Height:          900,
			MinWidth:        960,
			MinHeight:       640,
			StateKey:        "main",
			BackgroundColor: "light-dark(#f6f7f9, #0f172a)",
			Page: mygo.PageOptions{
				TrustedOrigins: []string{proxyURL},
			},
		})
		mainWindow.OnClose(func(e *mygo.CloseEvent) {
			if closeToTray && !quitting.Load() {
				e.PreventDefault()
				mainWindow.Hide()
			}
		})
		go func() {
			if err := desktopShell.Start(shellCtx); err != nil {
				log.Printf("desktop shell failed to start: %v", err)
			}
			// The loading page does not poll; reload it into the running app
			// or the error page once Start returns.
			if win := mainWindow; win != nil {
				win.Page().Reload()
			}
			status := desktopShell.Status()
			trayState.SetStatus(tray.Status{Running: status.State == shell.StateRunning, ProxyURL: status.ProxyURL})
		}()
		go func() {
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				if quitting.Load() {
					return
				}
				status := desktopShell.Status()
				trayState.SetStatus(tray.Status{Running: status.State == shell.StateRunning, ProxyURL: status.ProxyURL})
			}
		}()
		if cfg.Update.Auto && cfg.Update.Endpoint != "" {
			go func() {
				manifest, available, err := (&updater.Checker{Endpoint: cfg.Update.Endpoint, CurrentVersion: appVersion()}).Check(context.Background())
				if err != nil || !available || manifest == nil {
					return
				}
				_ = UpdateAvailable.Broadcast(updater.UpdateStatus{
					Available: true, Version: manifest.Version,
					ReleaseNotesURL: manifest.ReleaseNotesURL,
				})
			}()
		}
	})

	mygo.App.OnSecondInstance(func(args []string, workingDir string) { showMain() })
	mygo.App.OnOpenURL(func(rawURL string) { showMain() })
	mygo.App.OnOpenFile(func(path string) { showMain() })
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { stopShell() })
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { stopShell() })

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
	stopShell()
	_ = os.Remove(filepath.Join(dataDir, "lock"))
}

func appVersion() string {
	if version := os.Getenv("RA_APP_VERSION"); version != "" {
		return version
	}
	if version := mygo.App.Version(); version != "" {
		return version
	}
	return "0.0.0-dev"
}
