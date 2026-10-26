package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// SandboxClient 是后端与外部 docker-sandbox 服务通信的客户端
type SandboxClient struct {
	BaseURL string
	Client  *http.Client
	Token   string
}

// SandboxIdentity binds a sandbox to its tenant, plan and task. The sandbox
// service turns these values into Docker labels and refuses unlabeled
// containers.
type SandboxIdentity struct {
	TenantID string
	TaskID   string
	PlanID   string
}

// SandboxCreateRequest 创建沙箱请求 (简化版)
type SandboxCreateRequest struct {
	Image     string `json:"image"`
	MountPath string `json:"mount_path"`
	TenantID  string `json:"tenant_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	PlanID    string `json:"plan_id,omitempty"`
}

// SandboxCreateResponse 创建沙箱响应
type SandboxCreateResponse struct {
	SandboxID string `json:"sandbox_id"`
}

// PythonRunRequest Python 代码执行请求
type PythonRunRequest struct {
	Code string `json:"code"`
}

// PythonRunResponse Python 代码执行响应 (包含 stdout, stderr, exit_code 和 images)
type PythonRunResponse struct {
	Stdout   string   `json:"stdout"`
	Stderr   string   `json:"stderr"`
	ExitCode int      `json:"exit_code"`
	Images   []string `json:"images,omitempty"`
}

type executionStreamEvent struct {
	Type     string             `json:"type"`
	Stream   string             `json:"stream,omitempty"`
	Message  string             `json:"message,omitempty"`
	Response *PythonRunResponse `json:"response,omitempty"`
}

func sandboxClientTimeout() time.Duration {
	const fallback = 45 * time.Minute
	raw := strings.TrimSpace(os.Getenv("SANDBOX_CLIENT_TIMEOUT_SECONDS"))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return time.Duration(value) * time.Second
}

func NewSandboxClient(baseURL string) *SandboxClient {
	if baseURL == "" {
		baseURL = "http://localhost:8082" // 默认指向我们的新 docker-sandbox 服务
	}
	return &SandboxClient{
		BaseURL: baseURL,
		Token:   strings.TrimSpace(os.Getenv("SANDBOX_API_TOKEN")),
		Client: &http.Client{
			// Dependency installs (torch, CUDA wheels) can legitimately exceed
			// ten minutes on slow mirrors; the per-task timeout is enforced by
			// the scheduler, so the transport timeout only needs to be a safety
			// net above it.
			Timeout: sandboxClientTimeout(),
		},
	}
}

// CreatePersistentSandbox 创建持久化沙箱
func (s *SandboxClient) CreatePersistentSandbox(ctx context.Context, identity SandboxIdentity, image string, mountPath string) (string, error) {
	reqBody, _ := json.Marshal(SandboxCreateRequest{
		Image:     image,
		MountPath: mountPath,
		TenantID:  strings.TrimSpace(identity.TenantID),
		TaskID:    strings.TrimSpace(identity.TaskID),
		PlanID:    strings.TrimSpace(identity.PlanID),
	})
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/v1/sandboxes", bytes.NewBuffer(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	s.authorize(req)

	resp, err := s.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("沙箱服务返回错误 (Status %d): %s", resp.StatusCode, string(body))
	}

	var res SandboxCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	return res.SandboxID, nil
}

// CleanupSandbox 清理沙箱
func (s *SandboxClient) CleanupSandbox(ctx context.Context, sandboxID string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", s.BaseURL+"/api/v1/sandboxes/"+sandboxID, nil)
	if err != nil {
		return err
	}
	s.authorize(req)

	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	return nil
}

// RunPythonCode 运行 Python 代码
func (s *SandboxClient) RunPythonCode(ctx context.Context, sandboxID string, code string) (*PythonRunResponse, error) {
	reqBody, _ := json.Marshal(PythonRunRequest{Code: code})
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/v1/sandboxes/"+sandboxID+"/python", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	s.authorize(req)

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("沙箱执行失败 (Status %d): %s", resp.StatusCode, string(body))
	}

	var res PythonRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return &res, nil
}

func (s *SandboxClient) RunPythonCodeStream(ctx context.Context, sandboxID string, code string, onChunk func(stream string, line string)) (*PythonRunResponse, error) {
	return s.streamExecution(ctx, sandboxID, "/python/stream", PythonRunRequest{Code: code}, onChunk)
}

// ExecCommand 在沙箱中执行命令
func (s *SandboxClient) ExecCommand(ctx context.Context, sandboxID string, cmd []string) (*PythonRunResponse, error) {
	reqBody, _ := json.Marshal(map[string][]string{"cmd": cmd})
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/v1/sandboxes/"+sandboxID+"/commands", bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	s.authorize(req)

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("执行命令失败 (Status %d): %s", resp.StatusCode, string(body))
	}

	var res PythonRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, err
	}

	return &res, nil
}

func (s *SandboxClient) ExecCommandStream(ctx context.Context, sandboxID string, cmd []string, onChunk func(stream string, line string)) (*PythonRunResponse, error) {
	return s.streamExecution(ctx, sandboxID, "/commands/stream", map[string][]string{"cmd": cmd}, onChunk)
}

func (s *SandboxClient) streamExecution(ctx context.Context, sandboxID string, suffix string, payload any, onChunk func(stream string, line string)) (*PythonRunResponse, error) {
	reqBody, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, "POST", s.BaseURL+"/api/v1/sandboxes/"+sandboxID+suffix, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	s.authorize(req)

	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("stream execution failed (Status %d): %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	buffer := make([]byte, 0, 64*1024)
	scanner.Buffer(buffer, 1024*1024)

	var finalResponse *PythonRunResponse
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var event executionStreamEvent
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("decode execution stream failed: %w", err)
		}

		switch event.Type {
		case "chunk":
			if onChunk != nil {
				onChunk(event.Stream, event.Message)
			}
		case "final":
			finalResponse = event.Response
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if finalResponse == nil {
		return nil, fmt.Errorf("stream execution ended without final response")
	}
	return finalResponse, nil
}

// SandboxCapabilities mirrors the docker-sandbox capabilities endpoint.
type SandboxCapabilities struct {
	DockerAvailable     bool     `json:"docker_available"`
	ServerVersion       string   `json:"server_version,omitempty"`
	GPURequest          string   `json:"gpu_request,omitempty"`
	GPURuntimeAvailable bool     `json:"gpu_runtime_available"`
	CPUCount            int      `json:"cpu_count"`
	MemoryTotalBytes    int64    `json:"memory_total_bytes"`
	DiskFreeBytes       int64    `json:"disk_free_bytes"`
	ImageAllowlist      []string `json:"image_allowlist"`
	NetworkMode         string   `json:"network_mode"`
}

type SandboxImageInfo struct {
	Image     string `json:"image"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
	Pulled    bool   `json:"pulled"`
}

