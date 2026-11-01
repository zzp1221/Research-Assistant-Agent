//go:build windows

package sysinfo

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

var procGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func memoryTotalBytes() int64 {
	var status memoryStatusEx
	status.Length = uint32(unsafe.Sizeof(status))
	result, _, _ := procGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return 0
	}
	return int64(status.TotalPhys)
}

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
