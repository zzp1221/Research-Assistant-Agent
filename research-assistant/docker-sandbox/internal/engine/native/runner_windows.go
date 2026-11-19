//go:build windows

package native

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
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
	var err error
	spec, err = prepareProcessSpec(spec)
	if err != nil {
		return nil, err
	}
	isolation := spec.Isolation
	if isolation == "" {
		isolation, err = ResolveIsolation(r.requested, r.currentProbe(), spec.GPURequired)
		if err != nil {
			return nil, err
		}
	}
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
		return nil, fmt.Errorf("unknown native process isolation")
	}
}

func prepareProcessSpec(spec runSpec) (runSpec, error) {
	if len(spec.Argv) == 0 {
		return spec, fmt.Errorf("native command argv is empty")
	}
	if spec.EnvironmentRoot == "" {
		spec.EnvironmentRoot = spec.Cwd
	}
	if !filepath.IsAbs(spec.EnvironmentRoot) {
		return spec, fmt.Errorf("native process workspace must be absolute")
	}
	if _, err := validateTaskEnvironment(spec.Env); err != nil {
		return spec, err
	}
	if filepath.IsAbs(spec.Argv[0]) {
		return spec, nil
	}
	if strings.ContainsAny(spec.Argv[0], "/\\:") {
		return spec, fmt.Errorf("native process executable must be absolute or a system executable name")
	}
	base, err := systemEnvironment()
	if err != nil {
		return spec, err
	}
	paths := filepath.SplitList(base["PATH"])
	if spec.Interpreter != "" {
		if !filepath.IsAbs(spec.Interpreter) {
			return spec, fmt.Errorf("trusted interpreter must be absolute")
		}
		paths = append([]string{filepath.Dir(spec.Interpreter)}, paths...)
	}
	name := spec.Argv[0]
	for _, dir := range paths {
		candidate := filepath.Join(dir, name)
		if executable, err := exec.LookPath(candidate); err == nil {
			spec.Argv = append([]string(nil), spec.Argv...)
			spec.Argv[0] = executable
			return spec, nil
		}
	}
	return spec, fmt.Errorf("native executable is absent from the trusted search path")
}

func runExecProcess(ctx context.Context, spec runSpec) (*engine.Result, error) {
	var err error
	spec, err = prepareProcessSpec(spec)
	if err != nil {
		return nil, err
	}
	env, err := researchEnvironment(spec.EnvironmentRoot, spec.Interpreter, spec.Env)
	if err != nil {
		return nil, err
	}
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	command := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	command.Dir = spec.Cwd
	command.Env = env
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
