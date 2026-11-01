//go:build !windows

package tray

// New returns the in-memory tray outside Windows.
func New() Tray { return NewMemory() }
