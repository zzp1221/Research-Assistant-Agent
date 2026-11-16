//go:build windows

package native

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func runProbe(ctx context.Context, pythonPath, workDir string) ProbeReport {
	return runProbeWithEndpoint(ctx, pythonPath, workDir, strings.TrimSpace(os.Getenv("SANDBOX_PROBE_NETWORK_ADDRESS")))
}

func runProbeWithEndpoint(ctx context.Context, pythonPath, workDir, endpoint string) ProbeReport {
	cases := []ProbeCase{}
	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return failedProbe(err.Error())
	}
	root, err := os.MkdirTemp(workDir, "isolation-")
	if err != nil {
		return failedProbe(err.Error())
	}
	defer os.RemoveAll(root)
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return failedProbe(err.Error())
	}
	if err := prepareAppContainerWorkspace(workspace); err != nil {
		return failedProbe(err.Error())
	}
	sentinel := filepath.Join(root, "host-sentinel")
	const sentinelValue = "trusted-host-control"
	if err := os.WriteFile(sentinel, []byte(sentinelValue), 0o600); err != nil {
		return failedProbe(err.Error())
	}
	// Freeze the actual address reached by the host positive control. Offline
	// DNS denial and a different DNS answer must not stand in for testing the
	// configured TCP destination in both profiles.
	networkTarget, networkControlErr := probeControlledNetworkTarget(ctx, endpoint)

	for _, d := range ProbeDefinitions() {
		if ctx.Err() != nil {
			cases = append(cases, newProbeCase(d, "error", "probe cancelled"))
			continue
		}
		target := ""
		switch d.Name {
		case "workspace_write":
			target = filepath.Join(workspace, "allowed.txt")
		case "workspace_escape":
			target = sentinel
		case "network_connect", "network_online":
			if networkControlErr != nil {
				cases = append(cases, newProbeCase(d, "inconclusive", "controlled network target unavailable: "+networkControlErr.Error()))
				continue
			}
			target = networkTarget
		}
		if d.Name == "job_cleanup" {
			cases = append(cases, probeJobCleanup(ctx, pythonPath, workspace, d))
			continue
		}
		if d.Name == "process_identity" {
			cases = append(cases, probeProcessIdentity(ctx, pythonPath, workspace, d))
			continue
		}
		result, err := runAppContainerProbe(ctx, pythonPath, workspace, d.Profile == "online", ProbeScript(d.Name, target))
		if err != nil {
			cases = append(cases, newProbeCase(d, "error", err.Error()))
			continue
		}
		o, err := parseProbeObservation(result, d.Name)
		if err != nil {
			cases = append(cases, newProbeCase(d, "error", err.Error()))
			continue
		}
		c := evaluateObservation(d, o)
		if d.Name == "workspace_escape" {
			content, readErr := os.ReadFile(sentinel)
			if readErr != nil || string(content) != sentinelValue {
				c = newProbeCase(d, "fail", "trusted host sentinel was modified")
			}
		}
		if d.Name == "workspace_write" && c.Passed {
			content, readErr := os.ReadFile(target)
			if readErr != nil || string(content) != "isolated-probe" {
				c = newProbeCase(d, "error", "supervisor could not verify workspace output")
			}
		}
		cases = append(cases, c)
	}
	r := EvaluateProbe(cases)
	r.OS = runtime.GOOS + "/" + runtime.GOARCH
	return r
}

func failedProbe(reason string) ProbeReport {
	cases := []ProbeCase{}
	for _, d := range ProbeDefinitions() {
		cases = append(cases, newProbeCase(d, "error", reason))
	}
	r := EvaluateProbe(cases)
	r.OS = runtime.GOOS + "/" + runtime.GOARCH
	return r
}

func probeEndpointReachable(ctx context.Context, endpoint string) error {
	_, err := probeControlledNetworkTarget(ctx, endpoint)
	return err
}

func probeControlledNetworkTarget(ctx context.Context, endpoint string) (string, error) {
	if endpoint == "" {
		return "", fmt.Errorf("SANDBOX_PROBE_NETWORK_ADDRESS is not configured")
	}
	if _, _, err := splitProbeEndpoint(endpoint); err != nil {
		return "", err
	}
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", endpoint)
	if err != nil {
		return "", fmt.Errorf("host positive control failed")
	}
	target := conn.RemoteAddr().String()
	return target, conn.Close()
}

func probeLaunch(pythonPath, workspace string, online bool, script string) (rawLaunch, error) {
	security, err := appContainerCapabilities(online)
	if err != nil {
		return rawLaunch{}, err
	}
	return rawLaunch{Argv: probePythonArgv(pythonPath, script), Cwd: workspace, EnvironmentRoot: workspace, Interpreter: pythonPath, Security: security}, nil
}

