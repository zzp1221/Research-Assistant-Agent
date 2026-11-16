package native

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
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
	cfg            Config
	runtimes       *RuntimeManager
	runner         commandRunner
	runtimeMu      sync.RWMutex
	mu             sync.Mutex
	sessions       map[string]*nativeSession
	probeMu        sync.Mutex
	probeReport    *ProbeReport
	probePython    string
	probeCheckedAt time.Time
	// Optional internal dependency for deterministic boundary regression tests.
	probeRun func(context.Context, string, string) ProbeReport

	installMu    sync.Mutex
	installing   bool
	installStage string
	installError string
}

type nativeSession struct {
	id              string
	workspace       string
	venvPython      string
	isolation       string
	networkOn       bool
	limits          engine.Limits
	envHash         string
	createdAt       time.Time
	runtimeCopyRoot string
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
	p.runtimeMu.RLock()
	defer p.runtimeMu.RUnlock()
	isolation := IsolationLowIL
	p.probeMu.Lock()
	if p.probeReport != nil && p.probePython != "" {
		key, err := ProbeEnvironmentKey(p.probePython, "", p.probeConfiguration())
		if err != nil || key != p.probeReport.EnvironmentKey || !probeSnapshotFresh(p.probeCheckedAt, time.Now()) {
			p.probeReport.AppContainerOK = false
			p.probeReport.Reason = "runtime, isolation configuration or probe freshness changed; revalidation required"
			if p.runner != nil {
				p.runner.SetProbe(nil)
			}
		}
	}
	if NormalizeIsolation(p.cfg.Runner) != IsolationLowIL && p.probePython != "" && probeSnapshotFresh(p.probeCheckedAt, time.Now()) && verifiedProbe(p.probeReport) {
		isolation = IsolationAppContainer
	}
	enforced := "none"
	if isolation == "appcontainer" {
		enforced = "os"
	}
	var probe *engine.ProbeReport
	if p.probeReport != nil {
		copied := cloneProbeReport(*p.probeReport)
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

func (p *Provider) Create(ctx context.Context, spec engine.CreateSpec) (created engine.Session, createErr error) {
	p.runtimeMu.RLock()
	defer p.runtimeMu.RUnlock()
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
	selectedPython := runtimeInfo.PythonPath
	copyRoot := ""
	keepCopy := false
	defer func() {
		if copyRoot != "" && !keepCopy {
			if err := p.removeRuntimeCopy(copyRoot); err != nil {
				createErr = errors.Join(createErr, fmt.Errorf("cleanup native runtime copy: %w", err))
			}
		}
	}()
	var report *ProbeReport
	if NormalizeIsolation(p.cfg.Runner) != IsolationLowIL {
		copyRoot = filepath.Join(p.appContainerRuntimeRoot(), id)
		selectedPython, err = prepareAppContainerRuntimeCopy(runtimeInfo.PythonPath, copyRoot, p.runtimes.RuntimeRoot, p.runtimes.VenvRoot)
		if err != nil {
			return engine.Session{}, fmt.Errorf("sandbox_capability: runtime access preparation failed: %w", err)
		}
		current := p.probe(ctx, selectedPython)
		report = &current
	}
	isolation, err := ResolveIsolation(p.cfg.Runner, report, false)
	if err != nil {
		return engine.Session{}, err
	}
	networkMode, err := engine.EffectiveNetworkMode(spec.NetworkMode)
	if err != nil {
		return engine.Session{}, err
	}
	if isolation != IsolationAppContainer && networkMode == engine.NetworkNone {
		return engine.Session{}, fmt.Errorf("sandbox_capability: OS network isolation is required but AppContainer is not verified")
	}
	if isolation != IsolationAppContainer {
		// A compatibility fallback uses the untouched runtime, never a copy
		// carrying AppContainer ACLs that may change low-integrity access.
		selectedPython = runtimeInfo.PythonPath
		if copyRoot != "" {
			if err := p.removeRuntimeCopy(copyRoot); err != nil {
				return engine.Session{}, fmt.Errorf("cleanup unused native runtime copy: %w", err)
			}
			copyRoot = ""
		}
		if err := prepareLowIntegrityWorkspace(workspace); err != nil {
			return engine.Session{}, fmt.Errorf("prepare low-integrity workspace: %w", err)
		}
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
	session := &nativeSession{id: id, workspace: workspace, venvPython: selectedPython, isolation: isolation, networkOn: networkMode != engine.NetworkNone, limits: limits, envHash: runtimeInfo.EnvHash, createdAt: time.Now()}
	if isolation == IsolationAppContainer {
		session.runtimeCopyRoot = copyRoot
		keepCopy = true
	}
	p.mu.Lock()
	p.sessions[id] = session
	p.mu.Unlock()
	venvPath := filepath.Dir(selectedPython)
	if strings.EqualFold(filepath.Base(venvPath), "Scripts") || filepath.Base(venvPath) == "bin" {
		venvPath = filepath.Dir(venvPath)
	}
	return engine.Session{ID: id, Provider: "native", Isolation: isolation, Workspace: workspace, VenvPath: venvPath, CreatedAt: session.createdAt}, nil
}

func (p *Provider) Delete(_ context.Context, id string) error {
	p.runtimeMu.Lock()
	defer p.runtimeMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	session := p.sessions[id]
	if session != nil && session.runtimeCopyRoot != "" {
		if err := p.removeRuntimeCopy(session.runtimeCopyRoot); err != nil {
			return err
		}
	}
	delete(p.sessions, id)
	return nil
}

func (p *Provider) appContainerRuntimeRoot() string {
	return filepath.Join(filepath.Dir(p.runtimes.RuntimeRoot), "appcontainer")
}

func (p *Provider) removeRuntimeCopy(path string) error {
	base, err := filepath.Abs(p.appContainerRuntimeRoot())
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("native runtime copy cleanup target is outside the provider copy directory")
	}
	p.probeMu.Lock()
	probeRel, probeErr := filepath.Rel(target, p.probePython)
	if p.probePython != "" && probeErr == nil && probeRel != ".." && !strings.HasPrefix(probeRel, ".."+string(filepath.Separator)) && !filepath.IsAbs(probeRel) {
		p.probeReport, p.probePython, p.probeCheckedAt = nil, "", time.Time{}
		if p.runner != nil {
			p.runner.SetProbe(nil)
		}
	}
	p.probeMu.Unlock()
	return os.RemoveAll(target)
}

func (p *Provider) Exec(ctx context.Context, id string, command engine.Command, emit func(engine.Event) error) (*engine.Result, error) {
	p.runtimeMu.RLock()
	defer p.runtimeMu.RUnlock()
	session, err := p.session(id)
	if err != nil {
		return nil, err
	}
	if session.isolation == IsolationAppContainer {
		report := p.probe(ctx, session.venvPython)
		if _, err := ResolveIsolation(IsolationAppContainer, &report, false); err != nil {
			return nil, err
		}
	}
	translated, err := TranslateCommand(session.workspace, session.venvPython, command)
	if err != nil {
		return nil, err
	}
	result, err := p.runner.Run(ctx, runSpec{
		Argv:            translated.Argv,
		Cwd:             translated.Cwd,
		Env:             translated.Env,
		Stdin:           translated.Stdin,
		MaxOutputKB:     translated.MaxOutputKB,
		Timeout:         translated.Timeout,
		Limits:          session.limits,
		NetworkOn:       session.networkOn,
		Isolation:       session.isolation,
		EnvironmentRoot: session.workspace,
		Interpreter:     session.venvPython,
		Emit:            emit,
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
	p.mu.Lock()
	defer p.mu.Unlock()
	session, ok := p.sessions[id]
	if !ok {
		return fmt.Errorf("native sandbox %q not found", id)
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
	p.runtimeMu.Lock()
	defer p.runtimeMu.Unlock()
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, session := range p.sessions {
		if session.createdAt.Before(cutoff) {
			if session.runtimeCopyRoot != "" {
				if err := p.removeRuntimeCopy(session.runtimeCopyRoot); err != nil {
					return removed, err
				}
			}
			delete(p.sessions, id)
			removed++
		}
	}
	return removed, nil
}

func (p *Provider) Close() error {
	if p.runner != nil {
		return p.runner.Close()
	}
	return nil
}

// Capabilities is a short-lived diagnostic snapshot. File hashes cannot
// capture every firewall/ACL change, so Create and Exec always rerun probes
// at the strong-isolation boundary rather than authorize from a cached pass.
const probeSnapshotMaxAge = time.Minute

func probeSnapshotFresh(checkedAt, now time.Time) bool {
	return !checkedAt.IsZero() && !now.Before(checkedAt) && now.Sub(checkedAt) < probeSnapshotMaxAge
}

func (p *Provider) probe(ctx context.Context, pythonPath string) ProbeReport {
	p.probeMu.Lock()
	defer p.probeMu.Unlock()
	p.probePython = pythonPath
	key, err := ProbeEnvironmentKey(pythonPath, "", p.probeConfiguration())
	if err != nil {
		report := EvaluateProbe(nil)
		report.Reason = "runtime fingerprint unavailable: " + err.Error()
		p.probeReport = &report
		p.probeCheckedAt = time.Time{}
		if p.runner != nil {
			p.runner.SetProbe(nil)
		}
		return report
	}
	run := p.probeRun
	if run == nil {
		run = runProbe
	}
	report := run(ctx, pythonPath, filepath.Join(p.cfg.DataDir, "probe"))
	report.EnvironmentKey = key
	finalKey, err := ProbeEnvironmentKey(pythonPath, "", p.probeConfiguration())
	if err != nil || finalKey != key {
		report.AppContainerOK = false
		report.Reason = "runtime or isolation configuration changed during the probe; revalidation required"
	}
	stored := cloneProbeReport(report)
	p.probeReport = &stored
	p.probeCheckedAt = time.Now()
	if p.runner != nil {
		runnerReport := cloneProbeReport(report)
		p.runner.SetProbe(&runnerReport)
	}
	return cloneProbeReport(report)
}

func cloneProbeReport(report ProbeReport) ProbeReport {
	report.Cases = append([]ProbeCase(nil), report.Cases...)
	return report
}

func (p *Provider) probeConfiguration() string {
	return p.cfg.Runner + "|" + os.Getenv("SANDBOX_PROBE_NETWORK_ADDRESS")
}

func (p *Provider) session(id string) (*nativeSession, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	session, ok := p.sessions[id]
	if !ok {
		return nil, fmt.Errorf("native sandbox %q not found", id)
	}
	copy := *session
	return &copy, nil
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
