package vm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"docker-sandbox/internal/engine"
)

// Config configures the WSL2 VM provider.
type Config struct {
	Detection                Detection
	DataDir                  string
	WorkspaceRoot            string
	DefaultDistro            string
	WSLPath                  string
	AllowUnsupportedPlatform bool
}

// Provider executes commands through wsl.exe. It is only functional on Windows
// with a WSL2 distribution; elsewhere it fails explicitly.
type Provider struct {
	cfg      Config
	mu       sync.Mutex
	sessions map[string]*vmSession
}

type vmSession struct {
	id        string
	workspace string
	distro    string
	createdAt time.Time
}

// NewProvider probes WSL2 and uses the default per-user workspace root.
func NewProvider(ctx context.Context) *Provider {
	return NewProviderWithConfig(ctx, DefaultConfig())
}

// NewProviderWithConfig builds a provider from an explicit config.
func NewProviderWithConfig(ctx context.Context, cfg Config) *Provider {
	if strings.TrimSpace(cfg.DataDir) == "" {
		cfg.DataDir = DefaultConfig().DataDir
	}
	if strings.TrimSpace(cfg.WorkspaceRoot) == "" {
		cfg.WorkspaceRoot = filepath.Join(cfg.DataDir, "workspaces")
	}
	if !cfg.Detection.Available && cfg.Detection.Reason == "" && len(cfg.Detection.Distributions) == 0 {
		cfg.Detection = Detect(ctx)
	}
	return &Provider{cfg: cfg, sessions: map[string]*vmSession{}}
}

func (p *Provider) Name() string { return "vm" }

func (p *Provider) wslPath() string {
	if path := strings.TrimSpace(p.cfg.WSLPath); path != "" {
		return path
	}
	return "wsl.exe"
}

// Available reports whether a WSL2 distribution was detected.
func (p *Provider) Available() bool { return p.cfg.Detection.Available }

// DetectionReason explains why the provider is unavailable.
func (p *Provider) DetectionReason() string { return p.cfg.Detection.Reason }

func (p *Provider) Capabilities(context.Context) (engine.ProviderCapabilities, error) {
	notes := []string{"WSL2 VM provider"}
	if p.cfg.Detection.Reason != "" {
		notes = append(notes, p.cfg.Detection.Reason)
	}
	return engine.ProviderCapabilities{
		Provider:        "vm",
		Isolation:       "vm",
		Images:          true,
		NetworkControl:  false,
		NetworkEnforced: "os",
		GPU:             true,
		GPUEnforced:     false,
		PythonVersions:  []string{"3.9", "3.10", "3.11", "3.12"},
		OS:              runtime.GOOS,
		Notes:           notes,
	}, nil
}

func (p *Provider) Create(_ context.Context, spec engine.CreateSpec) (engine.Session, error) {
	if runtime.GOOS != "windows" && !p.cfg.AllowUnsupportedPlatform {
		return engine.Session{}, ErrUnavailable
	}
	if !p.cfg.Detection.Available {
		return engine.Session{}, fmt.Errorf("%w: %s", ErrUnavailable, p.cfg.Detection.Reason)
	}
	distro := strings.TrimSpace(p.cfg.DefaultDistro)
	if distro == "" {
		distro = p.cfg.Detection.DefaultDistribution
	}
	workspace := strings.TrimSpace(spec.MountPath)
	if workspace == "" {
		workspace = filepath.Join(p.cfg.WorkspaceRoot, "vm-"+randomID())
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return engine.Session{}, fmt.Errorf("create vm workspace: %w", err)
	}
	session := &vmSession{id: "vm-" + randomID(), workspace: workspace, distro: distro, createdAt: time.Now()}
	p.mu.Lock()
	p.sessions[session.id] = session
	p.mu.Unlock()
	return engine.Session{ID: session.id, Provider: "vm", Isolation: "vm", Workspace: workspace, CreatedAt: session.createdAt}, nil
}

func (p *Provider) Delete(_ context.Context, id string) error {
	p.mu.Lock()
	session := p.sessions[id]
	delete(p.sessions, id)
	p.mu.Unlock()
	if session == nil {
		return fmt.Errorf("vm sandbox %q not found", id)
	}
	if session.workspace != "" && strings.HasPrefix(session.workspace, p.cfg.WorkspaceRoot) {
		_ = os.RemoveAll(session.workspace)
	}
	return nil
}

func (p *Provider) Exec(ctx context.Context, id string, command engine.Command, emit func(engine.Event) error) (*engine.Result, error) {
	if runtime.GOOS != "windows" && !p.cfg.AllowUnsupportedPlatform {
		return nil, ErrUnavailable
	}
	session, err := p.session(id)
	if err != nil {
		return nil, err
	}
	argv, cwd, err := TranslateVMCommand(session.workspace, command)
	if err != nil {
		return nil, err
	}
	args := BuildWSLArgsWithEnv(session.distro, cwd, command.Env, argv)
	if command.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, command.Timeout)
		defer cancel()
	}
	started := time.Now()
	cmd := exec.CommandContext(ctx, p.wslPath(), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("wsl exec: %w", runErr)
		}
	}
	result := &engine.Result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode, DurationMS: time.Since(started).Milliseconds()}
	if limit := command.MaxOutputKB * 1024; limit > 0 {
		if len(result.Stdout) > limit {
			result.Stdout = result.Stdout[:limit] + "...[truncated]"
			result.Truncated = true
		}
		if len(result.Stderr) > limit {
			result.Stderr = result.Stderr[:limit] + "...[truncated]"
			result.Truncated = true
		}
	}
	if emit != nil {
		if result.Stdout != "" {
			_ = emit(engine.Event{Type: "chunk", Stream: "stdout", Message: result.Stdout})
		}
		if result.Stderr != "" {
			_ = emit(engine.Event{Type: "chunk", Stream: "stderr", Message: result.Stderr})
		}
	}
	return result, nil
}

func (p *Provider) InstallPackages(ctx context.Context, id string, packages []string) (*engine.Result, error) {
	argv := append([]string{"python3", "-m", "pip", "install"}, packages...)
	return p.Exec(ctx, id, engine.Command{Argv: argv, Cwd: "/workspace"}, nil)
}

func (p *Provider) SetNetwork(context.Context, string, bool) error {
	return fmt.Errorf("vm provider network switching is not implemented; WSL2 uses the host network")
}

func (p *Provider) Usage(_ context.Context, id string) (engine.Usage, error) {
	session, err := p.session(id)
	if err != nil {
		return engine.Usage{}, err
	}
	var total int64
	_ = filepath.WalkDir(session.workspace, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return nil
		}
		if info, statErr := entry.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return engine.Usage{DiskUsedMB: float64(total) / 1024 / 1024}, nil
}

func (p *Provider) Reap(_ context.Context, ttl time.Duration) (int, error) {
	if ttl <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-ttl)
	removed := 0
	p.mu.Lock()
	stale := make([]*vmSession, 0)
	for id, session := range p.sessions {
		if session.createdAt.Before(cutoff) {
			stale = append(stale, session)
			delete(p.sessions, id)
			removed++
		}
	}
	p.mu.Unlock()
	for _, session := range stale {
		if session.workspace != "" && strings.HasPrefix(session.workspace, p.cfg.WorkspaceRoot) {
			_ = os.RemoveAll(session.workspace)
		}
	}
	return removed, nil
}

func (p *Provider) Close() error { return nil }

func (p *Provider) session(id string) (*vmSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	session, ok := p.sessions[id]
	if !ok {
		return nil, fmt.Errorf("vm sandbox %q not found", id)
	}
	return session, nil
}

func randomID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}
