//go:build !windows

package procgroup

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestConfigureSetsProcessGroup(t *testing.T) {
	cmd := exec.Command("sleep", "1")
	Configure(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatalf("expected Setpgid to be true, got %+v", cmd.SysProcAttr)
	}
}

func TestKillTerminatesProcessTree(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 60 & wait")
	Configure(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := Kill(cmd); err != nil {
		t.Fatalf("kill: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("expected killed process to return an error")
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("process tree did not exit after Kill")
	}
	_ = syscall.Kill(-cmd.Process.Pid, 0)
}
