package shell

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RuntimeStatusView is the native runtime cache summary.
type RuntimeStatusView struct {
	Supported      bool     `json:"supported"`
	Provider       string   `json:"provider,omitempty"`
	PythonVersions []string `json:"python_versions,omitempty"`
	VenvCount      int      `json:"venv_count,omitempty"`
	DiskUsedMB     float64  `json:"disk_used_mb,omitempty"`
	Installing     bool     `json:"installing,omitempty"`
	Stage          string   `json:"stage,omitempty"`
	InstallError   string   `json:"install_error,omitempty"`
	Error          string   `json:"error,omitempty"`
}

// FetchRuntimeStatus reads the sandbox runtime status endpoint.
func (s *Shell) FetchRuntimeStatus(ctx context.Context) (RuntimeStatusView, error) {
	s.mu.RLock()
	sandboxURL := s.status.SandboxURL
	token := s.sandboxTok
	s.mu.RUnlock()
	if strings.TrimSpace(sandboxURL) == "" {
		return RuntimeStatusView{}, fmt.Errorf("sandbox is not running")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(sandboxURL, "/")+"/api/v1/runtime/status", nil)
	if err != nil {
		return RuntimeStatusView{}, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return RuntimeStatusView{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return RuntimeStatusView{}, fmt.Errorf("runtime status returned %s", response.Status)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return RuntimeStatusView{}, err
	}
	var status RuntimeStatusView
	if err := json.Unmarshal(body, &status); err != nil {
		return RuntimeStatusView{}, err
	}
	return status, nil
}

// InstallRuntime asks the sandbox to prepare a Python runtime and returns the
// updated cache status.
func (s *Shell) InstallRuntime(ctx context.Context, pythonVersion string) (RuntimeStatusView, error) {
	s.mu.RLock()
	sandboxURL := s.status.SandboxURL
	token := s.sandboxTok
	s.mu.RUnlock()
	if strings.TrimSpace(sandboxURL) == "" {
		return RuntimeStatusView{}, fmt.Errorf("sandbox is not running")
	}
	payload, _ := json.Marshal(map[string]string{"python_version": strings.TrimSpace(pythonVersion)})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(sandboxURL, "/")+"/api/v1/runtime/install", bytes.NewReader(payload))
	if err != nil {
		return RuntimeStatusView{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return RuntimeStatusView{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return RuntimeStatusView{}, fmt.Errorf("runtime install returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var status RuntimeStatusView
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return RuntimeStatusView{}, err
	}
	return status, nil
}

// CleanupRuntimes clears the sandbox runtime/venv cache.
func (s *Shell) CleanupRuntimes(ctx context.Context) (RuntimeStatusView, error) {
	s.mu.RLock()
	sandboxURL := s.status.SandboxURL
	token := s.sandboxTok
	s.mu.RUnlock()
	if strings.TrimSpace(sandboxURL) == "" {
		return RuntimeStatusView{}, fmt.Errorf("sandbox is not running")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(sandboxURL, "/")+"/api/v1/runtime/cleanup", nil)
	if err != nil {
		return RuntimeStatusView{}, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 2 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return RuntimeStatusView{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return RuntimeStatusView{}, fmt.Errorf("runtime cleanup returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var status RuntimeStatusView
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		return RuntimeStatusView{}, err
	}
	return status, nil
}
