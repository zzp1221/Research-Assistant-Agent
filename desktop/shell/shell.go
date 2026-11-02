// Package shell wires settings, child processes, the reverse proxy and the
// desktop HTTP surface into one lifecycle.
package shell

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"research-assistant-desktop/diagnostics"
	"research-assistant-desktop/proxy"
	"research-assistant-desktop/settings"
	"research-assistant-desktop/supervisor"
)

// State is the desktop shell lifecycle state.
type State string

const (
	StateIdle       State = "idle"
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateRestarting State = "restarting"
	StateFailed     State = "failed"
	StateStopped    State = "stopped"
)

// Status is the snapshot exposed to the UI and the tray.
type Status struct {
	State              State  `json:"state"`
	Error              string `json:"error,omitempty"`
	LogDir             string `json:"log_dir"`
	DataDir            string `json:"data_dir"`
	ProxyURL           string `json:"proxy_url,omitempty"`
	BackendURL         string `json:"backend_url,omitempty"`
	SandboxURL         string `json:"sandbox_url,omitempty"`
	AppVersion         string `json:"app_version"`
	Restarts           int    `json:"restarts"`
	LastRestartAt      string `json:"last_restart_at,omitempty"`
	OnboardingRequired bool   `json:"onboarding_required"`
}

// Event is emitted for lifecycle transitions.
type Event struct {
	Type    string
	Message string
}

// Config configures a Shell.
type Config struct {
	AppVersion          string
	SettingsPath        string
	DataDir             string
	LogDir              string
	BackendPath         string
	SandboxPath         string
	BackendArgs         []string
	SandboxArgs         []string
	StaticFS            fs.FS
	Runner              supervisor.Runner
	SecretStore         settings.SecretStore
	HealthTimeout       time.Duration
	LockPath            string
	ListenAddr          string
	MaxRestarts         int
	RestartBaseDelay    time.Duration
	HealthCheckInterval time.Duration
	BackendHealthURL    string
	SandboxHealthURL    string
	BackendURLOverride  string
	SandboxURLOverride  string
}

// Shell owns the desktop process tree and loopback server.
type Shell struct {
	cfg Config

	mu         sync.RWMutex
	status     Status
	settings   settings.Settings
	paths      settings.Paths
	ports      settings.Ports
	backendTok string
	sandboxTok string
	sup        *supervisor.Supervisor
	server     *http.Server
	listener   net.Listener
	proxyURL   string
	handler    http.Handler
	monitorOff context.CancelFunc
}

// New creates a Shell.
func New(cfg Config) *Shell {
	return &Shell{cfg: cfg, status: Status{State: StateIdle, AppVersion: cfg.AppVersion, LogDir: cfg.LogDir, DataDir: cfg.DataDir}}
}

// Prepare binds the loopback listener and serves the loading page before the
// child services start, so a desktop window can open immediately. Start calls
// it when it has not been called yet.
func (s *Shell) Prepare() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return nil
	}
	listenAddr := s.cfg.ListenAddr
	if listenAddr == "" {
		listenAddr = "127.0.0.1:0"
	}
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen proxy: %w", err)
	}
	server := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	s.listener = listener
	s.server = server
	s.proxyURL = "http://" + listener.Addr().String()
	s.status.ProxyURL = s.proxyURL
	go func() {
		_ = server.Serve(listener)
	}()
	return nil
}

