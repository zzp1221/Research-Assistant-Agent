package main

import (
	"context"
	"crypto/subtle"
	"docker-sandbox/internal/engine"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	osEngine *engine.OpenSandboxEngine
	dkEngine *engine.NativeDockerEngine
	config   Config

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

	return &SandboxService{
		osEngine: osEngine,
		dkEngine: engine.NewNativeDockerEngine(),
		config:   cfg,
		limits:   map[string]engine.Limits{},
		network:  map[string]string{},
	}
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

func configureFileLogging() {
	logDir := filepath.Join(resolveProjectRoot(), "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("failed to create log dir %s: %v", logDir, err)
		return
	}

	logFile := filepath.Join(logDir, "docker-sandbox.log")
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("failed to open sandbox log file %s: %v", logFile, err)
		return
	}

	writer := io.MultiWriter(os.Stdout, file)
	log.SetOutput(writer)
	gin.DefaultWriter = writer
	gin.DefaultErrorWriter = writer
	log.Printf("sandbox logs redirected to %s", logFile)
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
	Cmd []string `json:"cmd"`
}

// --- 处理器 ---

func (s *SandboxService) Health(c *gin.Context) {
	dockerHealth := s.dkEngine.Health(c.Request.Context())
	c.JSON(http.StatusOK, gin.H{
		"ok":                 dockerHealth.Available,
		"native_docker":      dockerHealth,
		"fallback_enabled":   s.config.EnableFallback,
		"opensandbox_url":    s.config.OpenSandboxURL,
		"effective_strategy": chooseHealthStrategy(dockerHealth.Available, s.config.EnableFallback),
	})
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
			if removed > 0 {
				log.Printf("[Sandbox] reaper removed %d stale sandbox container(s) (ttl=%dm)", removed, ttlMinutes)
			}
		}
	}
}

func (s *SandboxService) Capabilities(c *gin.Context) {
	c.JSON(http.StatusOK, s.dkEngine.Capabilities(c.Request.Context()))
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

	if strings.HasPrefix(id, "os-") {
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

	if strings.HasPrefix(id, "os-") {
		if s.osEngine == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "opensandbox fallback is disabled"})
			return
		}
		res, err = s.osEngine.ExecuteCommand(c.Request.Context(), strings.TrimPrefix(id, "os-"), req.Cmd)
	} else if strings.HasPrefix(id, "dk-") {
		res, err = s.dkEngine.ExecuteCommand(ctx, containerID, req.Cmd)
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
	switch {
	case strings.HasPrefix(id, "dk-"):
		res, err := s.dkEngine.ExecuteCommandStream(ctx, containerID, req.Cmd, emit)
		if err != nil {
			writeStreamError(c.Writer, flushWriter, err)
			return
		}
		if guardErr := s.enforceUsage(c.Request.Context(), containerID, res); guardErr != nil {
			writeStreamError(c.Writer, flushWriter, guardErr)
		}
	case strings.HasPrefix(id, "os-"):
		if s.osEngine == nil {
			writeStreamError(c.Writer, flushWriter, fmt.Errorf("opensandbox fallback is disabled"))
			return
		}
		res, err := s.osEngine.ExecuteCommand(ctx, strings.TrimPrefix(id, "os-"), req.Cmd)
		if err != nil {
			writeStreamError(c.Writer, flushWriter, err)
			return
		}
		_ = emit(engine.ExecutionStreamEvent{Type: "final", Response: res})
	default:
		writeStreamError(c.Writer, flushWriter, fmt.Errorf("unknown sandbox id prefix"))
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
