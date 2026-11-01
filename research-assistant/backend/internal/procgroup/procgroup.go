// Package procgroup provides platform-specific process-tree management.
//
// On Unix a command is placed in its own process group so a timeout can kill
// the whole tree. On Windows the same contract is implemented with a Job
// Object (KILL_ON_JOB_CLOSE) where available, with a taskkill fallback for
// short-lived helper commands such as git.
package procgroup

import (
	"errors"
	"os/exec"
)

// ErrUnsupported is returned when the current platform cannot create a Job
// Object (all non-Windows platforms).
var ErrUnsupported = errors.New("procgroup: job objects are only supported on windows")

// Limits describes the resource limits applied to a Windows Job Object.
type Limits struct {
	MemoryBytes  int64
	MaxProcesses int
	CPURate      uint32 // 1-10000, 10000 = 100%
}

// Configure prepares cmd so that Kill can terminate its whole process tree.
func Configure(cmd *exec.Cmd) {
	configure(cmd)
}

// Kill terminates the process tree created by Configure.
func Kill(cmd *exec.Cmd) error {
	return kill(cmd)
}

// Start starts cmd after applying platform-specific process-group setup.
func Start(cmd *exec.Cmd) error {
	configure(cmd)
	return cmd.Start()
}
