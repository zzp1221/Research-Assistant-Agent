package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var containerIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

const (
	sandboxLabelKey   = "research-assistant.sandbox"
	sandboxLabelValue = "true"
	tenantLabelKey    = "research-assistant.tenant-id"
	taskLabelKey      = "research-assistant.task-id"
	planLabelKey      = "research-assistant.plan-id"
)

type NativeDockerEngine struct {
	mu         sync.RWMutex
	mountPaths map[string]string // 记录容器 ID 到宿主机挂载路径的映射
}

type streamChunk struct {
	stream string
	line   string
}

type DockerHealth struct {
	Available           bool   `json:"available"`
	Command             string `json:"command"`
	ServerVersion       string `json:"server_version,omitempty"`
	GPURequest          string `json:"gpu_request,omitempty"`
	GPURuntimeAvailable bool   `json:"gpu_runtime_available"`
	Error               string `json:"error,omitempty"`
}

func NewNativeDockerEngine() *NativeDockerEngine {
	return &NativeDockerEngine{
		mountPaths: make(map[string]string),
	}
}

func (e *NativeDockerEngine) Health(ctx context.Context) DockerHealth {
	command := dockerCommand()
	gpuRequest := dockerGPURequest()
	checkCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	output, err := exec.CommandContext(checkCtx, command, "version", "--format", "{{.Server.Version}}").CombinedOutput()
	if err != nil {
		return DockerHealth{
			Available:  false,
			Command:    command,
			GPURequest: gpuRequest,
			Error:      fmt.Sprintf("Docker CLI or daemon is unavailable: %v, output: %s", err, strings.TrimSpace(string(output))),
		}
	}

	health := DockerHealth{
		Available:     true,
		Command:       command,
		ServerVersion: strings.TrimSpace(string(output)),
		GPURequest:    gpuRequest,
	}
	if gpuRequest == "" {
		return health
	}

	runtimes, runtimeErr := exec.CommandContext(checkCtx, command, "info", "--format", "{{json .Runtimes}}").CombinedOutput()
	health.GPURuntimeAvailable = runtimeErr == nil && strings.Contains(string(runtimes), `"nvidia"`)
	if !health.GPURuntimeAvailable {
		health.Available = false
		health.Error = fmt.Sprintf("GPU sandbox requested (%s), but the NVIDIA Docker runtime is unavailable", gpuRequest)
	}
	return health
}

func dockerGPURequest() string {
	request := strings.TrimSpace(os.Getenv("SANDBOX_DOCKER_GPUS"))
	if strings.EqualFold(request, "none") {
		return ""
	}
	return request
}

func dockerCommand() string {
	if path, err := exec.LookPath("docker"); err == nil {
		return path
	}
	for _, path := range []string{
		"/usr/local/bin/docker",
		"/opt/homebrew/bin/docker",
		"/Applications/Docker.app/Contents/Resources/bin/docker",
		"/Volumes/Docker/Docker.app/Contents/Resources/bin/docker",
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return "docker"
}

func (e *NativeDockerEngine) GetType() string {
	return "Docker"
}

func (e *NativeDockerEngine) Create(ctx context.Context, image string, mountPath string, labels map[string]string) (string, error) {
	image = rewriteImageWithRegistryMirror(image)
	args, err := secureDockerRunArgs(image, labels)
	if err != nil {
		return "", err
	}
	if gpuRequest := dockerGPURequest(); gpuRequest != "" {
		args = append(args, "--gpus", gpuRequest)
	}
	if mountPath != "" {
		normalizedMountPath, err := normalizeDockerMountPath(mountPath)
		if err != nil {
			return "", err
		}
		args = append(args, "-v", fmt.Sprintf("%s:/workspace", normalizedMountPath))
	}
	// Share one pip cache across sandbox containers so repeated dependency
	// installs hit the local cache instead of re-downloading wheels.
	if cacheDir := strings.TrimSpace(os.Getenv("SANDBOX_PIP_CACHE_DIR")); cacheDir != "" {
		if err := os.MkdirAll(cacheDir, 0o777); err == nil {
			args = append(args, "-v", fmt.Sprintf("%s:/root/.cache/pip", cacheDir))
		}
	}
	args = append(args, "--", image, "sleep", "infinity")
	fmt.Printf("[NativeDocker] Executing: docker %s\n", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, dockerCommand(), args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Docker run failed: %v, output: %s", err, string(output))
	}
	containerID := extractContainerIDFromDockerRunOutput(string(output))
	if containerID == "" {
		return "", fmt.Errorf("Docker run returned empty container id, output: %s", string(output))
	}
	if mountPath != "" {
		e.mu.Lock()
		e.mountPaths[containerID] = mountPath
		e.mu.Unlock()
	}
	return containerID, nil
}

func extractContainerIDFromDockerRunOutput(raw string) string {
	// `docker run -d` may print image pull progress before the final container id
	// when the image is not present locally. We scan from the last line upward and
	// validate the candidate against the expected hex container-id pattern.
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if containerIDPattern.MatchString(line) {
			return line
		}
	}
	return ""
}

