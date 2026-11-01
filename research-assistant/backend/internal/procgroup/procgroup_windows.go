//go:build windows

package procgroup

import (
	"os/exec"
	"strconv"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func configure(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}

func kill(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// taskkill /T terminates the process tree. Job Object based supervision
	// (desktop/supervisor) uses the stronger TerminateJobObject path.
	killCmd := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := killCmd.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}
