//go:build windows

package supervisor

import (
	"errors"

	"golang.org/x/sys/windows"
)

// processAlive reports whether pid names a running process.
func processAlive(pid int) bool {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		// A process that does not exist makes OpenProcess fail with
		// ERROR_INVALID_PARAMETER; access denied means it exists.
		return !errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(handle)
	var code uint32
	if err := windows.GetExitCodeProcess(handle, &code); err != nil {
		return true
	}
	return code == 259 // STILL_ACTIVE
}
