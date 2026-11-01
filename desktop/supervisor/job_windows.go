//go:build windows

package supervisor

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

type processJob interface {
	Terminate() error
	Close() error
}

type jobObject struct{ handle windows.Handle }

type jobExtendedLimitInfo struct {
	BasicLimitInformation struct {
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
	IoInfo                [6]uint64
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

func assignProcessJob(cmd *exec.Cmd) processJob {
	if cmd.Process == nil {
		return nil
	}
	handle, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil
	}
	info := jobExtendedLimitInfo{}
	info.BasicLimitInformation.LimitFlags = 0x00002000 // KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(handle, 9, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(handle)
		return nil
	}
	processHandle, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(handle)
		return nil
	}
	defer windows.CloseHandle(processHandle)
	if err := windows.AssignProcessToJobObject(handle, processHandle); err != nil {
		_ = windows.CloseHandle(handle)
		return nil
	}
	return &jobObject{handle: handle}
}

func (j *jobObject) Terminate() error { return windows.TerminateJobObject(j.handle, 1) }
func (j *jobObject) Close() error     { return windows.CloseHandle(j.handle) }