// Start launches the backend and sandbox, then serves the loopback UI proxy.
func (s *Shell) Start(ctx context.Context) error {
	s.mu.Lock()
	if s.status.State == StateStarting || s.status.State == StateRunning || s.status.State == StateRestarting {
		s.mu.Unlock()
		return fmt.Errorf("shell is already started")
	}
	s.status.State = StateStarting
	s.status.Error = ""
	s.mu.Unlock()

	cfg, err := settings.Load(s.cfg.SettingsPath)
	if err != nil {
		return s.fail(fmt.Errorf("load settings: %w", err))
	}
	apiKey := ""
	if s.cfg.SecretStore != nil {
		ref := strings.TrimSpace(cfg.LLM.APIKeyRef)
		keyName := strings.TrimPrefix(ref, "dpapi:")
		if keyName == "" {
			keyName = "default"
		}
		if value, err := s.cfg.SecretStore.Load(keyName); err == nil {
			apiKey = string(value)
		}
	}
	ports, err := pickPorts(3)
	if err != nil {
		return s.fail(fmt.Errorf("pick ports: %w", err))
	}
	backendToken, err := randomToken()
	if err != nil {
		return s.fail(err)
	}
	sandboxToken, err := randomToken()
	if err != nil {
		return s.fail(err)
	}
	dataPaths := settings.DefaultPaths(s.cfg.DataDir, s.cfg.LogDir)
	backendURL := s.cfg.BackendURLOverride
	if backendURL == "" {
		backendURL = fmt.Sprintf("http://127.0.0.1:%d", ports.Backend)
	}
	sandboxURL := s.cfg.SandboxURLOverride
	if sandboxURL == "" {
		sandboxURL = fmt.Sprintf("http://127.0.0.1:%d", ports.Sandbox)
	}
	backendHealth := s.cfg.BackendHealthURL
	if backendHealth == "" {
		backendHealth = backendURL + "/api/health"
	}
	sandboxHealth := s.cfg.SandboxHealthURL
	if sandboxHealth == "" {
		sandboxHealth = sandboxURL + "/api/v1/health"
	}
	runner := s.cfg.Runner
	if runner == nil {
		runner = supervisor.ExecRunner{}
	}
	sup := supervisor.New(supervisor.Config{
		Backend: supervisor.CommandSpec{
			Path: s.cfg.BackendPath,
			Args: s.cfg.BackendArgs,
			Env:  settings.WithAPIKey(settings.BackendEnv(cfg, dataPaths, ports, backendToken, sandboxToken), apiKey),
		},
		Sandbox: supervisor.CommandSpec{
			Path: s.cfg.SandboxPath,
			Args: s.cfg.SandboxArgs,
			Env:  settings.SandboxEnv(cfg, dataPaths, ports, sandboxToken),
		},
		BackendHealth: backendHealth,
		SandboxHealth: sandboxHealth,
		HealthTimeout: s.cfg.HealthTimeout,
		LockPath:      s.cfg.LockPath,
	}, runner)
	// Keep the supervisor reference even when the first start fails so the
	// error-page "retry" action can stop leftovers and start again.
	s.mu.Lock()
	s.sup = sup
	s.mu.Unlock()
	if err := sup.Start(ctx); err != nil {
		return s.fail(fmt.Errorf("start services: %w", err))
	}
	proxyHandler, err := proxy.New(proxy.Config{BackendURL: backendURL, StaticFS: s.cfg.StaticFS})
	if err != nil {
		_ = sup.Stop(context.Background())
		return s.fail(fmt.Errorf("create proxy: %w", err))
	}
	if err := s.Prepare(); err != nil {
		_ = sup.Stop(context.Background())
		return s.fail(err)
	}
	s.mu.Lock()
	s.settings = cfg
	s.paths = dataPaths
	s.ports = ports
	s.backendTok = backendToken
	s.sandboxTok = sandboxToken
	s.sup = sup
	s.handler = proxyHandler
	s.status.State = StateRunning
	s.status.BackendURL = backendURL
	s.status.SandboxURL = sandboxURL
	s.status.OnboardingRequired = !cfg.OnboardingCompleted
	s.status.LogDir = s.cfg.LogDir
	s.status.DataDir = s.cfg.DataDir
	s.mu.Unlock()
	s.startMonitor()
	return nil
}

// Stop shuts down the proxy, then sandbox, backend and the single-instance lock.
func (s *Shell) Stop(ctx context.Context) error {
	s.mu.Lock()
	if cancel := s.monitorOff; cancel != nil {
		cancel()
		s.monitorOff = nil
	}
	server := s.server
	s.server = nil
	listener := s.listener
	s.listener = nil
	sup := s.sup
	s.sup = nil
	s.status.State = StateStopped
	s.mu.Unlock()
	if server != nil {
		_ = server.Shutdown(ctx)
	}
	if listener != nil {
		_ = listener.Close()
	}
	if sup != nil {
		return sup.Stop(ctx)
	}
	return nil
}

// Restart stops and starts the child processes with exponential backoff.
func (s *Shell) Restart(ctx context.Context) error {
	s.mu.Lock()
	sup := s.sup
	if sup == nil {
		s.mu.Unlock()
		return fmt.Errorf("shell is not running")
	}
	attempt := s.status.Restarts
	s.status.State = StateRestarting
	s.mu.Unlock()
	if err := sup.Stop(ctx); err != nil {
		return s.fail(fmt.Errorf("stop for restart: %w", err))
	}
	delay := s.cfg.RestartBaseDelay
	if delay <= 0 {
		delay = time.Second
	}
	if attempt > 0 {
		shift := attempt
		if shift > 3 {
			shift = 3
		}
		delay *= time.Duration(1 << shift)
	}
	select {
	case <-ctx.Done():
		return s.fail(ctx.Err())
	case <-time.After(delay):
	}
	if err := sup.Start(ctx); err != nil {
		return s.fail(fmt.Errorf("restart services: %w", err))
	}
	s.mu.Lock()
	s.status.State = StateRunning
	s.status.Error = ""
	s.status.Restarts++
	s.status.LastRestartAt = time.Now().UTC().Format(time.RFC3339)
	s.mu.Unlock()
	s.emit(Event{Type: "restarted", Message: "services restarted"})
	return nil
}

// Status returns a copy of the current status.
func (s *Shell) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// ProxyURL returns the loopback URL served by the shell.
func (s *Shell) ProxyURL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.proxyURL
}

// Handler serves the desktop UI and local desktop endpoints.
func (s *Shell) Handler() http.Handler {
	return http.HandlerFunc(s.serveHTTP)
}

