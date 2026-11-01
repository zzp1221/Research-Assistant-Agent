package main

import (
	"context"
	"crypto/subtle"
	"docker-sandbox/internal/engine"
	"docker-sandbox/internal/engine/native"
	"docker-sandbox/internal/engine/remote"
	"docker-sandbox/internal/engine/vm"
	"docker-sandbox/internal/logging"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type Config struct {
	OpenSandboxURL string
	EnableFallback bool
	ListenAddr     string
}

type SandboxService struct {
	osEngine       *engine.OpenSandboxEngine
	dkEngine       *engine.NativeDockerEngine
	nativeProvider engine.Provider
	vmProvider     engine.Provider
	remoteProvider engine.Provider
	provider       string
	providerError  string
	config         Config

	limitsMu sync.RWMutex
	limits   map[string]engine.Limits
	network  map[string]string
}

func sandboxAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.URL.Path == "/api/v1/health" {
			c.Next()
			return
		}
		expected := strings.TrimSpace(os.Getenv("SANDBOX_API_TOKEN"))
		if expected == "" {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "SANDBOX_API_TOKEN is required"})
			return
		}
		provided := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid sandbox bearer token"})
			return
		}
		c.Next()
	}
}

func NewSandboxService(cfg Config) *SandboxService {
	var osEngine *engine.OpenSandboxEngine
	if cfg.EnableFallback && cfg.OpenSandboxURL != "" {
		osEngine = engine.NewOpenSandboxEngine(cfg.OpenSandboxURL)
	}
	service := &SandboxService{
		osEngine: osEngine,
		dkEngine: engine.NewNativeDockerEngine(),
		provider: "docker",
		config:   cfg,
		limits:   map[string]engine.Limits{},
		network:  map[string]string{},
	}
	service.nativeProvider = newNativeProviderFromEnv()
	requested := strings.ToLower(strings.TrimSpace(os.Getenv("SANDBOX_PROVIDER")))
	if requested == "" {
		requested = "auto"
	}
	switch requested {
	case "native":
		if service.nativeProvider != nil {
			service.provider = "native"
		} else {
			service.providerError = "SANDBOX_PROVIDER=native requested but the native provider is unavailable"
			log.Printf("[Sandbox] %s", service.providerError)
		}
	case "vm":
		service.vmProvider = vm.NewProvider(context.Background())
		service.provider = "vm"
		if provider, ok := service.vmProvider.(*vm.Provider); ok && !provider.Available() {
			service.providerError = "vm provider unavailable: " + provider.DetectionReason()
			log.Printf("[Sandbox] %s", service.providerError)
		}
	case "remote":
		if strings.TrimSpace(cfg.OpenSandboxURL) == "" {
			service.provider = "remote"
			service.providerError = "SANDBOX_PROVIDER=remote requires OPEN_SANDBOX_URL"
			log.Printf("[Sandbox] %s", service.providerError)
			break
		}
		provider, err := remote.NewProvider(cfg.OpenSandboxURL)
		if err != nil {
			service.provider = "remote"
			service.providerError = err.Error()
			log.Printf("[Sandbox] remote provider unavailable: %v", err)
			break
		}
		service.remoteProvider = provider
		service.osEngine = engine.NewOpenSandboxEngine(cfg.OpenSandboxURL)
		service.provider = "remote"
	case "auto":
		if service.nativeProvider != nil && !service.dkEngine.Health(context.Background()).Available {
			service.provider = "native"
		}
	default:
		service.providerError = fmt.Sprintf("unknown SANDBOX_PROVIDER %q", requested)
		log.Printf("[Sandbox] %s", service.providerError)
	}
	return service
}

// activeProvider returns the non-docker provider selected for this process.
func (s *SandboxService) activeProvider() engine.Provider {
	switch s.provider {
	case "native":
		return s.nativeProvider
	case "vm":
		return s.vmProvider
	case "remote":
		return s.remoteProvider
	default:
		return nil
	}
}

func newNativeProviderFromEnv() engine.Provider {
	requested := strings.ToLower(strings.TrimSpace(os.Getenv("SANDBOX_PROVIDER")))
	if requested != "" && requested != "auto" && requested != "native" {
		return nil
	}
	dataDir := strings.TrimSpace(os.Getenv("APP_DATA_DIR"))
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "research-assistant-desktop")
	}
	workspaceRoot := ""
	if roots := filepath.SplitList(strings.TrimSpace(os.Getenv("SANDBOX_WORKSPACE_ROOTS"))); len(roots) > 0 {
		workspaceRoot = roots[0]
	}
	provider, err := native.NewProvider(native.Config{
		DataDir:       dataDir,
		WorkspaceRoot: workspaceRoot,
		UVPath:        strings.TrimSpace(os.Getenv("UV_PATH")),
		PythonVersion: strings.TrimSpace(os.Getenv("SANDBOX_DEFAULT_PYTHON")),
		Runner:        strings.TrimSpace(os.Getenv("SANDBOX_WINDOWS_RUNNER")),
		MemoryMB:      envInt("SANDBOX_MEMORY_MB"),
		CPUCores:      envInt("SANDBOX_CPU_CORES"),
		MaxProcesses:  envInt("SANDBOX_MAX_PROCESSES"),
	})
	if err != nil {
		if requested == "native" {
			log.Printf("[Sandbox] native provider unavailable: %v", err)
		}
		return nil
	}
	return provider
}

