//go:build windows

package tools

import "os/exec"

func configureProcessGroup(cmd *exec.Cmd) {}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
