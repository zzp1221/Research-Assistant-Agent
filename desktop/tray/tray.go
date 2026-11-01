// Package tray defines the desktop tray/notification contract.
package tray

import "sync"

// Status is the compact tray tooltip state.
type Status struct {
	Running   bool
	Provider  string
	Isolation string
	ProxyURL  string
}

// Notification is one delivered desktop notification.
type Notification struct {
	Title string
	Body  string
}

// Tray is the platform tray contract. Implementations live in platform files.
type Tray interface {
	SetStatus(Status)
	Notify(title, body string) error
	Quit()
}

// Native is an optional driver that owns a real system tray icon.
type Native interface {
	Tray
	Start(onShow, onQuit func()) error
}

// Memory is a deterministic in-memory tray used by tests and by the headless
// stub entrypoint.
type Memory struct {
	mu            sync.Mutex
	status        Status
	notifications []Notification
	quit          bool
}

// NewMemory returns an empty memory tray.
func NewMemory() *Memory { return &Memory{} }

func (m *Memory) SetStatus(status Status) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = status
}

func (m *Memory) Notify(title, body string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications = append(m.notifications, Notification{Title: title, Body: body})
	return nil
}

func (m *Memory) Quit() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.quit = true
}

// Snapshot returns the current status, notifications and quit flag.
func (m *Memory) Snapshot() (Status, []Notification, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status, append([]Notification(nil), m.notifications...), m.quit
}
