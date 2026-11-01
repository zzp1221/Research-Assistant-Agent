//go:build windows

package native

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"docker-sandbox/internal/engine"

	"golang.org/x/sys/windows"
)

type rawLaunch struct {
	Argv     []string
	Cwd      string
	Env      map[string]string
	Stdin    string
	Token    windows.Token
	Security *securityCapabilities
	Limits   engine.Limits
}

type rawProcess struct {
	process windows.Handle
	job     windows.Handle
	pid     uint32
	stdout  *os.File
	stderr  *os.File
	stdin   *os.File
}

func startRawProcess(launch rawLaunch) (*rawProcess, error) {
	stdoutRead, stdoutWrite, err := inheritablePipe()
	if err != nil {
		return nil, err
	}
	stderrRead, stderrWrite, err := inheritablePipe()
	if err != nil {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		return nil, err
	}
	var stdinRead, stdinWrite *os.File
	if launch.Stdin != "" {
		stdinRead, stdinWrite, err = inheritablePipe()
		if err != nil {
			_ = stdoutRead.Close()
			_ = stdoutWrite.Close()
			_ = stderrRead.Close()
			_ = stderrWrite.Close()
			return nil, err
		}
	}
	cleanup := func() {
		_ = stdoutRead.Close()
		_ = stdoutWrite.Close()
		_ = stderrRead.Close()
		_ = stderrWrite.Close()
		if stdinRead != nil {
			_ = stdinRead.Close()
		}
		if stdinWrite != nil {
			_ = stdinWrite.Close()
		}
	}
	commandLine, err := windows.UTF16PtrFromString(buildCommandLine(launch.Argv))
	if err != nil {
		cleanup()
		return nil, err
	}
	var cwdPtr *uint16
	if strings.TrimSpace(launch.Cwd) != "" {
		cwdPtr, err = windows.UTF16PtrFromString(launch.Cwd)
		if err != nil {
			cleanup()
			return nil, err
		}
	}
	envBlock, err := buildEnvironmentBlock(append(os.Environ(), EnvPairs(launch.Env)...))
	if err != nil {
		cleanup()
		return nil, err
	}
	si := windows.StartupInfo{}
	si.Cb = uint32(unsafe.Sizeof(windows.StartupInfoEx{}))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdOutput = windows.Handle(stdoutWrite.Fd())
	si.StdErr = windows.Handle(stderrWrite.Fd())
	if stdinRead != nil {
		si.StdInput = windows.Handle(stdinRead.Fd())
	} else {
		si.StdInput = windows.InvalidHandle
	}
	siEx := windows.StartupInfoEx{StartupInfo: si}
	var attributes *windows.ProcThreadAttributeListContainer
	if launch.Security != nil {
		attributes, err = windows.NewProcThreadAttributeList(1)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("proc thread attribute list: %w", err)
		}
		defer attributes.Delete()
		if err := attributes.Update(procThreadAttributeSecurityCapabilities, unsafe.Pointer(launch.Security), unsafe.Sizeof(*launch.Security)); err != nil {
			cleanup()
			return nil, fmt.Errorf("security capabilities attribute: %w", err)
		}
		siEx.ProcThreadAttributeList = attributes.List()
	}
	var processInfo windows.ProcessInformation
	flags := uint32(windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT)
	startup := (*windows.StartupInfo)(unsafe.Pointer(&siEx))
	if launch.Token != 0 {
		err = windows.CreateProcessAsUser(launch.Token, nil, commandLine, nil, nil, true, flags, &envBlock[0], cwdPtr, startup, &processInfo)
	} else {
		err = windows.CreateProcess(nil, commandLine, nil, nil, true, flags, &envBlock[0], cwdPtr, startup, &processInfo)
	}
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("create process: %w", err)
	}
	_ = stdoutWrite.Close()
	_ = stderrWrite.Close()
	if stdinRead != nil {
		_ = stdinRead.Close()
	}
	job, err := createJob(launch.Limits)
	if err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		_ = windows.CloseHandle(processInfo.Process)
		_ = windows.CloseHandle(processInfo.Thread)
		cleanup()
		return nil, fmt.Errorf("create job: %w", err)
	}
	if err := assignProcessToJob(job, processInfo.Process); err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		_ = windows.CloseHandle(job)
		_ = windows.CloseHandle(processInfo.Process)
		_ = windows.CloseHandle(processInfo.Thread)
		cleanup()
		return nil, fmt.Errorf("assign process to job: %w", err)
	}
	if _, err := windows.ResumeThread(processInfo.Thread); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
		_ = windows.CloseHandle(processInfo.Process)
		_ = windows.CloseHandle(processInfo.Thread)
		cleanup()
		return nil, fmt.Errorf("resume process: %w", err)
	}
	_ = windows.CloseHandle(processInfo.Thread)
	process := &rawProcess{process: processInfo.Process, job: job, pid: processInfo.ProcessId, stdout: stdoutRead, stderr: stderrRead, stdin: stdinWrite}
	if stdinWrite != nil {
		go func() {
			_, _ = io.WriteString(stdinWrite, launch.Stdin)
			_ = stdinWrite.Close()
		}()
	}
	return process, nil
}