func envInt(name string) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func (s *SandboxService) setLimits(id string, limits engine.Limits, networkMode string) {
	s.limitsMu.Lock()
	defer s.limitsMu.Unlock()
	if s.limits == nil {
		s.limits = map[string]engine.Limits{}
	}
	if s.network == nil {
		s.network = map[string]string{}
	}
	s.limits[id] = limits
	s.network[id] = networkMode
}

func (s *SandboxService) getLimits(id string) (engine.Limits, string) {
	s.limitsMu.RLock()
	defer s.limitsMu.RUnlock()
	return s.limits[id], s.network[id]
}

func (s *SandboxService) forgetLimits(id string) {
	s.limitsMu.Lock()
	defer s.limitsMu.Unlock()
	delete(s.limits, id)
	delete(s.network, id)
}

// executionContext applies MaxRuntimeSec to a single command execution.
func (s *SandboxService) executionContext(parent context.Context, id string) (context.Context, context.CancelFunc) {
	limits, _ := s.getLimits(id)
	if limits.MaxRuntimeSec <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, time.Duration(limits.MaxRuntimeSec)*time.Second)
}

// enforceUsage checks disk and download budgets after a command and annotates
// the response with the measured usage. It returns an error when a hard limit
// is exceeded so the caller can surface a 507 with the reason and numbers.
func (s *SandboxService) enforceUsage(ctx context.Context, id string, res *engine.ExecutionResponse) error {
	limits, _ := s.getLimits(id)
	if limits == (engine.Limits{}) {
		return nil
	}
	if res == nil {
		return nil
	}
	if rx, tx, err := s.dkEngine.NetworkBytes(ctx, id); err == nil {
		res.NetworkRXBytes = rx
		res.NetworkTXBytes = tx
		if limits.MaxDownloadBytes > 0 && rx > limits.MaxDownloadBytes {
			return fmt.Errorf("sandbox download limit exceeded: received %d bytes, limit %d", rx, limits.MaxDownloadBytes)
		}
	}
	if limits.MaxDiskGB > 0 {
		if used, err := s.dkEngine.DiskUsageMB(ctx, id); err == nil {
			res.DiskUsedMB = used
			if used > limits.MaxDiskGB*1024 {
				return fmt.Errorf("sandbox disk limit exceeded: used %.0f MB, limit %.0f GB", used, limits.MaxDiskGB)
			}
		}
	}
	return nil
}

var sandboxLogWriter *logging.RotatingWriter

func configureFileLogging() {
	logDir := resolveLogDir()
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("failed to create log dir %s: %v", logDir, err)
		return
	}

	logFile := filepath.Join(logDir, "docker-sandbox.log")
	writer, err := logging.Open(logFile, 20<<20, 5)
	if err != nil {
		log.Printf("failed to open sandbox log file %s: %v", logFile, err)
		return
	}
	sandboxLogWriter = writer
	multi := io.MultiWriter(os.Stdout, writer)
	log.SetOutput(multi)
	gin.DefaultWriter = multi
	gin.DefaultErrorWriter = multi
	log.Printf("sandbox logs redirected to %s", logFile)
}

func resolveLogDir() string {
	if configured := strings.TrimSpace(os.Getenv("LOG_DIR")); configured != "" {
		return configured
	}
	if dataDir := strings.TrimSpace(os.Getenv("APP_DATA_DIR")); dataDir != "" {
		return filepath.Join(dataDir, "logs")
	}
	return filepath.Join(resolveProjectRoot(), "logs")
}

func resolveProjectRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}

	candidates := []string{wd, filepath.Dir(wd), filepath.Dir(filepath.Dir(wd))}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if isProjectRoot(candidate) {
			return candidate
		}
	}
	return wd
}

func isProjectRoot(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "backend"))
	if err != nil || !info.IsDir() {
		return false
	}
	info, err = os.Stat(filepath.Join(dir, "docker-sandbox"))
	if err != nil || !info.IsDir() {
		return false
	}
	return true
}

// --- API 定义 ---

const (
	sandboxLabelKey   = "research-assistant.sandbox"
	sandboxLabelValue = "true"
	tenantLabelKey    = "research-assistant.tenant-id"
	taskLabelKey      = "research-assistant.task-id"
	planLabelKey      = "research-assistant.plan-id"
)

