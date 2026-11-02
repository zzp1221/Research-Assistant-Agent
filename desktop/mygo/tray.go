package mygoapp

import (
	_ "embed"
	"fmt"
	"sync"

	mygo "github.com/egoist/mygo"

	"research-assistant-desktop/tray"
)

//go:embed icon.png
var trayIcon []byte

// Tray implements tray.Native with a MyGo tray icon.
type Tray struct {
	mu      sync.Mutex
	status  tray.Status
	native  *mygo.Tray
	onShow  func()
	onQuit  func()
	started bool
}

// NewTray returns a MyGo-backed tray driver.
func NewTray() tray.Native { return &Tray{} }

// Start creates the system tray icon and its menu.
func (t *Tray) Start(onShow, onQuit func()) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}
	t.started = true
	t.onShow, t.onQuit = onShow, onQuit
	tooltip := tooltip(t.status)
	t.mu.Unlock()

	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "显示主窗口", Click: func(*mygo.MenuItem, *mygo.Window) { t.show() }},
		mygo.Separator(),
		{Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { t.quit() }},
	})
	native, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "research-assistant",
		Icon:    trayIcon,
		ToolTip: tooltip,
		Menu:    menu,
	})
	if err != nil {
		t.mu.Lock()
		t.started = false
		t.mu.Unlock()
		return fmt.Errorf("create tray: %w", err)
	}
	t.mu.Lock()
	t.native = native
	t.mu.Unlock()
	return nil
}

// SetStatus updates the tray tooltip.
func (t *Tray) SetStatus(status tray.Status) {
	t.mu.Lock()
	t.status = status
	native := t.native
	t.mu.Unlock()
	if native != nil {
		native.SetToolTip(tooltip(status))
	}
}

// Notify shows a system notification.
func (t *Tray) Notify(title, body string) error {
	return Notify(title, body)
}

// Quit asks the application to quit.
func (t *Tray) Quit() { t.quit() }

func (t *Tray) show() {
	t.mu.Lock()
	callback := t.onShow
	t.mu.Unlock()
	if callback != nil {
		callback()
	}
}

func (t *Tray) quit() {
	t.mu.Lock()
	callback := t.onQuit
	t.mu.Unlock()
	if callback != nil {
		callback()
		return
	}
	mygo.App.Quit()
}

func tooltip(status tray.Status) string {
	if status.Provider == "" {
		return "ResearchAssistant"
	}
	state := "已停止"
	if status.Running {
		state = "运行中"
	}
	return fmt.Sprintf("ResearchAssistant · %s · %s", status.Provider, state)
}
