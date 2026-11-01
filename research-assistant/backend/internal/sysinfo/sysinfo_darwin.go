//go:build darwin

package sysinfo

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func memoryTotalBytes() int64 {
	raw, err := exec.Command("sysctl", "-n", "hw.memsize").Output()
	if err != nil {
		return 0
	}
	value, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func diskFreeBytes(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}
