//go:build !windows

package supervisor

import "os/exec"

type processJob interface {
	Terminate() error
	Close() error
}

func assignProcessJob(*exec.Cmd) processJob { return nil }