func normalizeDockerMountPath(mountPath string) (string, error) {
	if strings.TrimSpace(mountPath) == "" {
		return "", nil
	}
	return normalizeAndAuthorizeMountPath(mountPath)
}

// rewriteImageWithRegistryMirror prefixes docker.io images with the configured
// mirror (for example docker.m.daocloud.io) so a deployment without direct
// Docker Hub access can still pull runtime images. Images that already carry an
// explicit registry host are left untouched.
func rewriteImageWithRegistryMirror(image string) string {
	image = strings.TrimSpace(image)
	mirror := strings.TrimSpace(os.Getenv("SANDBOX_REGISTRY_MIRROR"))
	if image == "" || mirror == "" {
		return image
	}
	mirror = strings.TrimPrefix(mirror, "https://")
	mirror = strings.TrimPrefix(mirror, "http://")
	mirror = strings.TrimSuffix(mirror, "/")
	if mirror == "" || strings.HasPrefix(image, mirror+"/") {
		return image
	}
	if strings.Contains(image, "/") {
		first := strings.Split(image, "/")[0]
		if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
			// Already an explicit registry host; do not rewrite.
			return image
		}
		return mirror + "/" + image
	}
	// Bare docker.io image such as "python:3.11-bullseye".
	return mirror + "/library/" + image
}

func (e *NativeDockerEngine) validateSandboxContainer(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	if !containerIDPattern.MatchString(id) {
		return fmt.Errorf("invalid sandbox container id")
	}
	output, err := exec.CommandContext(ctx, dockerCommand(),
		"inspect", "--format", `{{json .Config.Labels}}`, id).Output()
	if err != nil {
		return fmt.Errorf("inspect sandbox container: %w", err)
	}
	labels := map[string]string{}
	if err := json.Unmarshal(bytes.TrimSpace(output), &labels); err != nil {
		return fmt.Errorf("decode sandbox labels: %w", err)
	}
	if strings.TrimSpace(labels[sandboxLabelKey]) != sandboxLabelValue {
		return fmt.Errorf("container %s is not managed by this sandbox service", id)
	}
	if strings.TrimSpace(labels[tenantLabelKey]) == "" || strings.TrimSpace(labels[taskLabelKey]) == "" {
		return fmt.Errorf("container %s is not bound to a tenant/task", id)
	}
	return nil
}

func (e *NativeDockerEngine) Delete(ctx context.Context, id string) error {
	if err := e.validateSandboxContainer(ctx, id); err != nil {
		return err
	}
	if err := exec.CommandContext(ctx, dockerCommand(), "rm", "-f", id).Run(); err != nil {
		return err
	}
	e.mu.Lock()
	delete(e.mountPaths, id)
	e.mu.Unlock()
	return nil
}

// executionMarkerPath marks the start of a sandbox execution so image capture
// only returns files created by the current run instead of stale outputs from
// earlier tasks sharing the same persistent runtime.
const executionMarkerPath = "/tmp/.ra_exec_start"

// executionOutputDir is the standard directory the runtime tells generated code
// to save figures into (mirrored to the backend as RA_OUTPUT_DIR).
const executionOutputDir = "/workspace/output"

