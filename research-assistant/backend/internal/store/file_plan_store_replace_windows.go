//go:build windows

package store

import (
	"golang.org/x/sys/windows"
)

// MoveFileEx replaces within a directory without a delete-before-rename gap.
// WRITE_THROUGH requests completion of the move before success is returned.
// Guarantees are limited to tested local Windows filesystems, not power loss.
func replacePlanStoreFile(from, to string) (bool, error) {
	source, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return false, err
	}
	target, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return false, err
	}
	if err := windows.MoveFileEx(source, target, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return false, err
	}
	return true, nil
}

// Windows file contents are flushed before the WRITE_THROUGH replacement.
func syncPlanStoreDirectory(string) error { return nil }
