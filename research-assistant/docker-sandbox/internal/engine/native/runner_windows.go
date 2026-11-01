//go:build windows

package native

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"docker-sandbox/internal/engine"

	"golang.org/x/sys/windows"
)

const createNewProcessGroup = 0x00000200

type windowsRunner struct {
	mu        sync.RWMutex
	requested string
	probe     *ProbeReport
}

func newCommandRunner(cfg Config) (commandRunner, error) {
	return &windowsRunner{requested: NormalizeIsolation(cfg.Runner)}, nil
}

func (r *windowsRunner) SetProbe(report *ProbeReport) {
	r.mu.Lock()
	r.probe = report
	r.mu.Unlock()
}

func (r *windowsRunner) currentProbe() *ProbeReport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.probe
}

func (r *windowsRunner) Close() error { return nil }

func (r *windowsRunner) Run(ctx context.Context, spec runSpec) (*engine.Result, error) {
	if len(spec.Argv) == 0 {
		return nil, fmt.Errorf("native command argv is empty")
	}
	isolation := SelectIsolation(r.requested, r.currentProbe(), spec.GPURequired)
	switch isolation {
	case IsolationAppContainer:
		capabilities, err := appContainerCapabilities(spec.NetworkOn)
		if err != nil {
			return nil, fmt.Errorf("appcontainer: %w", err)
		}
		return runRawProcess(ctx, spec, rawLaunch{Security: capabilities})
	case IsolationLowIL:
		token, err := createLowIntegrityToken()
		if err != nil {
			return nil, fmt.Errorf("lowil: %w", err)
		}
		defer token.Close()
		return runRawProcess(ctx, spec, rawLaunch{Token: token})
	default:
		return runExecProcess(ctx, spec)
	}
}

func runExecProcess(ctx context.Context, spec runSpec) (*engine.Result, error) {
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	command := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	command.Dir = spec.Cwd
	command.Env = append(os.Environ(), EnvPairs(spec.Env)...)
	if spec.Stdin != "" {
		command.Stdin = bytes.NewBufferString(spec.Stdin)
	}
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup, HideWindow: true}
	stdoutPipe, err := command.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := command.StderrPipe()
	if err != nil {
		return nil, err
	}
	started := time.Now()
	if err := command.Start(); err != nil {
		return nil, err
	}
	job, jobErr := createJob(spec.Limits)
	if jobErr == nil {
		if processHandle, openErr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid)); openErr == nil {
			_ = assignProcessToJob(job, processHandle)
			_ = windows.CloseHandle(processHandle)
		}
		defer windows.CloseHandle(job)
	}
	var stdout, stderr bytes.Buffer
	var wg sync.WaitGroup
	chunks := make(chan streamChunk, 64)
	read := func(reader io.Reader, stream string, sink *bytes.Buffer) {
		defer wg.Done()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			sink.WriteString(line + "\n")
			chunks <- streamChunk{stream: stream, line: line}
		}
	}
	wg.Add(2)
	go read(stdoutPipe, "stdout", &stdout)
	go read(stderrPipe, "stderr", &stderr)
	go func() {
		wg.Wait()
		close(chunks)
	}()
	limit := 0
	if spec.MaxOutputKB > 0 {
		limit = spec.MaxOutputKB * 1024
	}
	emitted := 0
	for chunk := range chunks {
		if spec.Emit == nil {
			continue
		}
		message := chunk.line
		if limit > 0 {
			if emitted >= limit {
				continue
			}
			if emitted+len(message) > limit {
				message = message[:limit-emitted] + "...[truncated]"
			}
			emitted += len(message)
		}
		_ = spec.Emit(engine.Event{Type: "chunk", Stream: chunk.stream, Message: message})
	}
	waitErr := command.Wait()
	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}
	result := &engine.Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode, DurationMS: time.Since(started).Milliseconds()}
	if limit > 0 {
		if len(result.Stdout) > limit {
			result.Stdout = result.Stdout[:limit] + "...[truncated]"
			result.Truncated = true
		}
		if len(result.Stderr) > limit {
			result.Stderr = result.Stderr[:limit] + "...[truncated]"
			result.Truncated = true
		}
	}
	return result, nil
}

type streamChunk struct {
	stream string
	line   string
}