// maxCapturedImages bounds how many images one execution can return.
const maxCapturedImages = 8

// maxCapturedImageBytes bounds the size of a single captured image.
const maxCapturedImageBytes = 32 << 20

// prepareExecution creates the standard output directory and stamps a marker
// file slightly in the past so a scan for "newer than marker" reliably includes
// files written during this execution even on filesystems with coarse mtimes.
func (e *NativeDockerEngine) prepareExecution(ctx context.Context, id string) {
	script := "mkdir -p " + executionOutputDir + " /workspace/outputs 2>/dev/null; " +
		"touch -d '2 seconds ago' " + executionMarkerPath + " 2>/dev/null || touch " + executionMarkerPath
	_ = exec.CommandContext(ctx, dockerCommand(), "exec", id, "sh", "-c", script).Run()
}

// collectGeneratedImages returns base64 encoded images created under /workspace
// since the execution marker was written. It supports multiple images and
// arbitrary file names, which is the core of the unified artifact contract.
func (e *NativeDockerEngine) collectGeneratedImages(ctx context.Context, id string) []string {
	findScript := "find /workspace -type f -name '*.png' -newer " + executionMarkerPath + " -print 2>/dev/null | head -n " + strconv.Itoa(maxCapturedImages)
	output, err := exec.CommandContext(ctx, dockerCommand(), "exec", id, "sh", "-c", findScript).Output()
	if err != nil {
		return nil
	}
	images := make([]string, 0, maxCapturedImages)
	seen := map[string]struct{}{}
	for _, rawPath := range strings.Split(string(output), "\n") {
		path := strings.TrimSpace(rawPath)
		if path == "" || strings.Contains(path, "..") {
			continue
		}
		if _, duplicate := seen[path]; duplicate {
			continue
		}
		seen[path] = struct{}{}
		data, readErr := exec.CommandContext(ctx, dockerCommand(), "exec", id, "cat", path).Output()
		if readErr != nil || len(data) == 0 || len(data) > maxCapturedImageBytes {
			continue
		}
		images = append(images, base64.StdEncoding.EncodeToString(data))
		_ = exec.CommandContext(ctx, dockerCommand(), "exec", id, "rm", "-f", path).Run()
	}
	return images
}

// readPlotImageFromContainer is the legacy single-file capture kept as a
// fallback for runtimes that still write the historical output_plot.png name
// without a newer mtime (for example when a previous step produced it).
// SandboxInfo describes one running sandbox container for the plan registry.
type SandboxInfo struct {
	ID        string `json:"id"`
	PlanID    string `json:"plan_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	TenantID  string `json:"tenant_id,omitempty"`
	Image     string `json:"image,omitempty"`
	Status    string `json:"status,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

// ListByPlan returns the sandbox containers labelled with the given plan id.
func (e *NativeDockerEngine) ListByPlan(ctx context.Context, planID string) ([]SandboxInfo, error) {
	planID = strings.TrimSpace(planID)
	if !sandboxIdentityValid(planID) {
		return nil, fmt.Errorf("invalid plan id")
	}
	output, err := exec.CommandContext(ctx, dockerCommand(), "ps", "-a",
		"--filter", "label="+sandboxLabelKey+"="+sandboxLabelValue,
		"--filter", "label="+planLabelKey+"="+planID,
		"--format", "{{.ID}}|{{.Image}}|{{.Status}}").Output()
	if err != nil {
		return nil, fmt.Errorf("list sandboxes for plan: %w", err)
	}
	items := make([]SandboxInfo, 0)
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 || !containerIDPattern.MatchString(parts[0]) {
			continue
		}
		items = append(items, SandboxInfo{ID: parts[0], PlanID: planID, Image: parts[1], Status: parts[2]})
	}
	return items, nil
}

// CleanupByPlan force-removes every sandbox container of a plan. Removing the
// container also terminates pip/python child processes inside it.
func (e *NativeDockerEngine) CleanupByPlan(ctx context.Context, planID string) (int, error) {
	items, err := e.ListByPlan(ctx, planID)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, item := range items {
		if err := exec.CommandContext(ctx, dockerCommand(), "rm", "-f", item.ID).Run(); err == nil {
			removed++
			e.mu.Lock()
			delete(e.mountPaths, item.ID)
			e.mu.Unlock()
		}
	}
	return removed, nil
}

