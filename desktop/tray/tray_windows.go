//go:build windows

package tray

import (
	"fmt"
	"sync"

	"github.com/getlantern/systray"
)

type windowsTray struct {
	mu                   sync.Mutex
	status               Status
	pendingNotifications []Notification
	showItem             *systray.MenuItem
	quitItem             *systray.MenuItem
	onShow               func()
	onQuit               func()
	started              bool
	ready                bool
}

func newWindowsTray() Tray { return &windowsTray{} }

func (t *windowsTray) Start(onShow, onQuit func()) error {
	t.mu.Lock()
	if t.started {
		t.mu.Unlock()
		return nil
	}
	t.started = true
	t.onShow = onShow
	t.onQuit = onQuit
	t.mu.Unlock()
	go systray.Run(t.onReady, func() {})
	return nil
}

func (t *windowsTray) onReady() {
	systray.SetTitle("ResearchAssistant")
	t.mu.Lock()
	t.ready = true
	tooltip := t.tooltipLocked()
	pending := append([]Notification(nil), t.pendingNotifications...)
	t.pendingNotifications = nil
	t.mu.Unlock()
	systray.SetTooltip(tooltip)
	t.showItem = systray.AddMenuItem("显示主窗口", "打开 ResearchAssistant")
	t.quitItem = systray.AddMenuItem("退出", "退出 ResearchAssistant")
	go func() {
		for {
			select {
			case <-t.showItem.ClickedCh:
				t.mu.Lock()
				callback := t.onShow
				t.mu.Unlock()
				if callback != nil {
					callback()
				}
			case <-t.quitItem.ClickedCh:
				t.Quit()
				return
			}
		}
	}()
	for _, notification := range pending {
		t.notifyNow(notification)
	}
}

func (t *windowsTray) SetStatus(status Status) {
	t.mu.Lock()
	t.status = status
	ready := t.ready
	tooltip := t.tooltipLocked()
	t.mu.Unlock()
	if ready {
		systray.SetTooltip(tooltip)
	}
}

func (t *windowsTray) Notify(title, body string) error {
	notification := Notification{Title: title, Body: body}
	t.mu.Lock()
	if !t.ready {
		t.pendingNotifications = append(t.pendingNotifications, notification)
		t.mu.Unlock()
		return nil
	}
	t.mu.Unlock()
	t.notifyNow(notification)
	return nil
}

// notifyNow uses the tray tooltip as a fallback because systray does not expose
// Windows toast notifications. The Wails layer still emits a UI event.
func (t *windowsTray) notifyNow(notification Notification) {
	if notification.Title == "" && notification.Body == "" {
		return
	}
	systray.SetTooltip(fmt.Sprintf("%s: %s", notification.Title, notification.Body))
}

func (t *windowsTray) Quit() { systray.Quit() }

func (t *windowsTray) tooltipLocked() string {
	if t.status.Provider == "" {
		return "ResearchAssistant"
	}
	state := "已停止"
	if t.status.Running {
		state = "运行中"
	}
	return fmt.Sprintf("ResearchAssistant · %s · %s", t.status.Provider, state)
}
