//go:build windows

package procgroup

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Job wraps a Windows Job Object.
type Job struct {
	handle windows.Handle
}

const (
	jobObjectExtendedLimitInformation  = 9
	jobObjectBasicUIRestrictions       = 4
	jobObjectCpuRateControlInformation = 15
)

type jobObjectBasicLimitInformation struct {
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

type ioCounters struct {
	ReadOperationCount  uint64
	WriteOperationCount uint64
	OtherOperationCount uint64
	ReadTransferCount   uint64
	WriteTransferCount  uint64
	OtherTransferCount  uint64
}

type jobObjectExtendedLimitInformationStruct struct {
	BasicLimitInformation jobObjectBasicLimitInformation
	IoInfo                ioCounters
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

type jobObjectBasicUIRestrictionsStruct struct {
	UIRestrictionsClass uint32
}

type jobObjectCpuRateControlInformationStruct struct {
	ControlFlags uint32
	CpuRate      uint32
}

// NewJob creates a Job Object with the supplied limits. A zero limit is left
// unset so the OS default applies.
func NewJob(limits Limits) (*Job, error) {
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create job object: %w", err)
	}
	job := &Job{handle: handle}
	info := jobObjectExtendedLimitInformationStruct{}
	info.BasicLimitInformation.LimitFlags = 0x00002000 // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if limits.MemoryBytes > 0 {
		info.BasicLimitInformation.LimitFlags |= 0x00000100 // JOB_OBJECT_LIMIT_PROCESS_MEMORY
		info.ProcessMemoryLimit = uintptr(limits.MemoryBytes)
	}
	if limits.MaxProcesses > 0 {
		info.BasicLimitInformation.LimitFlags |= 0x00000008 // JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = uint32(limits.MaxProcesses)
	}
	if _, err := windows.SetInformationJobObject(handle, jobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("set job limits: %w", err)
	}
	ui := jobObjectBasicUIRestrictionsStruct{UIRestrictionsClass: 0x1 | 0x2 | 0x4 | 0x8 | 0x10}
	_, _ = windows.SetInformationJobObject(handle, jobObjectBasicUIRestrictions, uintptr(unsafe.Pointer(&ui)), uint32(unsafe.Sizeof(ui)))
	if limits.CPURate > 0 {
		cpu := jobObjectCpuRateControlInformationStruct{ControlFlags: 0x1, CpuRate: limits.CPURate}
		_, _ = windows.SetInformationJobObject(handle, jobObjectCpuRateControlInformation, uintptr(unsafe.Pointer(&cpu)), uint32(unsafe.Sizeof(cpu)))
	}
	return job, nil
}

// Assign adds a process to the job.
func (j *Job) Assign(process *os.Process) error {
	if j == nil || j.handle == 0 {
		return ErrUnsupported
	}
	handle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(process.Pid))
	if err != nil {
		return fmt.Errorf("open process %d: %w", process.Pid, err)
	}
	defer windows.CloseHandle(handle)
	if err := windows.AssignProcessToJobObject(j.handle, handle); err != nil {
		return fmt.Errorf("assign process to job: %w", err)
	}
	return nil
}

// Terminate terminates every process in the job.
func (j *Job) Terminate() error {
	if j == nil || j.handle == 0 {
		return nil
	}
	if err := windows.TerminateJobObject(j.handle, 1); err != nil {
		return fmt.Errorf("terminate job: %w", err)
	}
	return nil
}

// Close releases the job handle. If KILL_ON_JOB_CLOSE is set this also kills
// remaining processes.
func (j *Job) Close() error {
	if j == nil || j.handle == 0 {
		return nil
	}
	err := windows.CloseHandle(j.handle)
	j.handle = 0
	return err
}
