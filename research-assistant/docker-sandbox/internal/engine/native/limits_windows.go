//go:build windows

package native

import (
	"fmt"
	"unsafe"

	"docker-sandbox/internal/engine"

	"golang.org/x/sys/windows"
)

const (
	jobObjectBasicUIRestrictions       = 4
	jobObjectExtendedLimitInformation  = 9
	jobObjectCpuRateControlInformation = 15

	jobObjectLimitActiveProcess  = 0x00000008
	jobObjectLimitProcessMemory  = 0x00000100
	jobObjectLimitKillOnJobClose = 0x00002000

	jobObjectCpuRateControlEnable  = 0x00000001
	jobObjectCpuRateControlHardCap = 0x00000004

	jobObjectUILimitHandles        = 0x00000001
	jobObjectUILimitReadClipboard  = 0x00000002
	jobObjectUILimitWriteClipboard = 0x00000004
)

type jobBasicLimitInformation struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimitInformation struct {
	BasicLimitInformation jobBasicLimitInformation
	IoInfo                [6]uint64
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type jobCpuRateControlInformation struct {
	ControlFlags uint32
	CpuRate      uint32
}

type jobBasicUIRestrictions struct {
	UIRestrictionsClass uint32
}

// createJob builds a kill-on-close Job Object with the requested memory,
// process-count and CPU-rate limits.
func createJob(limits engine.Limits) (windows.Handle, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := jobExtendedLimitInformation{}
	flags := uint32(jobObjectLimitKillOnJobClose)
	if limits.MemoryMB > 0 {
		flags |= jobObjectLimitProcessMemory
		info.ProcessMemoryLimit = uintptr(uint64(limits.MemoryMB) << 20)
	}
	maxProcesses := limits.MaxProcesses
	if maxProcesses <= 0 {
		maxProcesses = 64
	}
	flags |= jobObjectLimitActiveProcess
	info.BasicLimitInformation.ActiveProcessLimit = uint32(maxProcesses)
	info.BasicLimitInformation.LimitFlags = flags
	if _, err := windows.SetInformationJobObject(handle, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	if limits.CPUPercent > 0 {
		cpu := jobCpuRateControlInformation{
			ControlFlags: jobObjectCpuRateControlEnable | jobObjectCpuRateControlHardCap,
			CpuRate:      uint32(limits.CPUPercent) * 100,
		}
		if _, err := windows.SetInformationJobObject(handle, jobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu))); err != nil {
			_ = windows.CloseHandle(handle)
			return 0, fmt.Errorf("set cpu rate: %w", err)
		}
	}
	ui := jobBasicUIRestrictions{UIRestrictionsClass: jobObjectUILimitHandles | jobObjectUILimitReadClipboard | jobObjectUILimitWriteClipboard}
	if _, err := windows.SetInformationJobObject(handle, jobObjectBasicUIRestrictions, uintptr(unsafe.Pointer(&ui)), uint32(unsafe.Sizeof(ui))); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, err
	}
	return handle, nil
}

func assignProcessToJob(job windows.Handle, process windows.Handle) error {
	return windows.AssignProcessToJobObject(job, process)
}
