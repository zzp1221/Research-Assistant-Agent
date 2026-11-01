package native

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// RuntimeStatus reports installed Python runtimes and cached virtualenvs.
type RuntimeStatus struct {
	Supported      bool     `json:"supported"`
	Provider       string   `json:"provider"`
	PythonVersions []string `json:"python_versions,omitempty"`
	VenvCount      int      `json:"venv_count"`
	DiskUsedMB     float64  `json:"disk_used_mb"`
	RuntimeRoot    string   `json:"runtime_root,omitempty"`
	VenvRoot       string   `json:"venv_root,omitempty"`
	Installing     bool     `json:"installing,omitempty"`
	Stage          string   `json:"stage,omitempty"`
	InstallError   string   `json:"install_error,omitempty"`
}

// RuntimeStatus scans the uv runtime and venv cache directories.
func (p *Provider) RuntimeStatus(context.Context) (RuntimeStatus, error) {
	status := ScanRuntimeStatus(p.runtimes.RuntimeRoot, p.runtimes.VenvRoot)
	p.installMu.Lock()
	status.Installing = p.installing
	status.Stage = p.installStage
	status.InstallError = p.installError
	p.installMu.Unlock()
	return status, nil
}

// InstallRuntime ensures a CPython runtime/venv exists and returns the updated
// cache status. The actual download is delegated to uv.
func (p *Provider) InstallRuntime(ctx context.Context, pythonVersion string) (RuntimeStatus, error) {
	p.installMu.Lock()
	p.installing = true
	p.installStage = "starting"
	p.installError = ""
	p.installMu.Unlock()
	_, err := p.runtimes.EnsureRuntimeWithProgress(ctx, pythonVersion, nil, func(stage string) {
		p.installMu.Lock()
		p.installStage = stage
		p.installMu.Unlock()
	})
	p.installMu.Lock()
	p.installing = false
	if err != nil {
		p.installStage = "error"
		p.installError = err.Error()
	} else {
		p.installStage = "done"
	}
	p.installMu.Unlock()
	if err != nil {
		return RuntimeStatus{}, err
	}
	return p.RuntimeStatus(ctx)
}

// ScanRuntimeStatus is the pure scanner used by tests and the provider.
func ScanRuntimeStatus(runtimeRoot, venvRoot string) RuntimeStatus {
	status := RuntimeStatus{Supported: true, Provider: "native", RuntimeRoot: runtimeRoot, VenvRoot: venvRoot}
	versions := listDirectories(filepath.Join(runtimeRoot, "python"))
	if len(versions) == 0 {
		versions = listDirectories(runtimeRoot)
	}
	status.PythonVersions = versions
	status.VenvCount = len(listDirectories(venvRoot))
	var total int64
	for _, root := range []string{runtimeRoot, venvRoot} {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			if info, statErr := entry.Info(); statErr == nil {
				total += info.Size()
			}
			return nil
		})
	}
	status.DiskUsedMB = float64(total) / 1024 / 1024
	return status
}

func listDirectories(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
