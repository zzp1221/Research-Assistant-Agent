//go:build linux

package sysinfo

import "testing"

func TestMemoryAndDiskProbes(t *testing.T) {
	if got := MemoryTotalBytes(); got <= 0 {
		t.Fatalf("MemoryTotalBytes() = %d, want > 0", got)
	}
	if got := DiskFreeBytes(t.TempDir()); got <= 0 {
		t.Fatalf("DiskFreeBytes() = %d, want > 0", got)
	}
}
