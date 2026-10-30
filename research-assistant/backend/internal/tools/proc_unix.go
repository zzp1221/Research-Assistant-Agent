//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	// Negative pid targets the whole process group so a misbehaving MCP server
	// cannot leave grandchildren behind.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
