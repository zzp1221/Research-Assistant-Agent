// Package native implements the zero-install Windows sandbox provider.
package native

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Runtime describes one resolved Python environment.
type Runtime struct {
	PythonVersion string
	EnvHash       string
	VenvPath      string
	PythonPath    string
	Reused        bool
}

// RuntimeManager manages uv-installed CPython runtimes and per-environment
// virtualenvs. Venvs are cached by envHash so repeated reproductions do not
// reinstall torch.
type RuntimeManager struct {
	UVPath      string
	RuntimeRoot string
	VenvRoot    string
	LogDir      string
	Platform    string
}

func NewRuntimeManager(uvPath, runtimeRoot, venvRoot, logDir string) *RuntimeManager {
	return &RuntimeManager{UVPath: uvPath, RuntimeRoot: runtimeRoot, VenvRoot: venvRoot, LogDir: logDir}
}

// EnvHash is sha256(python version + sorted requirements + platform).
func EnvHash(pythonVersion string, requirements []string, platform string) string {
	sorted := append([]string(nil), requirements...)
	sort.Strings(sorted)
	payload := strings.Join([]string{pythonVersion, strings.Join(sorted, "\n"), platform}, "\n")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func (m *RuntimeManager) platform() string {
	if strings.TrimSpace(m.Platform) != "" {
		return m.Platform
	}
	return runtime.GOOS + "-" + runtime.GOARCH
}

func pythonExecutable(venvPath string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(venvPath, "Scripts", "python.exe")
	}
	return filepath.Join(venvPath, "bin", "python")
}

// EnsureRuntime installs the requested CPython if needed and returns a cached
// venv for the dependency set.
func (m *RuntimeManager) EnsureRuntime(ctx context.Context, pythonVersion string, requirements []string) (Runtime, error) {
	return m.EnsureRuntimeWithProgress(ctx, pythonVersion, requirements, nil)
}

// EnsureRuntimeWithProgress is EnsureRuntime with stage callbacks
// (python/venv/dependencies/freeze/done).
func (m *RuntimeManager) EnsureRuntimeWithProgress(ctx context.Context, pythonVersion string, requirements []string, progress func(string)) (Runtime, error) {
	report := func(stage string) {
		if progress != nil {
			progress(stage)
		}
	}
	pythonVersion = strings.TrimSpace(pythonVersion)
	if pythonVersion == "" {
		pythonVersion = "3.11"
	}
	if strings.TrimSpace(m.UVPath) == "" {
		return Runtime{}, fmt.Errorf("uv executable is not configured")
	}
	hash := EnvHash(pythonVersion, requirements, m.platform())
	venvPath := filepath.Join(m.VenvRoot, hash[:16])
	pythonPath := pythonExecutable(venvPath)
	result := Runtime{PythonVersion: pythonVersion, EnvHash: hash, VenvPath: venvPath, PythonPath: pythonPath}
	if info, err := os.Stat(pythonPath); err == nil && !info.IsDir() {
		result.Reused = true
		report("reused")
		return result, nil
	}
	if err := os.MkdirAll(m.RuntimeRoot, 0o755); err != nil {
		return Runtime{}, fmt.Errorf("create runtime root: %w", err)
	}
	if err := os.MkdirAll(m.VenvRoot, 0o755); err != nil {
		return Runtime{}, fmt.Errorf("create venv root: %w", err)
	}
	report("python")
	if output, err := m.runUV(ctx, "python", "install", pythonVersion); err != nil {
		return Runtime{}, fmt.Errorf("uv python install %s: %w (%s)", pythonVersion, err, output)
	}
	report("venv")
	if output, err := m.runUV(ctx, "venv", venvPath, "--python", pythonVersion); err != nil {
		return Runtime{}, fmt.Errorf("uv venv: %w (%s)", err, output)
	}
	if len(requirements) > 0 {
		report("dependencies")
		reqPath := filepath.Join(m.RuntimeRoot, "requirements-"+hash[:16]+".txt")
		if err := os.WriteFile(reqPath, []byte(strings.Join(requirements, "\n")+"\n"), 0o644); err != nil {
			return Runtime{}, fmt.Errorf("write requirements: %w", err)
		}
		if output, err := m.runUV(ctx, "pip", "install", "-r", reqPath, "--python", pythonPath); err != nil {
			return Runtime{}, fmt.Errorf("uv pip install: %w (%s)", err, output)
		}
	}
	report("freeze")
	freeze, err := m.runUV(ctx, "pip", "freeze", "--python", pythonPath)
	if err == nil {
		_ = os.MkdirAll(m.LogDir, 0o755)
		_ = os.WriteFile(filepath.Join(m.LogDir, "requirements-"+hash[:16]+".lock"), []byte(freeze), 0o644)
	}
	report("done")
	return result, nil
}

func (m *RuntimeManager) runUV(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, m.UVPath, args...)
	cmd.Env = append(os.Environ(), "UV_PYTHON_INSTALL_DIR="+filepath.Join(m.RuntimeRoot, "python"))
	output, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}
