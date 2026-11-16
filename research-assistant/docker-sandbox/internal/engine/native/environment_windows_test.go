//go:build windows

package native

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"docker-sandbox/internal/engine"

	"golang.org/x/sys/windows"
)

func nativeFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "研究工作区")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(workspace, "fixture.exe")
	if err := os.WriteFile(executable, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return workspace, executable
}

func TestNativeEnvironmentRealLowILAndAppContainer(t *testing.T) {
	syntheticSecrets(t)
	for _, isolation := range []string{IsolationLowIL, IsolationAppContainer} {
		t.Run(isolation, func(t *testing.T) {
			workspace, executable := nativeFixture(t)
			if isolation == IsolationAppContainer {
				if err := prepareAppContainerWorkspace(workspace); err != nil {
					t.Fatal(err)
				}
			}
			runner := &windowsRunner{requested: isolation}
			spec := runSpec{Argv: []string{executable, "native-env-check"}, Cwd: workspace, EnvironmentRoot: workspace,
				Isolation: isolation, Timeout: 10 * time.Second, Env: map[string]string{"SEED": "42"}}
			sawStream := false
			spec.Emit = func(event engine.Event) error {
				if event.Message == "safe-environment" {
					sawStream = true
				}
				return nil
			}
			result, err := runner.Run(context.Background(), spec)
			if err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "safe-environment" || !sawStream {
				t.Logf("synthetic fixture diagnostic: %s", result.Stdout)
				t.Fatalf("actual runner %s failed the environment/temp/stream assertions (exit=%d)", isolation, result.ExitCode)
			}
			t.Logf("actual runner=%s; inherited-secret=false; private-temp-writable=true; streamed=true", isolation)
		})
	}
}

func TestNativeEnvironmentBackupExec(t *testing.T) {
	syntheticSecrets(t)
	workspace, executable := nativeFixture(t)
	result, err := runExecProcess(context.Background(), runSpec{Argv: []string{executable, "native-env-check"}, Cwd: workspace, EnvironmentRoot: workspace, Timeout: 10 * time.Second})
	if err != nil || result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "safe-environment" {
		t.Fatal("backup exec did not enforce the same environment policy")
	}
}

func TestNativeEnvironmentRejectedBeforeProcessStarts(t *testing.T) {
	workspace, executable := nativeFixture(t)
	runner := &windowsRunner{requested: IsolationLowIL}
	for _, env := range []map[string]string{{"SANDBOX_API_TOKEN": "synthetic"}, {"Path": workspace}, {"PYTHONPATH": t.TempDir()}, {"SEED": "1", "seed": "2"}} {
		_, err := runner.Run(t.Context(), runSpec{Argv: []string{executable, "native-env-check"}, Cwd: workspace, EnvironmentRoot: workspace, Isolation: IsolationLowIL, Env: env})
		if err == nil {
			t.Fatal("untrusted task environment reached the process")
		}
		if _, err := os.Stat(filepath.Join(workspace, ".ra-env", "tmp", "env-ok.txt")); err == nil {
			t.Fatal("rejected command still executed")
		}
	}
}

func TestRawProcessCancellationDoesNotWaitForPipeEOF(t *testing.T) {
	workspace, executable := nativeFixture(t)
	runner := &windowsRunner{requested: IsolationLowIL}
	started := time.Now()
	result, err := runner.Run(t.Context(), runSpec{Argv: []string{executable, "native-env-sleep"}, Cwd: workspace, EnvironmentRoot: workspace, Isolation: IsolationLowIL, Timeout: 250 * time.Millisecond})
	if err != nil || result.ExitCode == 0 {
		t.Fatal("cancelled silent process must terminate with a nonzero result")
	}
	if time.Since(started) > 5*time.Second {
		t.Fatal("cancellation waited for the child's pipe EOF")
	}
}

func TestPythonPathRejectsWindowsJunctionEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "junction")
	cmd := exec.Command("cmd.exe", "/c", "mklink", "/J", link, outside)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create test-owned junction: %v (%s)", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(link) })
	if _, err := translatePythonPath(workspace, "/workspace/junction/new"); err == nil {
		t.Fatal("logical PYTHONPATH escaped through a Windows junction")
	}
	if _, err := researchEnvironment(workspace, "", map[string]string{"PYTHONPATH": filepath.Join(link, "new")}); err == nil {
		t.Fatal("final process PYTHONPATH escaped through a Windows junction")
	}
	if _, err := TranslateCwd(workspace, "/workspace/junction"); err == nil {
		t.Fatal("working directory escaped through a Windows junction")
	}
}

