// Package sysinfo provides cross-platform host resource probes used by the
// reproduction mode decision and the sandbox capability endpoint.
package sysinfo

// MemoryTotalBytes returns total physical memory, or 0 when it cannot be
// determined.
func MemoryTotalBytes() int64 {
	return memoryTotalBytes()
}

// DiskFreeBytes returns the free bytes available to the current user on the
// filesystem containing path, or 0 when it cannot be determined.
func DiskFreeBytes(path string) int64 {
	return diskFreeBytes(path)
}
