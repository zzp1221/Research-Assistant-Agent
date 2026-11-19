package native

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const ProbeVersion = "native.isolation-probe/v2"

// Bump when the runner's environment, ACL, Job or network policy changes.
const probeRunnerVersion = "native-launch/v3"

type ProbeDefinition struct {
	Name     string
	Security bool
	Expected string
	Profile  string
}

// Core probes use only the standard library. Scientific-package compatibility
// is a property of each research runtime, not a prerequisite for OS isolation.
func ProbeDefinitions() []ProbeDefinition {
	return []ProbeDefinition{
		{Name: "python_stdlib", Expected: "allow", Profile: "offline"},
		{Name: "multiprocessing", Expected: "allow", Profile: "offline"},
		{Name: "workspace_write", Expected: "allow", Profile: "offline"},
		{Name: "process_identity", Security: true, Expected: "appcontainer", Profile: "offline"},
		{Name: "workspace_escape", Security: true, Expected: "deny", Profile: "offline"},
		{Name: "network_connect", Security: true, Expected: "deny", Profile: "offline"},
		{Name: "network_online", Security: true, Expected: "allow", Profile: "online"},
		{Name: "job_cleanup", Security: true, Expected: "cleanup", Profile: "offline"},
	}
}

func newProbeCase(d ProbeDefinition, status, detail string) ProbeCase {
	return ProbeCase{Name: d.Name, Security: d.Security, Expected: d.Expected,
		Profile: d.Profile, Status: status, Passed: status == "pass", Detail: detail}
}