func processIsAppContainer(process windows.Handle) (bool, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(process, windows.TOKEN_QUERY, &token); err != nil {
		return false, err
	}
	defer token.Close()
	var value, length uint32
	const tokenIsAppContainer = 29
	if err := windows.GetTokenInformation(token, tokenIsAppContainer, (*byte)(unsafe.Pointer(&value)), uint32(unsafe.Sizeof(value)), &length); err != nil {
		return false, err
	}
	return value == 1, nil
}

func runAppContainerProbe(ctx context.Context, pythonPath, workspace string, online bool, script string) (string, error) {
	launch, err := probeLaunch(pythonPath, workspace, online, script)
	if err != nil {
		return "", err
	}
	process, err := startRawProcess(launch)
	if err != nil {
		return "", err
	}
	defer process.close()
	if ok, err := processIsAppContainer(process.process); err != nil || !ok {
		_ = windows.TerminateJobObject(process.job, 1)
		return "", fmt.Errorf("probe did not execute with an AppContainer token")
	}
	childCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() { _, _ = windows.WaitForSingleObject(process.process, windows.INFINITE); close(done) }()
	stdout := make(chan []byte, 1)
	go func() { data, _ := io.ReadAll(io.LimitReader(process.stdout, 16384)); stdout <- data }()
	go func() { _, _ = io.Copy(io.Discard, process.stderr) }()
	select {
	case <-childCtx.Done():
		_ = windows.TerminateJobObject(process.job, 1)
		<-done
		return "", fmt.Errorf("probe timed out or was cancelled")
	case <-done:
	}
	// Kill descendants before waiting for inherited stdout handles to close.
	_ = windows.TerminateJobObject(process.job, 0)
	var exit uint32
	if err := windows.GetExitCodeProcess(process.process, &exit); err != nil || exit != 0 {
		return "", fmt.Errorf("isolated interpreter failed to complete")
	}
	select {
	case data := <-stdout:
		return string(data), nil
	case <-childCtx.Done():
		return "", fmt.Errorf("probe output did not close")
	}
}

func probeProcessIdentity(ctx context.Context, pythonPath, workspace string, d ProbeDefinition) ProbeCase {
	launch, err := probeLaunch(pythonPath, workspace, false, "import time; time.sleep(120)")
	if err != nil {
		return newProbeCase(d, "error", err.Error())
	}
	process, err := startRawProcess(launch)
	if err != nil {
		return newProbeCase(d, "error", err.Error())
	}
	defer process.close()
	defer windows.TerminateJobObject(process.job, 0)
	ok, err := processIsAppContainer(process.process)
	if err != nil {
		return newProbeCase(d, "error", "supervisor could not inspect child token")
	}
	if !ok {
		return newProbeCase(d, "fail", "child token is not AppContainer")
	}
	return newProbeCase(d, "pass", "supervisor verified TokenIsAppContainer on child")
}

func probeJobCleanup(ctx context.Context, pythonPath, workspace string, d ProbeDefinition) ProbeCase {
	launch, err := probeLaunch(pythonPath, workspace, false, ProbeScript(d.Name, ""))
	if err != nil {
		return newProbeCase(d, "error", err.Error())
	}
	process, err := startRawProcess(launch)
	if err != nil {
		return newProbeCase(d, "error", err.Error())
	}
	defer process.close()
	defer windows.TerminateJobObject(process.job, 0)
	if ok, err := processIsAppContainer(process.process); err != nil || !ok {
		return newProbeCase(d, "fail", "cleanup probe is not AppContainer")
	}
	line := make(chan string, 1)
	go func() { s, _ := bufio.NewReader(process.stdout).ReadString('\n'); line <- s }()
	probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var output string
	select {
	case output = <-line:
	case <-probeCtx.Done():
		return newProbeCase(d, "error", "child creation could not be observed")
	}
	o, err := parseProbeObservation(output, d.Name)
	if err != nil || o.Outcome != "success" || o.PID == 0 {
		return newProbeCase(d, "error", "child PID not observed")
	}
	child, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, o.PID)
	if err != nil {
		return newProbeCase(d, "error", "supervisor could not inspect descendant")
	}
	defer windows.CloseHandle(child)
	var member bool
	var inJob uint32
	isProcessInJob := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob")
	callResult, _, _ := isProcessInJob.Call(uintptr(child), uintptr(process.job), uintptr(unsafe.Pointer(&inJob)))
	member = inJob != 0
	if callResult == 0 || !member {
		return newProbeCase(d, "fail", "descendant escaped the job")
	}
	if err := windows.TerminateJobObject(process.job, 0); err != nil {
		return newProbeCase(d, "error", "job termination failed")
	}
	state, err := windows.WaitForSingleObject(child, 5000)
	if err != nil || state != windows.WAIT_OBJECT_0 {
		return newProbeCase(d, "fail", "descendant survived job termination")
	}
	return newProbeCase(d, "pass", "supervisor verified descendant job membership and termination")
}