func (s *SandboxClient) Capabilities(ctx context.Context) (*SandboxCapabilities, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/api/v1/capabilities", nil)
	if err != nil {
		return nil, err
	}
	s.authorize(req)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sandbox capabilities failed (Status %d): %s", resp.StatusCode, string(body))
	}
	var caps SandboxCapabilities
	if err := json.NewDecoder(resp.Body).Decode(&caps); err != nil {
		return nil, err
	}
	return &caps, nil
}

func (s *SandboxClient) EnsureImage(ctx context.Context, image string) (*SandboxImageInfo, error) {
	payload, _ := json.Marshal(map[string]string{"image": image})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/api/v1/images/ensure", bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	s.authorize(req)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("sandbox image ensure failed (Status %d): %s", resp.StatusCode, string(body))
	}
	var info SandboxImageInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// SandboxInfo mirrors one entry of the sandbox plan registry.
type SandboxInfo struct {
	ID        string `json:"id"`
	PlanID    string `json:"plan_id,omitempty"`
	Image     string `json:"image,omitempty"`
	Status    string `json:"status,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

// ListPlanSandboxes lists the running sandbox containers of one plan.
func (s *SandboxClient) ListPlanSandboxes(ctx context.Context, planID string) ([]SandboxInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.BaseURL+"/api/v1/sandboxes?plan_id="+url.QueryEscape(planID), nil)
	if err != nil {
		return nil, err
	}
	s.authorize(req)
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list plan sandboxes failed (Status %d): %s", resp.StatusCode, string(body))
	}
	var payload struct {
		Sandboxes []SandboxInfo `json:"sandboxes"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload.Sandboxes, nil
}

// CleanupPlanSandboxes force-removes every sandbox container of a plan. This
// terminates pip/python child processes as a side effect.
func (s *SandboxClient) CleanupPlanSandboxes(ctx context.Context, planID string) (int, error) {
	payload, _ := json.Marshal(map[string]string{"plan_id": planID})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL+"/api/v1/sandboxes/cleanup", bytes.NewBuffer(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	s.authorize(req)
	resp, err := s.Client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return 0, fmt.Errorf("cleanup plan sandboxes failed (Status %d): %s", resp.StatusCode, string(body))
	}
	var result struct {
		Removed int `json:"removed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.Removed, nil
}

func (s *SandboxClient) authorize(req *http.Request) {
	if strings.TrimSpace(s.Token) != "" {
		req.Header.Set("Authorization", "Bearer "+s.Token)
	}
}