// ReapStale removes sandbox containers that have been running longer than the
// configured TTL. It returns the number of removed containers.
func (e *NativeDockerEngine) ReapStale(ctx context.Context, ttl time.Duration) (int, error) {
	if ttl <= 0 {
		return 0, nil
	}
	output, err := exec.CommandContext(ctx, dockerCommand(), "ps", "-a",
		"--filter", "label="+sandboxLabelKey+"="+sandboxLabelValue,
		"--format", "{{.ID}}").Output()
	if err != nil {
		return 0, fmt.Errorf("list sandbox containers: %w", err)
	}
	removed := 0
	now := time.Now()
	for _, raw := range strings.Split(string(output), "\n") {
		id := strings.TrimSpace(raw)
		if !containerIDPattern.MatchString(id) {
			continue
		}
		startedRaw, inspectErr := exec.CommandContext(ctx, dockerCommand(), "inspect",
			"--format", "{{.State.StartedAt}}", id).Output()
		if inspectErr != nil {
			continue
		}
		started, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(startedRaw)))
		if parseErr != nil || now.Sub(started) < ttl {
			continue
		}
		if rmErr := exec.CommandContext(ctx, dockerCommand(), "rm", "-f", id).Run(); rmErr == nil {
			removed++
			e.mu.Lock()
			delete(e.mountPaths, id)
			e.mu.Unlock()
		}
	}
	return removed, nil
}

func sandboxIdentityValid(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.' || r == ':':
		default:
			return false
		}
	}
	return true
}

func (e *NativeDockerEngine) readPlotImageFromContainer(ctx context.Context, id string) []string {
	cmd := exec.CommandContext(ctx, dockerCommand(), "exec", id, "cat", "/workspace/output_plot.png")
	data, err := cmd.Output()
	if err != nil || len(data) == 0 {
		return nil
	}
	_ = exec.CommandContext(ctx, dockerCommand(), "exec", id, "rm", "-f", "/workspace/output_plot.png").Run()
	return []string{base64.StdEncoding.EncodeToString(data)}
}

func (e *NativeDockerEngine) ExecutePython(ctx context.Context, id string, code string) (*ExecutionResponse, error) {
	if err := e.validateSandboxContainer(ctx, id); err != nil {
		return nil, err
	}
	e.prepareExecution(ctx, id)
	cmd := exec.CommandContext(ctx, dockerCommand(), "exec",
		"-e", "RA_OUTPUT_DIR="+executionOutputDir,
		"-e", "MPLBACKEND=Agg",
		id, "python3", "-c", code)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	response := &ExecutionResponse{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}

	// 统一产物契约：扫描本次执行在 /workspace 下新增的 *.png（支持多图与任意文件名）。
	response.Images = e.collectGeneratedImages(ctx, id)
	if len(response.Images) == 0 {
		// 兼容旧契约：仍从挂载目录读取 output_plot.png。
		e.mu.RLock()
		mountPath, ok := e.mountPaths[id]
		e.mu.RUnlock()
		if ok {
			plotPath := filepath.Join(mountPath, "output_plot.png")
			if _, err := os.Stat(plotPath); err == nil {
				imgData, readErr := os.ReadFile(plotPath)
				if readErr == nil {
					response.Images = []string{base64.StdEncoding.EncodeToString(imgData)}
					// 读取后删除，避免下一次执行干扰
					os.Remove(plotPath)
				}
			}
		}
	}
	if len(response.Images) == 0 {
		response.Images = e.readPlotImageFromContainer(ctx, id)
	}

	return response, nil
}

