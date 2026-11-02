package shell

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"research-assistant-desktop/settings"
	"research-assistant-desktop/supervisor"
)

type fakeProcess struct {
	name   string
	wait   chan struct{}
	mu     sync.Mutex
	killed bool
}

func (p *fakeProcess) Start() error { return nil }
func (p *fakeProcess) Wait() error  { <-p.wait; return nil }
func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.killed {
		p.killed = true
		close(p.wait)
	}
	return nil
}
func (p *fakeProcess) Pid() int { return 1 }

type fakeRunner struct {
	mu    sync.Mutex
	procs map[string]*fakeProcess
}

func (r *fakeRunner) Start(spec supervisor.CommandSpec) (supervisor.Process, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.procs == nil {
		r.procs = map[string]*fakeProcess{}
	}
	process := &fakeProcess{name: spec.Path, wait: make(chan struct{})}
	r.procs[spec.Path] = process
	return process, nil
}

func healthServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
}

func newTestShell(t *testing.T, health *httptest.Server) *Shell {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		AppVersion:         "test",
		SettingsPath:       filepath.Join(dir, "settings.json"),
		DataDir:            filepath.Join(dir, "data"),
		LogDir:             filepath.Join(dir, "logs"),
		BackendPath:        "backend",
		SandboxPath:        "sandbox",
		StaticFS:           fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>desktop index</html>")}},
		Runner:             &fakeRunner{},
		SecretStore:        settings.NewMemorySecretStore(),
		HealthTimeout:      2 * time.Second,
		LockPath:           filepath.Join(dir, "lock"),
		MaxRestarts:        3,
		RestartBaseDelay:   time.Millisecond,
		BackendHealthURL:   health.URL + "/api/health",
		SandboxHealthURL:   health.URL + "/api/v1/health",
		BackendURLOverride: health.URL,
		SandboxURLOverride: health.URL,
	}
	return New(cfg)
}

func doRequest(handler http.Handler, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Host = "127.0.0.1:12345"
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestStartServeAndStop(t *testing.T) {
	health := healthServer()
	defer health.Close()
	shell := newTestShell(t, health)
	if err := shell.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer shell.Stop(context.Background())
	status := shell.Status()
	if status.State != StateRunning || status.ProxyURL == "" {
		t.Fatalf("status = %#v", status)
	}
	index := doRequest(shell.Handler(), http.MethodGet, "/")
	if index.Code != http.StatusOK || !strings.Contains(index.Body.String(), "desktop index") {
		t.Fatalf("index = %d %q", index.Code, index.Body.String())
	}
	state := doRequest(shell.Handler(), http.MethodGet, "/desktop/state")
	if state.Code != http.StatusOK {
		t.Fatalf("state = %d", state.Code)
	}
	var decoded Status
	if err := json.Unmarshal(state.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.State != StateRunning {
		t.Fatalf("decoded state = %#v", decoded)
	}
}

func TestStartFailureShowsErrorPage(t *testing.T) {
	health := healthServer()
	defer health.Close()
	shell := newTestShell(t, health)
	shell.cfg.SandboxHealthURL = "http://127.0.0.1:1/api/v1/health"
	shell.cfg.HealthTimeout = 150 * time.Millisecond
	if err := shell.Start(context.Background()); err == nil {
		t.Fatal("Start should fail")
	}
	status := shell.Status()
	if status.State != StateFailed || status.Error == "" {
		t.Fatalf("status = %#v", status)
	}
	page := doRequest(shell.Handler(), http.MethodGet, "/")
	if page.Code != http.StatusServiceUnavailable {
		t.Fatalf("error page code = %d", page.Code)
	}
	if !strings.Contains(page.Body.String(), "启动失败") || !strings.Contains(page.Body.String(), status.LogDir) {
		t.Fatalf("error page missing content: %s", page.Body.String())
	}
}

func TestRestart(t *testing.T) {
	health := healthServer()
	defer health.Close()
	shell := newTestShell(t, health)
	if err := shell.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer shell.Stop(context.Background())
	if err := shell.Restart(context.Background()); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	status := shell.Status()
	if status.State != StateRunning || status.Restarts != 1 {
		t.Fatalf("status = %#v", status)
	}
}

func TestDiagnosticsEndpoint(t *testing.T) {
	health := healthServer()
	defer health.Close()
	shell := newTestShell(t, health)
	if err := shell.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer shell.Stop(context.Background())
	if err := os.MkdirAll(shell.cfg.LogDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shell.cfg.LogDir, "shell.log"), []byte("hello shell"), 0o600); err != nil {
		t.Fatal(err)
	}
	response := doRequest(shell.Handler(), http.MethodPost, "/desktop/diagnostics")
	if response.Code != http.StatusOK {
		t.Fatalf("diagnostics = %d %s", response.Code, response.Body.String())
	}
	body, _ := io.ReadAll(response.Body)
	var payload struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(payload.Path); err != nil {
		t.Fatalf("diagnostics file: %v", err)
	}
}

func TestPrepareServesLoadingPage(t *testing.T) {
	health := healthServer()
	defer health.Close()
	shell := newTestShell(t, health)
	if err := shell.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	defer shell.Stop(context.Background())
	proxyURL := shell.ProxyURL()
	if proxyURL == "" {
		t.Fatal("ProxyURL is empty after Prepare")
	}
	response, err := http.Get(proxyURL + "/")
	if err != nil {
		t.Fatalf("GET loading page: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "正在启动") {
		t.Fatalf("loading page = %d %q", response.StatusCode, string(body))
	}
	if err := shell.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if shell.ProxyURL() != proxyURL {
		t.Fatalf("Start moved the proxy from %q to %q", proxyURL, shell.ProxyURL())
	}
	response, err = http.Get(proxyURL + "/")
	if err != nil {
		t.Fatalf("GET frontend: %v", err)
	}
	body, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "desktop index") {
		t.Fatalf("frontend = %d %q", response.StatusCode, string(body))
	}
}

func TestHandlerRejectsNonLoopbackHost(t *testing.T) {
	health := healthServer()
	defer health.Close()
	shell := newTestShell(t, health)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Host = "evil.example.com"
	recorder := httptest.NewRecorder()
	shell.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("code = %d", recorder.Code)
	}
}