// BuildDiagnostics creates a redacted support bundle for the current state.
func (s *Shell) BuildDiagnostics(ctx context.Context) (string, error) {
	status := s.Status()
	s.mu.RLock()
	cfg := s.settings
	backendToken := s.backendTok
	sandboxToken := s.sandboxTok
	s.mu.RUnlock()
	return diagnostics.Build(ctx, diagnostics.Options{
		AppVersion: s.cfg.AppVersion, DataDir: s.cfg.DataDir, LogDir: s.cfg.LogDir,
		CrashDir: filepath.Join(s.cfg.DataDir, "crashes"),
		Settings: cfg, BackendURL: status.BackendURL, SandboxURL: status.SandboxURL,
		BackendToken: backendToken, SandboxToken: sandboxToken,
	})
}

func (s *Shell) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackHost(r.Host) {
		http.Error(w, "forbidden host", http.StatusForbidden)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/desktop/") {
		s.handleDesktop(w, r)
		return
	}
	s.mu.RLock()
	state := s.status.State
	handler := s.handler
	status := s.status
	s.mu.RUnlock()
	if state == StateRunning && handler != nil {
		handler.ServeHTTP(w, r)
		return
	}
	if state == StateFailed {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": status.Error})
			return
		}
		writeHTML(w, http.StatusServiceUnavailable, errorPageHTML(status))
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "desktop shell is starting"})
		return
	}
	writeHTML(w, http.StatusOK, loadingPageHTML(status))
}

func (s *Shell) handleDesktop(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/desktop/state":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, s.Status())
	case "/desktop/error":
		writeHTML(w, http.StatusServiceUnavailable, errorPageHTML(s.Status()))
	case "/desktop/metrics":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		summary, err := s.FetchMetrics(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, summary)
	case "/desktop/runtime":
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status, err := s.FetchRuntimeStatus(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, status)
	case "/desktop/runtime/install":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var request struct {
			PythonVersion string `json:"python_version"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		status, err := s.InstallRuntime(r.Context(), request.PythonVersion)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, status)
	case "/desktop/runtime/cleanup":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		status, err := s.CleanupRuntimes(r.Context())
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, status)
	case "/desktop/diagnostics":
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		path, err := s.BuildDiagnostics(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"path": path})
	case "/desktop/open-logs":
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := openLogDir(s.cfg.LogDir); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "opened"})
	case "/desktop/retry":
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			_ = s.Restart(ctx)
		}()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "restarting"})
	default:
		http.NotFound(w, r)
	}
}

func (s *Shell) startMonitor() {
	interval := s.cfg.HealthCheckInterval
	if interval <= 0 {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.monitorOff = cancel
	s.mu.Unlock()
	go func() {
		failures := 0
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.healthy(ctx) {
					failures = 0
					continue
				}
				failures++
				if failures < 2 {
					continue
				}
				failures = 0
				max := s.cfg.MaxRestarts
				if max <= 0 {
					max = 3
				}
				s.mu.RLock()
				restarts := s.status.Restarts
				s.mu.RUnlock()
				if restarts >= max {
					_ = s.fail(fmt.Errorf("services failed %d times; see %s", restarts, s.cfg.LogDir))
					return
				}
				restartCtx, restartCancel := context.WithTimeout(context.Background(), 90*time.Second)
				_ = s.Restart(restartCtx)
				restartCancel()
			}
		}
	}()
}

func (s *Shell) healthy(ctx context.Context) bool {
	s.mu.RLock()
	backendURL := s.status.BackendURL
	sandboxURL := s.status.SandboxURL
	s.mu.RUnlock()
	client := &http.Client{Timeout: 2 * time.Second}
	for _, target := range []string{backendURL + "/api/health", sandboxURL + "/api/v1/health"} {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return false
		}
		response, err := client.Do(request)
		if err != nil {
			return false
		}
		_ = response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return false
		}
	}
	return true
}

func (s *Shell) fail(err error) error {
	s.mu.Lock()
	s.status.State = StateFailed
	s.status.Error = err.Error()
	s.mu.Unlock()
	s.emit(Event{Type: "failed", Message: err.Error()})
	return err
}

func (s *Shell) emit(event Event) {
	s.mu.RLock()
	// No listeners yet: events are surfaced through Status. The hook exists so
	// tray/notification code can subscribe without changing the shell API.
	s.mu.RUnlock()
}

func pickPorts(attempts int) (settings.Ports, error) {
	if attempts <= 0 {
		attempts = 3
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		backendPort, err := supervisor.PickPort()
		if err != nil {
			lastErr = err
			continue
		}
		sandboxPort, err := supervisor.PickPort()
		if err != nil {
			lastErr = err
			continue
		}
		if backendPort == sandboxPort {
			lastErr = fmt.Errorf("allocated duplicate port %d", backendPort)
			continue
		}
		return settings.Ports{Backend: backendPort, Sandbox: sandboxPort}, nil
	}
	return settings.Ports{}, lastErr
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeHTML(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}