func (e *NativeDockerEngine) ExecutePythonStream(ctx context.Context, id string, code string, emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
	if err := e.validateSandboxContainer(ctx, id); err != nil {
		return nil, err
	}
	e.prepareExecution(ctx, id)
	cmd := exec.CommandContext(ctx, dockerCommand(), "exec",
		"-e", "RA_OUTPUT_DIR="+executionOutputDir,
		"-e", "MPLBACKEND=Agg",
		id, "python3", "-c", code)
	response, err := e.runStreamingCommand(ctx, cmd, emit)
	if err != nil {
		return nil, err
	}

	response.Images = e.collectGeneratedImages(ctx, id)
	if len(response.Images) == 0 {
		e.mu.RLock()
		mountPath, ok := e.mountPaths[id]
		e.mu.RUnlock()
		if ok {
			plotPath := filepath.Join(mountPath, "output_plot.png")
			if _, statErr := os.Stat(plotPath); statErr == nil {
				imgData, readErr := os.ReadFile(plotPath)
				if readErr == nil {
					response.Images = []string{base64.StdEncoding.EncodeToString(imgData)}
					os.Remove(plotPath)
				}
			}
		}
	}
	if len(response.Images) == 0 {
		response.Images = e.readPlotImageFromContainer(ctx, id)
	}

	return response, emit(ExecutionStreamEvent{
		Type:     "final",
		Response: response,
	})
}

func (e *NativeDockerEngine) ExecuteCommand(ctx context.Context, id string, cmdArr []string) (*ExecutionResponse, error) {
	if err := e.validateSandboxContainer(ctx, id); err != nil {
		return nil, err
	}
	e.prepareExecution(ctx, id)
	args := append([]string{"exec", "-e", "RA_OUTPUT_DIR=" + executionOutputDir, id}, cmdArr...)
	fmt.Printf("[NativeDocker] Executing: docker %s\n", strings.Join(args, " "))
	dockerCmd := exec.CommandContext(ctx, dockerCommand(), args...)
	var stdout, stderr bytes.Buffer
	dockerCmd.Stdout = &stdout
	dockerCmd.Stderr = &stderr
	err := dockerCmd.Run()

	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	response := &ExecutionResponse{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
	response.Images = e.collectGeneratedImages(ctx, id)
	return response, nil
}

func (e *NativeDockerEngine) ExecuteCommandStream(ctx context.Context, id string, cmdArr []string, emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
	if err := e.validateSandboxContainer(ctx, id); err != nil {
		return nil, err
	}
	e.prepareExecution(ctx, id)
	args := append([]string{"exec", "-e", "RA_OUTPUT_DIR=" + executionOutputDir, id}, cmdArr...)
	fmt.Printf("[NativeDocker] Executing: docker %s\n", strings.Join(args, " "))
	dockerCmd := exec.CommandContext(ctx, dockerCommand(), args...)
	response, err := e.runStreamingCommand(ctx, dockerCmd, emit)
	if err != nil {
		return nil, err
	}
	response.Images = e.collectGeneratedImages(ctx, id)
	return response, emit(ExecutionStreamEvent{
		Type:     "final",
		Response: response,
	})
}

func (e *NativeDockerEngine) runStreamingCommand(ctx context.Context, cmd *exec.Cmd, emit func(ExecutionStreamEvent) error) (*ExecutionResponse, error) {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	chunks := make(chan streamChunk, 64)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		readPipeLines(stdoutPipe, "stdout", &stdoutBuf, chunks)
	}()
	go func() {
		defer wg.Done()
		readPipeLines(stderrPipe, "stderr", &stderrBuf, chunks)
	}()
	go func() {
		wg.Wait()
		close(chunks)
	}()

	for chunk := range chunks {
		if emitErr := emit(ExecutionStreamEvent{
			Type:    "chunk",
			Stream:  chunk.stream,
			Message: chunk.line,
		}); emitErr != nil {
			return nil, emitErr
		}
	}

	waitErr := cmd.Wait()
	exitCode := 0
	if waitErr != nil {
		if exitError, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	return &ExecutionResponse{
		Stdout:   stdoutBuf.String(),
		Stderr:   stderrBuf.String(),
		ExitCode: exitCode,
	}, nil
}

func readPipeLines(reader io.Reader, stream string, sink *bytes.Buffer, out chan<- streamChunk) {
	scanner := bufio.NewScanner(reader)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		sink.WriteString(line)
		sink.WriteByte('\n')
		out <- streamChunk{stream: stream, line: line}
	}
}
