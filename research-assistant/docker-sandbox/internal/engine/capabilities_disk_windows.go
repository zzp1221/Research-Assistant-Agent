//go:build windows

package engine

import (
	"golang.org/x/sys/windows"
)

// diskFreeBytes reports the free bytes available to the current user on the
// volume containing path. It replaces the historical stub that always returned
// zero, which made the desktop capability probe lie about disk space.
func diskFreeBytes(path string) int64 {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	var freeAvailable, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(pathPtr, &freeAvailable, &totalBytes, &totalFree); err != nil {
		return 0
	}
	return int64(freeAvailable)
}
