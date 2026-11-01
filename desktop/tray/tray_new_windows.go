//go:build windows

package tray

// New returns the Windows systray driver.
func New() Tray { return newWindowsTray() }
