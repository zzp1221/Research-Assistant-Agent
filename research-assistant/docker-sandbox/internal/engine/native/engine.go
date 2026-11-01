package native

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"docker-sandbox/internal/engine"
)

// Config configures the native provider.
type Config struct {
	DataDir       string
	WorkspaceRoot string
	UVPath        string
	PythonVersion string
	Runner        string // auto | appcontainer | lowil
	MemoryMB      int
	CPUCores      int
	MaxProcesses  int
}

// Provider implements engine.Provider on Windows without Docker.
type Provider struct {
	cfg         Config
	runtimes    *RuntimeManager
	runner      commandRunner
	mu          sync.Mutex
	sessions    map[string]*nativeSession
	probeMu     sync.Mutex
	probeReport *ProbeReport

	installMu    sync.Mutex
	installing   bool
	installStage string
	installError string
}

type nativeSession struct {
	id         string
	workspace  string
	venvPython string
	isolation  string
	networkOn  bool
	limits     engine.Limits
	envHash    string
	createdAt  time.Time
}

// NewProvider creates the native provider. On platforms without a native
// runner it returns ErrUnsupported.
func NewProvider(cfg Config) (*Provider, error) {
	if strings.TrimSpace(cfg.DataDir) == "" {
		return nil, fmt.Errorf("native provider: data dir is required")
	}
	if strings.TrimSpace(cfg.WorkspaceRoot) == "" {
		cfg.WorkspaceRoot = filepath.Join(cfg.DataDir, "workspaces")
	}
	runner, err := newCommandRunner(cfg)
	if err != nil {
		return nil, err
	}
	uvPath := strings.TrimSpace(cfg.UVPath)
	if uvPath == "" {
		uvPath = "uv"
	}
	runtimes := NewRuntimeManager(uvPath, filepath.Join(cfg.DataDir, "runtimes", "python"), filepath.Join(cfg.DataDir, "runtimes", "venvs"), filepath.Join(cfg.DataDir, "logs"))
	return &Provider{cfg: cfg, runtimes: runtimes, runner: runner, sessions: map[string]*nativeSession{}}, nil
}

func (p *Provider) Name() string { return "native" }

func (p *Provider) Capabilities(context.Context) (engine.ProviderCapabilities, error) {
	isolation := NormalizeIsolation(p.cfg.Runner)
	if isolation == IsolationAuto {
		isolation = IsolationLowIL
		p.probeMu.Lock()
		if p.probeReport != nil && p.probeReport.AppContainerOK {
			isolation = IsolationAppContainer
		}
		p.probeMu.Unlock()
	}
	enforced := "none"
	if isolation == "appcontainer" {
		enforced = "os"
	}
	var probe *engine.ProbeReport
	p.probeMu.Lock()
	if p.probeReport != nil {
		copied := *p.probeReport
		probe = &copied
	}
	p.probeMu.Unlock()
	return engine.ProviderCapabilities{
		Probe:           probe,
		Provider:        "native",
		Isolation:       isolation,
		Images:          false,
		NetworkControl:  isolation == IsolationAppContainer,
		NetworkEnforced: enforced,
		GPU:             true,
		GPUEnforced:     false,
		PythonVersions:  []string{"3.9", "3.10", "3.11", "3.12"},
		OS:              runtime.GOOS,
		Notes:           []string{"native mode is not container-grade isolation"},
	}, nil
}

func (p *Provider) Create(ctx context.Context, spec engine.CreateSpec) (engine.Session, error) {
	workspace := strings.TrimSpace(spec.MountPath)
	if workspace == "" {
		workspace = filepath.Join(p.cfg.WorkspaceRoot, "nt-"+randomID())
	}
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		return engine.Session{}, fmt.Errorf("create native workspace: %w", err)
	}
	runtimeInfo, err := p.runtimes.EnsureRuntime(ctx, spec.PythonVer, nil)
	if err != nil {
		return engine.Session{}, err
	}
	id := "nt-" + randomID()
	isolation := NormalizeIsolation(p.cfg.Runner)
	if isolation == IsolationAuto {
		report := p.probe(ctx, runtimeInfo.PythonPath)
		isolation = SelectIsolation(p.cfg.Runner, &report, false)
	}
	if isolation == IsolationAppContainer {
		if err := prepareAppContainerWorkspace(workspace); err != nil {
			return engine.Session{}, fmt.Errorf("prepare appcontainer workspace: %w", err)
		}
	}
	limits := spec.Limits
	if limits.MemoryMB <= 0 {
		limits.MemoryMB = p.cfg.MemoryMB
	}
	if limits.CPUPercent <= 0 {
		limits.CPUPercent = cpuPercent(p.cfg.CPUCores)
	}
	if limits.MaxProcesses <= 0 {
		limits.MaxProcesses = p.cfg.MaxProcesses
	}
	session := &nativeSession{id: id, workspace: workspace, venvPython: runtimeInfo.PythonPath, isolation: isolation, limits: limits, envHash: runtimeInfo.EnvHash, createdAt: time.Now()}
	p.mu.Lock()
	p.sessions[id] = session
	p.mu.Unlock()
	return engine.Session{ID: id, Provider: "native", Isolation: isolation, Workspace: workspace, VenvPath: runtimeInfo.VenvPath, CreatedAt: session.createdAt}, nil
}