// Hash the interpreter and environment contents, not only their timestamps.
// Switching runtime, packages, profiles or policy invalidates previous results.
func ProbeEnvironmentKey(pythonPath, environmentHash, configuration string) (string, error) {
	abs, err := filepath.Abs(pythonPath)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	encoder := json.NewEncoder(h)
	// JSON records frame names and values independently. Concatenating raw
	// paths and file bytes permits two different file sets to hash identically.
	if err := encoder.Encode(struct {
		Format, Probe, Runner, Platform, Architecture, Interpreter, Environment, Configuration, Offline, Online string
	}{"native.environment-key/v2", ProbeVersion, probeRunnerVersion, probePlatformKey(), runtime.GOARCH,
		filepath.Clean(abs), environmentHash, configuration, AppContainerOfflineProfile, AppContainerOnlineProfile}); err != nil {
		return "", err
	}
	root := filepath.Dir(abs)
	if strings.EqualFold(filepath.Base(root), "Scripts") || filepath.Base(root) == "bin" {
		root = filepath.Dir(root)
	}
	roots := []string{root}
	if config, err := os.ReadFile(filepath.Join(root, "pyvenv.cfg")); err == nil {
		for _, line := range strings.Split(string(config), "\n") {
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "home" {
				roots = append(roots, strings.TrimSpace(value))
			}
		}
	}
	files := []string{}
	seenRoots := map[string]bool{}
	for _, root := range roots {
		root, err = canonicalEnvironmentPath(root)
		if err != nil {
			return "", err
		}
		if seenRoots[root] {
			continue
		}
		seenRoots[root] = true
		err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&(os.ModeSymlink|os.ModeIrregular) != 0 {
				return fmt.Errorf("runtime probe refuses a symbolic environment entry")
			}
			if entry.IsDir() {
				if entry.Name() == ".ra-env" {
					return filepath.SkipDir
				}
				return nil
			}
			files = append(files, path)
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Strings(files)
	for _, path := range files {
		f, err := os.Open(path)
		if err != nil {
			return "", err
		}
		contentHash := sha256.New()
		size, copyErr := io.Copy(contentHash, f)
		closeErr := f.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		if err := encoder.Encode(struct {
			Path   string
			Size   int64
			SHA256 string
		}{filepath.Clean(path), size, hex.EncodeToString(contentHash.Sum(nil))}); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func probePythonArgv(pythonPath, script string) []string {
	// No site-packages, .pth/sitecustomize, PYTHON* injection or current-directory
	// imports may influence the security observer. -B avoids warming a shared
	// runtime's bytecode cache while probing; existing bytecode remains hashed.
	return []string{pythonPath, "-I", "-S", "-B", "-c", script}
}

func splitProbeEndpoint(endpoint string) (string, int, error) {
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" {
		return "", 0, fmt.Errorf("expected host:port, with brackets around IPv6 addresses")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("expected a numeric TCP port between 1 and 65535")
	}
	return host, port, nil
}

type probeObservation struct {
	Operation string `json:"operation"`
	Outcome   string `json:"outcome"`
	Errno     int    `json:"errno"`
	Winerror  int    `json:"winerror"`
	PID       uint32 `json:"pid"`
	ErrorType string `json:"error_type"`
}

func parseProbeObservation(output, operation string) (probeObservation, error) {
	var o probeObservation
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 1 || json.Unmarshal([]byte(lines[0]), &o) != nil || o.Operation != operation {
		return o, fmt.Errorf("probe returned an incomplete or ambiguous observation")
	}
	if o.Outcome != "success" && o.Outcome != "denied" && o.Outcome != "error" {
		return o, fmt.Errorf("probe returned an unknown outcome")
	}
	return o, nil
}

func evaluateObservation(d ProbeDefinition, o probeObservation) ProbeCase {
	if o.Operation != d.Name {
		return newProbeCase(d, "error", "observation belongs to another probe")
	}
	if d.Expected == "deny" {
		if o.Outcome == "success" {
			return newProbeCase(d, "fail", "operation succeeded despite the isolation policy")
		}
		// A timeout or unavailable target is not evidence of OS network denial.
		if o.Outcome == "denied" && (o.Winerror == 5 || o.Winerror == 10013) {
			return newProbeCase(d, "pass", "OS explicitly denied the controlled operation")
		}
		return newProbeCase(d, "inconclusive", fmt.Sprintf("operation failed without an attributable OS access denial (winerror=%d errno=%d type=%s)", o.Winerror, o.Errno, o.ErrorType))
	}
	if o.Outcome == "success" {
		return newProbeCase(d, "pass", "controlled operation completed")
	}
	return newProbeCase(d, "inconclusive", fmt.Sprintf("required allowed operation could not be established (winerror=%d errno=%d type=%s)", o.Winerror, o.Errno, o.ErrorType))
}

// Each operation runs in a fresh isolated process. Only the trusted supervisor
// evaluates the observation; scripts never declare the isolation safe.
func ProbeScript(operation, target string) string {
	quotedOperation, _ := json.Marshal(operation)
	quotedTarget, _ := json.Marshal(target)
	networkHost, networkPort, endpointErr := splitProbeEndpoint(target)
	quotedHost, _ := json.Marshal(networkHost)
	networkTargetValid := "False"
	if endpointErr == nil {
		networkTargetValid = "True"
	}
	return fmt.Sprintf(`import json, os, sys
operation = %s
target = %s
network_host = %s
network_port = %d
network_target_valid = %s
def main():
    result = {'operation': operation, 'outcome': 'success'}
    try:
        if operation == 'python_stdlib':
            import hashlib, ssl, tempfile
            assert hashlib.sha256(b'probe').hexdigest()
            with tempfile.TemporaryFile() as f:
                f.write(b'probe')
            with tempfile.TemporaryDirectory() as directory:
                path = os.path.join(directory, '科研数据.txt')
                with open(path, 'w', encoding='utf-8') as f:
                    f.write('isolated-temp')
                with open(path, encoding='utf-8') as f:
                    assert f.read() == 'isolated-temp'
        elif operation == 'multiprocessing':
            import multiprocessing as m
            with m.get_context('spawn').Pool(2) as pool:
                assert pool.map(len, ['a', 'bb']) == [1, 2]
        elif operation in ('workspace_write', 'workspace_escape'):
            import ctypes
            from ctypes import wintypes
            kernel = ctypes.WinDLL('kernel32', use_last_error=True)
            kernel.CreateFileW.restype = wintypes.HANDLE
            kernel.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, wintypes.LPVOID, wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
            handle = kernel.CreateFileW(target, 0x40000000, 7, None, 2, 0x80, None)
            if handle == wintypes.HANDLE(-1).value:
                raise ctypes.WinError(ctypes.get_last_error())
            kernel.CloseHandle.argtypes = [wintypes.HANDLE]
            kernel.CloseHandle(handle)
            with open(target, 'w', encoding='utf-8') as f:
                f.write('isolated-probe')
        elif operation in ('network_connect', 'network_online'):
            import socket
            if not network_target_valid:
                raise ValueError('invalid controlled network endpoint')
            with socket.create_connection((network_host, network_port), timeout=3):
                pass
        elif operation == 'job_cleanup':
            import subprocess, time
            child = subprocess.Popen([sys.executable, '-I', '-S', '-B', '-c', 'import time; time.sleep(120)'])
            result['pid'] = child.pid
            print(json.dumps(result), flush=True)
            time.sleep(120)
            return
        else:
            raise ValueError('unknown probe operation')
    except OSError as exc:
        result.update(outcome='denied' if getattr(exc, 'winerror', 0) in (5,10013) else 'error',
                      errno=exc.errno or 0, winerror=getattr(exc, 'winerror', 0) or 0, error_type=type(exc).__name__)
    except Exception as exc:
        result.update(outcome='error', error_type=type(exc).__name__)
    print(json.dumps(result), flush=True)
if __name__ == '__main__':
    main()
`, quotedOperation, quotedTarget, quotedHost, networkPort, networkTargetValid)
}
