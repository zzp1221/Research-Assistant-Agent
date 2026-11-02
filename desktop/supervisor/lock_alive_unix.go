//go:build !windows

package supervisor

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid names a running process.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
