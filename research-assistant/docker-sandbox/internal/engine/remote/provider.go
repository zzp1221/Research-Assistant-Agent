// Package remote adapts the existing OpenSandbox client to the Provider
// contract for the P4 remote sandbox tier.
package remote

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"docker-sandbox/internal/engine"
)

// Provider wraps OpenSandboxEngine.
type Provider struct {
	engine *engine.OpenSandboxEngine
}

// NewProvider builds a remote provider. An empty URL is an explicit error so a
// misconfigured desktop never silently falls back.
func NewProvider(baseURL string) (*Provider, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("remote provider: OpenSandbox URL is required")
	}
	return &Provider{engine: engine.NewOpenSandboxEngine(baseURL)}, nil
}

func (p *Provider) Name() string { return "remote" }

func (p *Provider) Capabilities(context.Context) (engine.ProviderCapabilities, error) {
	return engine.ProviderCapabilities{
		Provider:        "remote",
		Isolation:       "remote",
		Images:          true,
		NetworkControl:  true,
		NetworkEnforced: "os",
		GPU:             false,
		GPUEnforced:     false,
		OS:              runtime.GOOS,
		Notes:           []string{"OpenSandbox remote provider; workspace is remote"},
	}, nil
}

func (p *Provider) Create(ctx context.Context, spec engine.CreateSpec) (engine.Session, error) {
	id, err := p.engine.Create(ctx, spec.Image, spec.MountPath)
	if err != nil {
		return engine.Session{}, err
	}
	return engine.Session{ID: "os-" + id, Provider: "remote", Isolation: "remote", Workspace: spec.MountPath, ContainerID: id, CreatedAt: time.Now()}, nil
}

func (p *Provider) Delete(ctx context.Context, id string) error {
	return p.engine.Delete(ctx, strings.TrimPrefix(id, "os-"))
}

func (p *Provider) Exec(ctx context.Context, id string, cmd engine.Command, emit func(engine.Event) error) (*engine.Result, error) {
	if cmd.Shell {
		return nil, fmt.Errorf("remote provider does not support POSIX shell commands; use argv")
	}
	if len(cmd.Argv) == 0 {
		return nil, fmt.Errorf("remote command argv is empty")
	}
	response, err := p.engine.ExecuteCommand(ctx, strings.TrimPrefix(id, "os-"), cmd.Argv)
	if err != nil {
		return nil, err
	}
	if emit != nil && response.Stdout != "" {
		_ = emit(engine.Event{Type: "chunk", Stream: "stdout", Message: response.Stdout})
	}
	return &engine.Result{Stdout: response.Stdout, Stderr: response.Stderr, ExitCode: response.ExitCode, Truncated: response.Truncated}, nil
}

func (p *Provider) InstallPackages(ctx context.Context, id string, packages []string) (*engine.Result, error) {
	argv := append([]string{"python", "-m", "pip", "install"}, packages...)
	return p.Exec(ctx, id, engine.Command{Argv: argv}, nil)
}

func (p *Provider) SetNetwork(context.Context, string, bool) error {
	return fmt.Errorf("remote provider network switching is managed by the remote service")
}

func (p *Provider) Usage(context.Context, string) (engine.Usage, error) {
	return engine.Usage{}, nil
}

func (p *Provider) Reap(context.Context, time.Duration) (int, error) { return 0, nil }

func (p *Provider) Close() error { return nil }