type CreateRequest struct {
	Image       string         `json:"image"`
	MountPath   string         `json:"mount_path"`
	TenantID    string         `json:"tenant_id,omitempty"`
	TaskID      string         `json:"task_id,omitempty"`
	PlanID      string         `json:"plan_id,omitempty"`
	NetworkMode string         `json:"network_mode,omitempty"`
	Runtime     string         `json:"runtime,omitempty"`
	Limits      *engine.Limits `json:"limits,omitempty"`
}

type NetworkRequest struct {
	Mode string `json:"mode"`
}

type CreateResponse struct {
	SandboxID string `json:"sandbox_id"`
}

type ExecuteRequest struct {
	Code string `json:"code"`
}

type CommandRequest struct {
	Cmd         []string          `json:"cmd,omitempty"`
	Argv        []string          `json:"argv,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Shell       bool              `json:"shell,omitempty"`
	TimeoutSec  int               `json:"timeout_sec,omitempty"`
	Stdin       string            `json:"stdin,omitempty"`
	MaxOutputKB int               `json:"max_output_kb,omitempty"`
}

func (r CommandRequest) engineCommand() engine.Command {
	argv := r.Argv
	if len(argv) == 0 {
		argv = r.Cmd
	}
	return engine.Command{
		Argv:        argv,
		Cwd:         r.Cwd,
		Env:         r.Env,
		Shell:       r.Shell,
		Timeout:     time.Duration(r.TimeoutSec) * time.Second,
		Stdin:       r.Stdin,
		MaxOutputKB: r.MaxOutputKB,
	}
}

// --- 处理器 ---

func (s *SandboxService) Health(c *gin.Context) {
	dockerHealth := s.dkEngine.Health(c.Request.Context())
	ok := dockerHealth.Available
	switch s.provider {
	case "native":
		ok = s.nativeProvider != nil
	case "vm":
		ok = s.vmProvider != nil
	case "remote":
		ok = s.remoteProvider != nil
	}
	if s.providerError != "" {
		ok = false
	}
	payload := gin.H{
		"ok":                    ok,
		"provider":              s.provider,
		"provider_capabilities": s.providerCapabilities(c.Request.Context()),
		"native_docker":         dockerHealth,
		"fallback_enabled":      s.config.EnableFallback,
		"opensandbox_url":       s.config.OpenSandboxURL,
		"effective_strategy":    chooseHealthStrategy(dockerHealth.Available, s.config.EnableFallback),
	}
	if s.providerError != "" {
		payload["error"] = s.providerError
	}
	c.JSON(http.StatusOK, payload)
}

type providerCapabilitiesResponse struct {
	engine.Capabilities
	Provider        string              `json:"provider"`
	Isolation       string              `json:"isolation"`
	Images          bool                `json:"images"`
	NetworkControl  bool                `json:"network_control"`
	NetworkEnforced string              `json:"network_enforced"`
	GPU             bool                `json:"gpu"`
	GPUEnforced     bool                `json:"gpu_enforced"`
	PythonVersions  []string            `json:"python_versions,omitempty"`
	OS              string              `json:"os"`
	Notes           []string            `json:"notes,omitempty"`
	Probe           *engine.ProbeReport `json:"probe,omitempty"`
}

func (s *SandboxService) providerCapabilities(ctx context.Context) providerCapabilitiesResponse {
	if provider := s.activeProvider(); provider != nil {
		caps, err := provider.Capabilities(ctx)
		if err == nil {
			return providerCapabilitiesResponse{
				Provider: caps.Provider, Isolation: caps.Isolation, Images: caps.Images,
				NetworkControl: caps.NetworkControl, NetworkEnforced: caps.NetworkEnforced,
				GPU: caps.GPU, GPUEnforced: caps.GPUEnforced, PythonVersions: caps.PythonVersions,
				OS: caps.OS, Notes: caps.Notes, Probe: caps.Probe,
			}
		}
	}
	caps := s.dkEngine.Capabilities(ctx)
	return providerCapabilitiesResponse{
		Capabilities:    caps,
		Provider:        "docker",
		Isolation:       "container",
		Images:          true,
		NetworkControl:  true,
		NetworkEnforced: "os",
		GPU:             strings.TrimSpace(caps.GPURequest) != "",
		GPUEnforced:     caps.GPURuntimeAvailable,
		OS:              runtime.GOOS,
	}
}

func (s *SandboxService) CreateSandbox(c *gin.Context) {
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Image == "" {
		req.Image = "python:3.9-bullseye"
	}
	labels, err := sandboxLabels(req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	limits := engine.Limits{}
	if req.Limits != nil {
		limits = *req.Limits
	}
	networkMode, networkErr := engine.EffectiveNetworkMode(req.NetworkMode)
	if networkErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": networkErr.Error()})
		return
	}

	if provider := s.activeProvider(); provider != nil {
		session, err := provider.Create(c.Request.Context(), engine.CreateSpec{
			Image: req.Image, MountPath: req.MountPath, Labels: labels, Limits: limits,
			NetworkMode: networkMode, PythonVer: req.Runtime,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, CreateResponse{SandboxID: session.ID})
		return
	}

	// 1. 优先尝试 Native Docker (本地开发最可靠，支持挂载)
	id, err := s.dkEngine.Create(c.Request.Context(), engine.CreateOptions{
		Image:       req.Image,
		MountPath:   req.MountPath,
		Labels:      labels,
		NetworkMode: networkMode,
		Runtime:     req.Runtime,
		Limits:      limits,
	})
	if err == nil {
		s.setLimits(id, limits, networkMode)
		log.Printf("[Success] Native Docker sandbox created: %s network=%s", id, networkMode)
		c.JSON(http.StatusOK, CreateResponse{SandboxID: "dk-" + id})
		return
	}
	dockerErr := err

	log.Printf("[Warning] Native Docker failed: %v", dockerErr)

	if !s.config.EnableFallback || s.osEngine == nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Native Docker failed: %v", dockerErr),
		})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("SANDBOX_ALLOW_UNLABELED_FALLBACK")), "true") {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Native Docker failed: %v; OpenSandbox fallback cannot enforce tenant/task labels (set SANDBOX_ALLOW_UNLABELED_FALLBACK=true only for trusted local use)", dockerErr),
		})
		return
	}

	log.Printf("[Warning] Falling back to OpenSandbox: %s", s.config.OpenSandboxURL)

	// 2. 显式开启时才兜底 OpenSandbox
	id, err = s.osEngine.Create(c.Request.Context(), req.Image, req.MountPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": fmt.Sprintf("Native Docker failed: %v; OpenSandbox failed: %v", dockerErr, err),
		})
		return
	}

	c.JSON(http.StatusOK, CreateResponse{SandboxID: "os-" + id})
}

var sandboxIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func sandboxLabels(req CreateRequest) (map[string]string, error) {
	taskID := strings.TrimSpace(req.TaskID)
	if !sandboxIdentityPattern.MatchString(taskID) {
		return nil, fmt.Errorf("task_id is required and must match %s", sandboxIdentityPattern)
	}
	tenantID := strings.TrimSpace(req.TenantID)
	if tenantID == "" {
		tenantID = "local"
	}
	if !sandboxIdentityPattern.MatchString(tenantID) {
		return nil, fmt.Errorf("tenant_id must match %s", sandboxIdentityPattern)
	}
	planID := strings.TrimSpace(req.PlanID)
	if planID != "" && !sandboxIdentityPattern.MatchString(planID) {
		return nil, fmt.Errorf("plan_id must match %s", sandboxIdentityPattern)
	}
	labels := map[string]string{
		sandboxLabelKey: sandboxLabelValue,
		tenantLabelKey:  tenantID,
		taskLabelKey:    taskID,
	}
	if planID != "" {
		labels[planLabelKey] = planID
	}
	return labels, nil
}

func chooseHealthStrategy(nativeDockerAvailable bool, fallbackEnabled bool) string {
	if nativeDockerAvailable {
		return "native_docker"
	}
	if fallbackEnabled {
		return "opensandbox_fallback"
	}
	return "unavailable"
}

func (s *SandboxService) DeleteSandbox(c *gin.Context) {
	id := c.Param("id")
	ctx := c.Request.Context()
	if strings.HasPrefix(id, "nt-") {
		if s.nativeProvider == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "native provider is unavailable"})
			return
		}
		if err := s.nativeProvider.Delete(ctx, id); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
		return
	}
	if strings.HasPrefix(id, "vm-") {
		if s.vmProvider == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "vm provider is unavailable"})
			return
		}
		if err := s.vmProvider.Delete(ctx, id); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Status(http.StatusOK)
		return
	}
	if strings.HasPrefix(id, "os-") {
		if s.osEngine == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "opensandbox fallback is disabled"})
			return
		}
		if err := s.osEngine.Delete(ctx, strings.TrimPrefix(id, "os-")); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
	} else if strings.HasPrefix(id, "dk-") {
		if err := s.dkEngine.Delete(ctx, strings.TrimPrefix(id, "dk-")); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s.forgetLimits(strings.TrimPrefix(id, "dk-"))
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown sandbox id prefix"})
		return
	}
	c.Status(http.StatusOK)
}

// SetSandboxNetwork opens or closes egress for one sandbox. The backend calls
// this around an approved dependency install and always closes it afterwards.
func (s *SandboxService) SetSandboxNetwork(c *gin.Context) {
	id := c.Param("id")
	if strings.HasPrefix(id, "nt-") {
		var req NetworkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if s.nativeProvider == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "native provider is unavailable"})
			return
		}
		if err := s.nativeProvider.SetNetwork(c.Request.Context(), id, strings.EqualFold(req.Mode, "bridge")); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sandbox_id": id, "network_mode": req.Mode})
		return
	}
	if strings.HasPrefix(id, "vm-") && s.vmProvider != nil {
		var req NetworkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := s.vmProvider.SetNetwork(c.Request.Context(), id, strings.EqualFold(req.Mode, "bridge")); err != nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sandbox_id": id, "network_mode": req.Mode})
		return
	}
	if strings.HasPrefix(id, "os-") && s.remoteProvider != nil {
		var req NetworkRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		if err := s.remoteProvider.SetNetwork(c.Request.Context(), id, strings.EqualFold(req.Mode, "bridge")); err != nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sandbox_id": id, "network_mode": req.Mode})
		return
	}
	if !strings.HasPrefix(id, "dk-") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "network control is only supported for native docker sandboxes"})
		return
	}
	var req NetworkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	containerID := strings.TrimPrefix(id, "dk-")
	if err := s.dkEngine.SetNetwork(c.Request.Context(), containerID, req.Mode); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	mode, _ := engine.EffectiveNetworkMode(req.Mode)
	s.limitsMu.Lock()
	if s.network != nil {
		s.network[containerID] = mode
	}
	s.limitsMu.Unlock()
	log.Printf("[Sandbox] network mode for %s set to %s", containerID, mode)
	c.JSON(http.StatusOK, gin.H{"sandbox_id": id, "network_mode": mode})
}

// SandboxUsage reports live network and disk usage for one sandbox.
func (s *SandboxService) SandboxUsage(c *gin.Context) {
	id := c.Param("id")
	if strings.HasPrefix(id, "nt-") {
		if s.nativeProvider == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "native provider is unavailable"})
			return
		}
		usage, err := s.nativeProvider.Usage(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sandbox_id": id, "disk_used_mb": usage.DiskUsedMB, "runtime_hash": usage.RuntimeHash})
		return
	}
	if strings.HasPrefix(id, "vm-") && s.vmProvider != nil {
		usage, err := s.vmProvider.Usage(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sandbox_id": id, "disk_used_mb": usage.DiskUsedMB})
		return
	}
	if strings.HasPrefix(id, "os-") && s.remoteProvider != nil {
		usage, err := s.remoteProvider.Usage(c.Request.Context(), id)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"sandbox_id": id, "disk_used_mb": usage.DiskUsedMB, "runtime_hash": usage.RuntimeHash})
		return
	}
	if !strings.HasPrefix(id, "dk-") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "usage is only supported for native docker sandboxes"})
		return
	}
	containerID := strings.TrimPrefix(id, "dk-")
	limits, networkMode := s.getLimits(containerID)
	rx, tx, netErr := s.dkEngine.NetworkBytes(c.Request.Context(), containerID)
	disk, diskErr := s.dkEngine.DiskUsageMB(c.Request.Context(), containerID)
	payload := gin.H{
		"sandbox_id":   id,
		"network_mode": networkMode,
		"limits":       limits,
	}
	if netErr == nil {
		payload["network_rx_bytes"] = rx
		payload["network_tx_bytes"] = tx
	}
	if diskErr == nil {
		payload["disk_used_mb"] = disk
	}
	c.JSON(http.StatusOK, payload)
}

// RuntimeStatus reports the native runtime cache state.
func (s *SandboxService) RuntimeStatus(c *gin.Context) {
	if s.provider == "native" {
		if provider, ok := s.nativeProvider.(*native.Provider); ok {
			status, err := provider.RuntimeStatus(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, status)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"supported": false, "provider": s.provider})
}

type runtimeInstallRequest struct {
	PythonVersion string `json:"python_version"`
}

// InstallRuntime triggers the native uv runtime preparation.
func (s *SandboxService) InstallRuntime(c *gin.Context) {
	if s.provider == "native" {
		if provider, ok := s.nativeProvider.(*native.Provider); ok {
			var req runtimeInstallRequest
			_ = c.ShouldBindJSON(&req)
			status, err := provider.InstallRuntime(c.Request.Context(), req.PythonVersion)
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, status)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"supported": false, "provider": s.provider})
}

// CleanupRuntime clears the native runtime/venv cache.
func (s *SandboxService) CleanupRuntime(c *gin.Context) {
	if s.provider == "native" {
		if provider, ok := s.nativeProvider.(*native.Provider); ok {
			status, err := provider.CleanupRuntimeCache(c.Request.Context())
			if err != nil {
				c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, status)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"supported": false, "provider": s.provider})
}

func (s *SandboxService) ListSandboxes(c *gin.Context) {
	planID := strings.TrimSpace(c.Query("plan_id"))
	if planID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "plan_id is required"})
		return
	}
	items, err := s.dkEngine.ListByPlan(c.Request.Context(), planID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sandboxes": items, "count": len(items)})
}

type cleanupSandboxesRequest struct {
	PlanID string `json:"plan_id" binding:"required"`
}

func (s *SandboxService) CleanupSandboxes(c *gin.Context) {
	var req cleanupSandboxesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	removed, err := s.dkEngine.CleanupByPlan(c.Request.Context(), req.PlanID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.Printf("[Sandbox] cleanup plan=%s removed=%d", req.PlanID, removed)
	c.JSON(http.StatusOK, gin.H{"removed": removed, "plan_id": req.PlanID})
}

// startSandboxReaper periodically removes sandbox containers that outlived the
// configured TTL so leaked persistent runtimes cannot exhaust the host.
func startSandboxReaper(ctx context.Context, svc *SandboxService) {
	ttlMinutes := 120
	if raw := strings.TrimSpace(os.Getenv("SANDBOX_TTL_MINUTES")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			ttlMinutes = parsed
		}
	}
	interval := 5 * time.Minute
	if raw := strings.TrimSpace(os.Getenv("SANDBOX_REAPER_INTERVAL_SECONDS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 10 {
			interval = time.Duration(parsed) * time.Second
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed, err := svc.dkEngine.ReapStale(context.Background(), time.Duration(ttlMinutes)*time.Minute)
			if err != nil {
				log.Printf("[Sandbox] reaper failed: %v", err)
				continue
			}
			if svc.nativeProvider != nil {
				if nativeRemoved, nativeErr := svc.nativeProvider.Reap(context.Background(), time.Duration(ttlMinutes)*time.Minute); nativeErr == nil {
					removed += nativeRemoved
				}
			}
			if svc.remoteProvider != nil {
				if remoteRemoved, remoteErr := svc.remoteProvider.Reap(context.Background(), time.Duration(ttlMinutes)*time.Minute); remoteErr == nil {
					removed += remoteRemoved
				}
			}
			if svc.vmProvider != nil {
				if vmRemoved, vmErr := svc.vmProvider.Reap(context.Background(), time.Duration(ttlMinutes)*time.Minute); vmErr == nil {
					removed += vmRemoved
				}
			}
			if removed > 0 {
				log.Printf("[Sandbox] reaper removed %d stale sandbox container(s) (ttl=%dm)", removed, ttlMinutes)
			}
		}
	}
}

func (s *SandboxService) Capabilities(c *gin.Context) {
	c.JSON(http.StatusOK, s.providerCapabilities(c.Request.Context()))
}

type ensureImageRequest struct {
	Image string `json:"image" binding:"required"`
}

func (s *SandboxService) EnsureImage(c *gin.Context) {
	var req ensureImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	info, err := s.dkEngine.EnsureImage(c.Request.Context(), req.Image)
	if err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "image_not_allowed") {
			status = http.StatusBadRequest
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, info)
}

func (s *SandboxService) ExecutePython(c *gin.Context) {
	id := c.Param("id")
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var res *engine.ExecutionResponse
	var err error
	containerID := strings.TrimPrefix(id, "dk-")
	ctx, cancel := s.executionContext(c.Request.Context(), containerID)
	defer cancel()

	if strings.HasPrefix(id, "nt-") {
		if s.nativeProvider == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "native provider is unavailable"})
			return
		}
		result, execErr := s.nativeProvider.Exec(ctx, id, engine.Command{Argv: []string{"python", "-c", req.Code}, Cwd: "/workspace"}, nil)
		err = execErr
		if result != nil {
			res = resultToExecutionResponse(result)
		}
	} else if strings.HasPrefix(id, "vm-") {
		if s.vmProvider == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "vm provider is unavailable"})
			return
		}
		result, execErr := s.vmProvider.Exec(ctx, id, engine.Command{Argv: []string{"python3", "-c", req.Code}, Cwd: "/workspace"}, nil)
		err = execErr
		if result != nil {
			res = resultToExecutionResponse(result)
		}
	} else if strings.HasPrefix(id, "os-") {
		if s.osEngine == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "opensandbox fallback is disabled"})
			return
		}
		res, err = s.osEngine.ExecutePython(c.Request.Context(), strings.TrimPrefix(id, "os-"), req.Code)
	} else if strings.HasPrefix(id, "dk-") {
		res, err = s.dkEngine.ExecutePython(ctx, containerID, req.Code)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown sandbox id prefix"})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if guardErr := s.enforceUsage(c.Request.Context(), containerID, res); guardErr != nil {
		c.JSON(http.StatusInsufficientStorage, gin.H{"error": guardErr.Error(), "usage": res})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *SandboxService) ExecutePythonStream(c *gin.Context) {
	id := c.Param("id")
	var req ExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flushWriter, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming is not supported"})
		return
	}

	emit := func(event engine.ExecutionStreamEvent) error {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := c.Writer.Write(append(payload, '\n')); err != nil {
			return err
		}
		flushWriter.Flush()
		return nil
	}

	containerID := strings.TrimPrefix(id, "dk-")
	ctx, cancel := s.executionContext(c.Request.Context(), containerID)
	defer cancel()
	switch {
	case strings.HasPrefix(id, "dk-"):
		res, err := s.dkEngine.ExecutePythonStream(ctx, containerID, req.Code, emit)
		if err != nil {
			writeStreamError(c.Writer, flushWriter, err)
			return
		}
		if guardErr := s.enforceUsage(c.Request.Context(), containerID, res); guardErr != nil {
			writeStreamError(c.Writer, flushWriter, guardErr)
		}
	case strings.HasPrefix(id, "vm-"):
		if s.vmProvider == nil {
			writeStreamError(c.Writer, flushWriter, fmt.Errorf("vm provider is unavailable"))
			return
		}
		result, execErr := s.vmProvider.Exec(ctx, id, engine.Command{Argv: []string{"python3", "-c", req.Code}, Cwd: "/workspace"}, nil)
		if execErr != nil {
			writeStreamError(c.Writer, flushWriter, execErr)
			return
		}
		_ = emit(engine.ExecutionStreamEvent{Type: "final", Response: resultToExecutionResponse(result)})
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			writeStreamError(c.Writer, flushWriter, fmt.Errorf("opensandbox fallback is disabled"))
			return
		}
		res, err := s.osEngine.ExecutePython(ctx, strings.TrimPrefix(id, "os-"), req.Code)
		if err != nil {
			writeStreamError(c.Writer, flushWriter, err)
			return
		}
		_ = emit(engine.ExecutionStreamEvent{Type: "final", Response: res})
	default:
		writeStreamError(c.Writer, flushWriter, fmt.Errorf("unknown sandbox id prefix"))
	}
}

func (s *SandboxService) ExecuteCommand(c *gin.Context) {
	id := c.Param("id")
	var req CommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var res *engine.ExecutionResponse
	var err error
	containerID := strings.TrimPrefix(id, "dk-")
	ctx, cancel := s.executionContext(c.Request.Context(), containerID)
	defer cancel()

	cmd := req.engineCommand()
	if cmd.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, cmd.Timeout)
		defer timeoutCancel()
	}
	if strings.HasPrefix(id, "nt-") {
		if s.nativeProvider == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "native provider is unavailable"})
			return
		}
		result, execErr := s.nativeProvider.Exec(ctx, id, cmd, nil)
		err = execErr
		if result != nil {
			res = resultToExecutionResponse(result)
		}
	} else if strings.HasPrefix(id, "vm-") {
		if s.vmProvider == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "vm provider is unavailable"})
			return
		}
		result, execErr := s.vmProvider.Exec(ctx, id, cmd, nil)
		err = execErr
		if result != nil {
			res = resultToExecutionResponse(result)
		}
	} else if strings.HasPrefix(id, "os-") {
		if s.osEngine == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "opensandbox fallback is disabled"})
			return
		}
		res, err = s.osEngine.ExecuteCommand(c.Request.Context(), strings.TrimPrefix(id, "os-"), cmd.Argv)
	} else if strings.HasPrefix(id, "dk-") {
		res, err = s.dkEngine.ExecuteCommandSpec(ctx, containerID, cmd)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown sandbox id prefix"})
		return
	}

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if guardErr := s.enforceUsage(c.Request.Context(), containerID, res); guardErr != nil {
		c.JSON(http.StatusInsufficientStorage, gin.H{"error": guardErr.Error(), "usage": res})
		return
	}
	c.JSON(http.StatusOK, res)
}

func (s *SandboxService) ExecuteCommandStream(c *gin.Context) {
	id := c.Param("id")
	var req CommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "application/x-ndjson")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	flushWriter, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming is not supported"})
		return
	}

	emit := func(event engine.ExecutionStreamEvent) error {
		payload, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := c.Writer.Write(append(payload, '\n')); err != nil {
			return err
		}
		flushWriter.Flush()
		return nil
	}

	containerID := strings.TrimPrefix(id, "dk-")
	ctx, cancel := s.executionContext(c.Request.Context(), containerID)
	defer cancel()
	cmd := req.engineCommand()
	if cmd.Timeout > 0 {
		var timeoutCancel context.CancelFunc
		ctx, timeoutCancel = context.WithTimeout(ctx, cmd.Timeout)
		defer timeoutCancel()
	}
	switch {
	case strings.HasPrefix(id, "nt-"):
		if s.nativeProvider == nil {
			writeStreamError(c.Writer, flushWriter, fmt.Errorf("native provider is unavailable"))
			return
		}
		result, execErr := s.nativeProvider.Exec(ctx, id, cmd, func(event engine.Event) error {
			if event.Type == "chunk" {
				return emit(engine.ExecutionStreamEvent{Type: "chunk", Stream: event.Stream, Message: event.Message})
			}
			return nil
		})
		if execErr != nil {
			writeStreamError(c.Writer, flushWriter, execErr)
			return
		}
		res := resultToExecutionResponse(result)
		if guardErr := s.enforceUsage(c.Request.Context(), containerID, res); guardErr != nil {
			writeStreamError(c.Writer, flushWriter, guardErr)
		}
		_ = emit(engine.ExecutionStreamEvent{Type: "final", Response: res})
	case strings.HasPrefix(id, "dk-"):
		res, err := s.dkEngine.ExecuteCommandStreamSpec(ctx, containerID, cmd, emit)
		if err != nil {
			writeStreamError(c.Writer, flushWriter, err)
			return
		}
		if guardErr := s.enforceUsage(c.Request.Context(), containerID, res); guardErr != nil {
			writeStreamError(c.Writer, flushWriter, guardErr)
		}
	case strings.HasPrefix(id, "vm-"):
		if s.vmProvider == nil {
			writeStreamError(c.Writer, flushWriter, fmt.Errorf("vm provider is unavailable"))
			return
		}
		result, execErr := s.vmProvider.Exec(ctx, id, cmd, nil)
		if execErr != nil {
			writeStreamError(c.Writer, flushWriter, execErr)
			return
		}
		_ = emit(engine.ExecutionStreamEvent{Type: "final", Response: resultToExecutionResponse(result)})
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			writeStreamError(c.Writer, flushWriter, fmt.Errorf("opensandbox fallback is disabled"))
			return
		}
		res, err := s.osEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "os-"), cmd.Argv)
		if err != nil {
			writeStreamError(c.Writer, flushWriter, err)
			return
		}
		_ = emit(engine.ExecutionStreamEvent{Type: "final", Response: res})
	default:
		writeStreamError(c.Writer, flushWriter, fmt.Errorf("unknown sandbox id prefix"))
	}
}

func resultToExecutionResponse(result *engine.Result) *engine.ExecutionResponse {
	if result == nil {
		return nil
	}
	return &engine.ExecutionResponse{
		Stdout: result.Stdout, Stderr: result.Stderr, ExitCode: result.ExitCode, Truncated: result.Truncated,
	}
}

func writeStreamError(w io.Writer, flusher http.Flusher, err error) {
	payload, _ := json.Marshal(engine.ExecutionStreamEvent{
		Type:    "chunk",
		Stream:  "stderr",
		Message: err.Error(),
	})
	_, _ = w.Write(append(payload, '\n'))
	finalPayload, _ := json.Marshal(engine.ExecutionStreamEvent{
		Type: "final",
		Response: &engine.ExecutionResponse{
			Stderr:   err.Error(),
			ExitCode: -1,
		},
	})
	_, _ = w.Write(append(finalPayload, '\n'))
	flusher.Flush()
}

func main() {
	configureFileLogging()

	if strings.TrimSpace(os.Getenv("SANDBOX_API_TOKEN")) == "" {
		log.Fatal("SANDBOX_API_TOKEN is required; refusing to start a fail-open sandbox service")
	}
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SANDBOX_MODE")), "gateway") {
		runGateway()
		return
	}
	runEngine()
}

func sandboxListenAddr() string {
	if addr := strings.TrimSpace(os.Getenv("SANDBOX_LISTEN_ADDR")); addr != "" {
		return addr
	}
	return ":8082"
}

func runEngine() {
	osURL := os.Getenv("OPEN_SANDBOX_URL")
	enableFallback := strings.EqualFold(os.Getenv("ENABLE_OPENSANDBOX_FALLBACK"), "true")
	listenAddr := sandboxListenAddr()

	svc := NewSandboxService(Config{
		OpenSandboxURL: osURL,
		EnableFallback: enableFallback,
		ListenAddr:     listenAddr,
	})

	r := gin.Default()
	api := r.Group("/api/v1", sandboxAuthMiddleware())
	{
		api.GET("/health", svc.Health)
		api.GET("/capabilities", svc.Capabilities)
		api.POST("/images/ensure", svc.EnsureImage)
		api.POST("/sandboxes", svc.CreateSandbox)
		api.GET("/sandboxes", svc.ListSandboxes)
		api.POST("/sandboxes/cleanup", svc.CleanupSandboxes)
		api.DELETE("/sandboxes/:id", svc.DeleteSandbox)
		api.POST("/sandboxes/:id/network", svc.SetSandboxNetwork)
		api.GET("/sandboxes/:id/usage", svc.SandboxUsage)
		api.GET("/runtime/status", svc.RuntimeStatus)
		api.POST("/runtime/install", svc.InstallRuntime)
		api.POST("/runtime/cleanup", svc.CleanupRuntime)
		api.POST("/sandboxes/:id/python", svc.ExecutePython)
		api.POST("/sandboxes/:id/python/stream", svc.ExecutePythonStream)
		api.POST("/sandboxes/:id/commands", svc.ExecuteCommand)
		api.POST("/sandboxes/:id/commands/stream", svc.ExecuteCommandStream)
	}

	go startSandboxReaper(context.Background(), svc)

	log.Printf("Docker Sandbox Engine starting on %s, fallback=%t, opensandbox=%s", listenAddr, enableFallback, osURL)
	if err := r.Run(listenAddr); err != nil {
		log.Fatalf("sandbox engine stopped: %v", err)
	}
}
