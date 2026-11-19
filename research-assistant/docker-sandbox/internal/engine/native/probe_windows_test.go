//go:build windows

package native

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Uses an offline installed CPython; no downloads, real secrets or user data.
func probePythonFixture(t *testing.T) string {
	t.Helper()
	source := os.Getenv("RA_TEST_PYTHON")
	if source == "" {
		uv, err := exec.LookPath("uv")
		if err != nil {
			t.Fatal("Windows probe integration requires installed uv or RA_TEST_PYTHON")
		}
		output, err := exec.Command(uv, "python", "find", "3.12", "--offline").Output()
		if err != nil {
			t.Fatal("Windows probe integration requires offline CPython 3.12 or RA_TEST_PYTHON")
		}
		source = strings.TrimSpace(string(output))
	}
	root := t.TempDir()
	install := filepath.Join(root, "python")
	sourceRoot := filepath.Dir(source)
	sourceRoot, err := canonicalEnvironmentPath(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			return statErr
		}
		if info.IsDir() {
			if entry.Name() == "site-packages" || entry.Name() == "__pycache__" || entry.Name() == "tcl" || entry.Name() == "include" || entry.Name() == "libs" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(install, rel), 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		out, err := os.Create(filepath.Join(install, rel))
		if err != nil {
			in.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		closeErr := out.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
	if err != nil {
		t.Fatal(err)
	}
	python := filepath.Join(install, filepath.Base(source))
	python, err = prepareAppContainerRuntimeCopy(python, filepath.Join(root, "appcontainer"), root)
	if err != nil {
		t.Fatal(err)
	}
	return python
}

func TestProbeWindowsRealIsolationAndHonestNetworkResults(t *testing.T) {
	python := probePythonFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	r := runProbeWithEndpoint(ctx, python, t.TempDir(), listener.Addr().String())
	for _, c := range r.Cases {
		t.Logf("case=%s status=%s expected=%s profile=%s detail=%s", c.Name, c.Status, c.Expected, c.Profile, c.Detail)
		if c.Name == "network_online" || c.Name == "network_connect" {
			// Standard Windows blocks AppContainer loopback even online. The
			// report must retain uncertainty rather than pretending this is a
			// successful positive control or weakening the machine policy.
			if c.Status != "pass" && c.Status != "inconclusive" {
				t.Fatalf("unexpected network control: %#v", c)
			}
			continue
		}
		if (c.Name == "python_stdlib" || c.Name == "multiprocessing") && c.Status == "inconclusive" {
			// CPython's private TemporaryDirectory DACL and multiprocessing's
			// named pipes can deny this real token. Preserve the incompatibility;
			// do not loosen ACLs or substitute a successful host execution.
			if r.AppContainerOK {
				t.Fatal("compatibility failure was treated as verified isolation")
			}
			continue
		}
		if !c.Passed {
			t.Errorf("required real isolation assertion did not pass: %#v", c)
		}
	}
	if len(r.Cases) != len(ProbeDefinitions()) {
		t.Fatal("real probe omitted required cases")
	}
	if r.Inconclusive > 0 && r.AppContainerOK {
		t.Fatal("uncertainty counted as safety")
	}
	if _, err := ResolveIsolation(IsolationAppContainer, &r, false); r.Inconclusive > 0 && err == nil {
		t.Fatal("strong isolation request accepted unverified report")
	}
}

func TestProbeWindowsIgnoresUntrustedPythonStartupHooks(t *testing.T) {
	python := probePythonFixture(t)
	venv := filepath.Join(filepath.Dir(filepath.Dir(python)), "hostile-venv")
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	create := exec.CommandContext(ctx, python, "-I", "-S", "-B", "-m", "venv", "--without-pip", "--copies", venv)
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create test-owned venv: %v (%s)", err, output)
	}
	sitePackages := filepath.Join(venv, "Lib", "site-packages")
	for name, content := range map[string]string{
		"observer.pth":     "import builtins; builtins.print('untrusted-pth-hook')\n",
		"sitecustomize.py": "print('untrusted-site-hook')\n",
	} {
		if err := os.WriteFile(filepath.Join(sitePackages, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	venvPython := filepath.Join(venv, "Scripts", "python.exe")
	control := exec.CommandContext(ctx, venvPython, "-c", "print('host-positive-control')")
	controlOutput, err := control.CombinedOutput()
	if err != nil || !strings.Contains(string(controlOutput), "untrusted-pth-hook") || !strings.Contains(string(controlOutput), "untrusted-site-hook") {
		t.Fatalf("synthetic startup hooks did not execute in the positive control: %v (%s)", err, controlOutput)
	}
	venvPython, err = prepareAppContainerRuntimeCopy(venvPython, t.TempDir(), venv, filepath.Dir(python))
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	if err := prepareAppContainerWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	script := `import json, sys; print(json.dumps({'site_loaded': 'site' in sys.modules, 'isolated': sys.flags.isolated, 'no_site': sys.flags.no_site}))`
	output, err := runAppContainerProbe(ctx, venvPython, workspace, false, script)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		SiteLoaded bool `json:"site_loaded"`
		Isolated   int  `json:"isolated"`
		NoSite     int  `json:"no_site"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil || result.SiteLoaded || result.Isolated != 1 || result.NoSite != 1 {
		t.Fatalf("security observer loaded untrusted site startup code: %v (%s)", err, output)
	}
	t.Log("real AppContainer observer ignored synthetic .pth/sitecustomize hooks; ordinary Python positive control executed both")
}

func TestProbeWindowsIPv6EndpointScriptConnects(t *testing.T) {
	python := probePythonFixture(t)
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Fatal("controlled IPv6 loopback endpoint unavailable: ", err)
	}
	defer listener.Close()
	accepted := make(chan error, 2)
	go func() {
		for range 2 {
			connection, err := listener.Accept()
			if err == nil {
				connection.Close()
			}
			accepted <- err
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if err := probeEndpointReachable(ctx, listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	// This host execution checks the shared endpoint parser, not isolation.
	// AppContainer's loopback policy remains independently tested above.
	command := exec.CommandContext(ctx, python, probePythonArgv(python, ProbeScript("network_online", listener.Addr().String()))[1:]...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("controlled IPv6 script: %v (%s)", err, output)
	}
	observation, err := parseProbeObservation(string(output), "network_online")
	if err != nil || observation.Outcome != "success" {
		t.Fatalf("bracketed IPv6 endpoint did not connect: %#v (%v)", observation, err)
	}
	for range 2 {
		select {
		case err := <-accepted:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("host and script IPv6 connections were not both observed")
		}
	}
}

func TestProbeWindowsUnavailableEndpointCannotPass(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if probeEndpointReachable(ctx, "") == nil || probeEndpointReachable(ctx, "not-an-endpoint") == nil {
		t.Fatal("missing positive control accepted")
	}
}

func TestAppContainerRuntimeCopyIsReadOnlyToTask(t *testing.T) {
	python := probePythonFixture(t)
	workspace := t.TempDir()
	if err := prepareAppContainerWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	script := `import os, sys, json, ctypes
from ctypes import wintypes
kernel = ctypes.WinDLL('kernel32', use_last_error=True)
kernel.CreateFileW.restype = wintypes.HANDLE
kernel.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD, wintypes.LPVOID, wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
try:
    handle = kernel.CreateFileW(sys.executable, 0x40000000, 7, None, 3, 0x80, None)
    if handle == wintypes.HANDLE(-1).value:
        raise ctypes.WinError(ctypes.get_last_error())
except OSError as exc:
    print(json.dumps({'denied': getattr(exc, 'winerror', 0) == 5, 'winerror': getattr(exc, 'winerror', 0), 'errno': exc.errno}))
else:
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel.CloseHandle(handle)
    print(json.dumps({'denied': False}))`
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	output, err := runAppContainerProbe(ctx, python, workspace, false, script)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Denied bool `json:"denied"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil || !result.Denied {
		t.Fatalf("task could write the service-owned runtime copy: %v (%s)", err, output)
	}
}