func (p *rawProcess) close() {
	if p.stdout != nil {
		_ = p.stdout.Close()
	}
	if p.stderr != nil {
		_ = p.stderr.Close()
	}
	if p.job != 0 {
		_ = windows.CloseHandle(p.job)
		p.job = 0
	}
	if p.process != 0 {
		_ = windows.CloseHandle(p.process)
		p.process = 0
	}
}

func inheritablePipe() (*os.File, *os.File, error) {
	read, write, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	if err := windows.SetHandleInformation(windows.Handle(write.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		_ = read.Close()
		_ = write.Close()
		return nil, nil, err
	}
	if err := windows.SetHandleInformation(windows.Handle(read.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		_ = read.Close()
		_ = write.Close()
		return nil, nil, err
	}
	return read, write, nil
}

func buildCommandLine(argv []string) string {
	parts := make([]string, 0, len(argv))
	for _, arg := range argv {
		parts = append(parts, syscall.EscapeArg(arg))
	}
	return strings.Join(parts, " ")
}

func buildEnvironmentBlock(entries []string) ([]uint16, error) {
	seen := map[string]string{}
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		index := strings.Index(entry, "=")
		if index <= 0 {
			continue
		}
		key := strings.ToUpper(entry[:index])
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
		seen[key] = entry
	}
	sort.Strings(keys)
	block := make([]uint16, 0, 4096)
	for _, key := range keys {
		block = append(block, utf16.Encode([]rune(seen[key]))...)
		block = append(block, 0)
	}
	block = append(block, 0)
	return block, nil
}

func runRawProcess(ctx context.Context, spec runSpec, launch rawLaunch) (*engine.Result, error) {
	launch.Argv = spec.Argv
	launch.Cwd = spec.Cwd
	launch.Env = spec.Env
	launch.Stdin = spec.Stdin
	launch.Limits = spec.Limits
	if spec.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Timeout)
		defer cancel()
	}
	process, err := startRawProcess(launch)
	if err != nil {
		return nil, err
	}
	defer process.close()
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
	go read(process.stdout, "stdout", &stdout)
	go read(process.stderr, "stderr", &stderr)
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
	waitDone := make(chan struct{})
	go func() {
		_, _ = windows.WaitForSingleObject(process.process, windows.INFINITE)
		close(waitDone)
	}()
	select {
	case <-ctx.Done():
		_ = windows.TerminateJobObject(process.job, 1)
		<-waitDone
	case <-waitDone:
	}
	var exitCode uint32
	_ = windows.GetExitCodeProcess(process.process, &exitCode)
	result := &engine.Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: int(exitCode)}
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