func (p *Provider) Delete(_ context.Context, id string) error {
	p.mu.Lock()
	delete(p.sessions, id)
	p.mu.Unlock()
	return nil
}

func (p *Provider) Exec(ctx context.Context, id string, command engine.Command, emit func(engine.Event) error) (*engine.Result, error) {
	session, err := p.session(id)
	if err != nil {
		return nil, err
	}
	translated, err := TranslateCommand(session.workspace, session.venvPython, command)
	if err != nil {
		return nil, err
	}
	result, err := p.runner.Run(ctx, runSpec{
		Argv:        translated.Argv,
		Cwd:         translated.Cwd,
		Env:         translated.Env,
		Stdin:       translated.Stdin,
		MaxOutputKB: translated.MaxOutputKB,
		Timeout:     translated.Timeout,
		Limits:      session.limits,
		NetworkOn:   session.networkOn,
		Emit:        emit,
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (p *Provider) InstallPackages(ctx context.Context, id string, packages []string) (*engine.Result, error) {
	argv := append([]string{"python", "-m", "pip", "install"}, packages...)
	return p.Exec(ctx, id, engine.Command{Argv: argv, Cwd: "/workspace"}, nil)
}

func (p *Provider) SetNetwork(_ context.Context, id string, on bool) error {
	session, err := p.session(id)
	if err != nil {
		return err
	}
	if session.isolation != IsolationAppContainer {
		return fmt.Errorf("native network switching is unsupported in %s mode; only appcontainer enforces network at the OS level", session.isolation)
	}
	session.networkOn = on
	return nil
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
	return engine.Usage{DiskUsedMB: float64(total) / 1024 / 1024, RuntimeHash: session.envHash}, nil
}

func (p *Provider) Reap(_ context.Context, ttl time.Duration) (int, error) {
	if ttl <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-ttl)
	removed := 0
	p.mu.Lock()
	for id, session := range p.sessions {
		if session.createdAt.Before(cutoff) {
			delete(p.sessions, id)
			removed++
		}
	}
	p.mu.Unlock()
	return removed, nil
}

func (p *Provider) Close() error {
	if p.runner != nil {
		return p.runner.Close()
	}
	return nil
}

// probe runs the AppContainer compatibility probe once and caches the report.
func (p *Provider) probe(ctx context.Context, pythonPath string) ProbeReport {
	p.probeMu.Lock()
	defer p.probeMu.Unlock()
	if p.probeReport != nil {
		return *p.probeReport
	}
	report := runProbe(ctx, pythonPath, filepath.Join(p.cfg.DataDir, "probe"))
	p.probeReport = &report
	if p.runner != nil {
		p.runner.SetProbe(&report)
	}
	return report
}

func (p *Provider) session(id string) (*nativeSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	session, ok := p.sessions[id]
	if !ok {
		return nil, fmt.Errorf("native sandbox %q not found", id)
	}
	return session, nil
}

// cpuPercent converts a core budget into a Job Object CPU rate percentage.
func cpuPercent(cores int) int {
	if cores <= 0 {
		return 0
	}
	total := runtime.NumCPU()
	if total <= 0 {
		return 0
	}
	percent := cores * 100 / total
	if percent < 1 {
		percent = 1
	}
	if percent > 100 {
		percent = 100
	}
	return percent
}

func randomID() string {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw)
}

// EnvPairs returns sorted KEY=VALUE pairs for a command.
func EnvPairs(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}
