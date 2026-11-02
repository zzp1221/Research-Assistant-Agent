package supervisor

import (
	"os/exec"
)

// ExecRunner starts real OS processes.
type ExecRunner struct{}

func (ExecRunner) Start(spec CommandSpec) (Process, error) {
	cmd := exec.Command(spec.Path, spec.Args...)
	cmd.Dir = spec.Dir
	cmd.Env = spec.Env
	// backend.exe and sandbox.exe are console-subsystem programs; without
	// this Windows would open a console window for each of them.
	configureProcess(cmd)
	return &execProcess{cmd: cmd}, nil
}

type execProcess struct {
	cmd *exec.Cmd
	job processJob
}

func (p *execProcess) Start() error {
	if err := p.cmd.Start(); err != nil {
		return err
	}
	p.job = assignProcessJob(p.cmd)
	return nil
}

func (p *execProcess) Wait() error {
	if p.job != nil {
		defer p.job.Close()
	}
	return p.cmd.Wait()
}

func (p *execProcess) Kill() error {
	if p.job != nil {
		return p.job.Terminate()
	}
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

func (p *execProcess) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