func nativePythonFixture(t *testing.T, isolation string) string {
	t.Helper()
	source := probePythonFixture(t)
	if isolation == IsolationAppContainer {
		return source
	}
	// The security probe's runtime has AppContainer profile ACEs. Those ACEs
	// deny reads from an ordinary low-integrity token on Windows. Copy bytes
	// into a separate fixture with its own inherited ACL, as a lowil runtime
	// would be installed; never remove profile grants from an existing runtime.
	root := filepath.Join(t.TempDir(), "python")
	sourceRoot := filepath.Dir(source)
	if err := filepath.WalkDir(sourceRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(root, rel), 0o700)
		}
		if !entry.Type().IsRegular() {
			return os.ErrInvalid
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(root, rel), data, 0o600)
	}); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, filepath.Base(source))
}

func TestLowIntegrityWorkspaceDoesNotRelabelExternalObjects(t *testing.T) {
	workspace, executable := nativeFixture(t)
	outside := t.TempDir()
	external := filepath.Join(outside, "external.txt")
	if err := os.WriteFile(external, []byte("host-positive-control"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "external-link.txt")
	if err := os.Link(external, link); err != nil {
		t.Fatal(err)
	}
	junction := filepath.Join(workspace, "external-directory")
	if output, err := exec.Command("cmd.exe", "/c", "mklink", "/J", junction, outside).CombinedOutput(); err != nil {
		t.Fatalf("create test-owned junction: %v (%s)", err, output)
	}
	t.Cleanup(func() { _ = os.Remove(junction) })
	security := func(path string, information windows.SECURITY_INFORMATION) string {
		t.Helper()
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, information)
		if err != nil {
			t.Fatal(err)
		}
		return sd.String()
	}
	beforeFile := security(external, windows.LABEL_SECURITY_INFORMATION)
	beforeDirectory := security(outside, windows.LABEL_SECURITY_INFORMATION)
	beforeDACL := security(workspace, windows.DACL_SECURITY_INFORMATION)
	if err := prepareLowIntegrityWorkspace(workspace); err != nil {
		t.Fatal(err)
	}
	if security(external, windows.LABEL_SECURITY_INFORMATION) != beforeFile || security(outside, windows.LABEL_SECURITY_INFORMATION) != beforeDirectory {
		t.Fatalf("workspace labeling changed an external hardlink or junction target: file=%q -> %q dir=%q -> %q", beforeFile, security(external, windows.LABEL_SECURITY_INFORMATION), beforeDirectory, security(outside, windows.LABEL_SECURITY_INFORMATION))
	}
	if security(workspace, windows.DACL_SECURITY_INFORMATION) != beforeDACL {
		t.Fatal("workspace labeling changed the discretionary ACL")
	}
	runner := &windowsRunner{requested: IsolationLowIL}
	output := filepath.Join(workspace, "new-output.txt")
	for _, check := range []struct {
		path string
		exit int
	}{
		{output, 0},
		{link, 95},
		{filepath.Join(junction, "external.txt"), 95},
	} {
		result, err := runner.Run(t.Context(), runSpec{Argv: []string{executable, "native-workspace-write-check", check.path}, Cwd: workspace,
			EnvironmentRoot: workspace, Isolation: IsolationLowIL, Timeout: 10 * time.Second})
		if err != nil || result.ExitCode != check.exit {
			t.Fatalf("actual lowil workspace write exit=%d result=%+v err=%v", check.exit, result, err)
		}
	}
	if data, err := os.ReadFile(output); err != nil || string(data) != "low-workspace-output" {
		t.Fatal("lowil did not create the new workspace output")
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "host-positive-control" {
		t.Fatal("lowil modified the external host object")
	}
	if err := prepareLowIntegrityWorkspace(junction); err == nil {
		t.Fatal("reparse workspace root was relabeled")
	}
	if security(external, windows.LABEL_SECURITY_INFORMATION) != beforeFile || security(outside, windows.LABEL_SECURITY_INFORMATION) != beforeDirectory {
		t.Fatal("rejected reparse root changed external labels")
	}
}

func TestResearchEnvironmentDoesNotRelabelHardlinkedHostObject(t *testing.T) {
	workspace, executable := nativeFixture(t)
	cache := filepath.Join(workspace, ".ra-env", "tmp")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	external := filepath.Join(t.TempDir(), "host-file.txt")
	if err := os.WriteFile(external, []byte("host-positive-control"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cache, "host-link.txt")
	if err := os.Link(external, link); err != nil {
		t.Fatal(err)
	}
	before, err := windows.GetNamedSecurityInfo(external, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := researchEnvironment(workspace, "", nil); err != nil {
		t.Fatal(err)
	}
	after, err := windows.GetNamedSecurityInfo(external, windows.SE_FILE_OBJECT, windows.LABEL_SECURITY_INFORMATION)
	if err != nil || before.String() != after.String() {
		t.Fatal("private environment preparation relabeled an external hardlinked host file")
	}
	runner := &windowsRunner{requested: IsolationLowIL}
	result, err := runner.Run(t.Context(), runSpec{Argv: []string{executable, "native-workspace-write-check", link}, Cwd: workspace,
		EnvironmentRoot: workspace, Isolation: IsolationLowIL, Timeout: 10 * time.Second})
	if err != nil || result.ExitCode != 95 {
		t.Fatalf("lowil wrote an existing host file through a cache hardlink: result=%+v err=%v", result, err)
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "host-positive-control" {
		t.Fatal("host positive control was modified")
	}
}

func TestNativePythonScientificBasicsWithPrivateEnvironment(t *testing.T) {
	syntheticSecrets(t)
	for _, isolation := range []string{IsolationLowIL, IsolationAppContainer} {
		t.Run(isolation, func(t *testing.T) {
			python := nativePythonFixture(t, isolation)
			workspace := filepath.Join(t.TempDir(), "中文科研路径")
			if err := os.Mkdir(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			if isolation == IsolationAppContainer {
				if err := prepareAppContainerWorkspace(workspace); err != nil {
					t.Fatal(err)
				}
			} else if err := prepareLowIntegrityWorkspace(workspace); err != nil {
				t.Fatal(err)
			}
			script := `import os, sys, tempfile, subprocess, venv, json, pathlib
assert all(k not in os.environ for k in ('SANDBOX_API_TOKEN', 'API_AUTH_TOKEN', 'NATIVE_UNKNOWN_SENTINEL', 'OPENAI_API_KEY'))
assert os.environ['PYTHONNOUSERSITE'] == '1'
assert os.environ['OMP_NUM_THREADS'] == '2'
with tempfile.TemporaryFile() as f:
    f.write(b'safe-temp')
p = pathlib.Path(os.environ['TMP']) / '中文数据.json'
p.write_text(json.dumps({'x': 42}), encoding='utf-8')
assert json.loads(p.read_text(encoding='utf-8'))['x'] == 42
venv.EnvBuilder(with_pip=False, symlinks=False).create(str(pathlib.Path(os.environ['TMP']) / 'venv'))
child = subprocess.run([sys.executable, '-c', "import os; assert 'SANDBOX_API_TOKEN' not in os.environ; print('child-ok')"], capture_output=True, text=True, check=True)
assert child.stdout.strip() == 'child-ok'
print('scientific-basics-ok')`
			runner := &windowsRunner{requested: isolation}
			result, err := runner.Run(t.Context(), runSpec{Argv: []string{python, "-c", script}, Cwd: workspace,
				EnvironmentRoot: workspace, Interpreter: python, Isolation: isolation, Timeout: 30 * time.Second, Env: map[string]string{"OMP_NUM_THREADS": "2"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "scientific-basics-ok" {
				t.Fatalf("Python basics failed in %s (exit=%d): %s", isolation, result.ExitCode, result.Stderr)
			}
			t.Logf("actual runner=%s; imports, venv, private-temp, subprocess and Chinese paths passed", isolation)
		})
	}
}

func TestNativeScientificPlotWithPrivateEnvironment(t *testing.T) {
	if os.Getenv("RA_TEST_SCIENTIFIC") != "1" {
		t.Skip("set RA_TEST_SCIENTIFIC=1 for test-owned numpy/matplotlib wheel installation and real offline plotting")
	}
	syntheticSecrets(t)
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Fatal("scientific integration requires installed uv")
	}
	for _, isolation := range []string{IsolationLowIL, IsolationAppContainer} {
		t.Run(isolation, func(t *testing.T) {
			// Install before granting profile RX, into independent runtimes so
			// neither mode's ACL can contaminate the other mode's result.
			basePython := nativePythonFixture(t, IsolationLowIL)
			root := t.TempDir()
			venvPath := filepath.Join(root, "scientific-venv")
			python := pythonExecutable(venvPath)
			manager := NewRuntimeManager(uv, filepath.Join(root, "install"), filepath.Join(root, "venvs"), filepath.Join(root, "logs"))
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
			defer cancel()
			if output, err := manager.runUV(ctx, "venv", venvPath, "--python", basePython, "--no-python-downloads"); err != nil {
				t.Fatalf("create test-owned scientific venv: %v (%s)", err, output)
			}
			if output, err := manager.runUV(ctx, "pip", "install", "--python", python, "--index-url", "https://pypi.org/simple", "--only-binary=:all:", "numpy==2.2.6", "matplotlib==3.10.8"); err != nil {
				t.Fatalf("install test-owned scientific wheels: %v (%s)", err, output)
			}
			workspace := filepath.Join(t.TempDir(), "离线科研绘图")
			if err := os.Mkdir(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			if isolation == IsolationAppContainer {
				python, err = prepareAppContainerRuntimeCopy(python, t.TempDir(), venvPath, filepath.Dir(basePython))
				if err != nil {
					t.Fatal(err)
				}
				if err := prepareAppContainerWorkspace(workspace); err != nil {
					t.Fatal(err)
				}
			} else if err := prepareLowIntegrityWorkspace(workspace); err != nil {
				t.Fatal(err)
			}
			script := `import os, sys, pathlib, tempfile, subprocess, venv, json
import numpy as np
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
assert all(k not in os.environ for k in ('SANDBOX_API_TOKEN', 'API_AUTH_TOKEN', 'IDENTITY_COOKIE_SECRET', 'OPENAI_API_KEY', 'HTTP_PROXY', 'PYTHONSTARTUP', 'NATIVE_UNKNOWN_SENTINEL'))
assert os.environ['PYTHONNOUSERSITE'] == '1'
assert os.environ['OMP_NUM_THREADS'] == '2'
assert os.environ['MPLBACKEND'] == 'Agg'
with tempfile.TemporaryFile() as f:
    f.write(b'safe-temp')
    f.seek(0)
    assert f.read() == b'safe-temp'
data = pathlib.Path(os.environ['TMP']) / '中文实验.json'
data.write_text(json.dumps({'mean': float(np.mean([1, 2, 3]))}), encoding='utf-8')
assert json.loads(data.read_text(encoding='utf-8'))['mean'] == 2.0
venv.EnvBuilder(with_pip=False, symlinks=False).create(str(pathlib.Path(os.environ['TMP']) / 'child-venv'))
child = subprocess.run([sys.executable, '-c', "import numpy, os; assert 'SANDBOX_API_TOKEN' not in os.environ; print('scientific-child-ok')"], capture_output=True, text=True, check=True)
assert child.stdout.strip() == 'scientific-child-ok'
x = np.linspace(0, 2 * np.pi, 50)
fig, ax = plt.subplots()
ax.plot(x, np.sin(x))
image = pathlib.Path.cwd() / '离线结果.png'
fig.savefig(image)
plt.close(fig)
assert image.read_bytes().startswith(b'\x89PNG\r\n\x1a\n')
assert image.stat().st_size > 1000
print('scientific-plot-ok')`
			runner := &windowsRunner{requested: isolation}
			result, err := runner.Run(ctx, runSpec{Argv: []string{python, "-c", script}, Cwd: workspace,
				EnvironmentRoot: workspace, Interpreter: python, Isolation: isolation, Timeout: time.Minute, Env: map[string]string{"OMP_NUM_THREADS": "2"}})
			if err != nil {
				t.Fatal(err)
			}
			if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != "scientific-plot-ok" {
				t.Fatalf("scientific workflow failed in %s (exit=%d): %s", isolation, result.ExitCode, result.Stderr)
			}
			t.Logf("actual runner=%s; numpy=2.2.6; matplotlib=3.10.8; private-temp/venv/subprocess/Chinese-path/Agg-PNG=passed; inherited-secret=false", isolation)
		})
	}
}
