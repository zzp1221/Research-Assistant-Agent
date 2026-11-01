package engine

import (
	"context"
	"runtime"
	"strings"
	"time"
)

// DockerProvider adapts the existing NativeDockerEngine to the Provider
// contract. The REST contract and container behavior stay unchanged.
type DockerProvider struct {
	engine *NativeDockerEngine
}

func NewDockerProvider() *DockerProvider {
	return &DockerProvider{engine: NewNativeDockerEngine()}
}

func (p *DockerProvider) Name() string { return "docker" }

func (p *DockerProvider) Capabilities(ctx context.Context) (ProviderCapabilities, error) {
	caps := p.engine.Capabilities(ctx)
	notes := make([]string, 0)
	if !caps.DockerAvailable {
		notes = append(notes, "docker daemon is unavailable")
	}
	return ProviderCapabilities{
		Provider:        "docker",
		Isolation:       "container",
		Images:          true,
		NetworkControl:  true,
		NetworkEnforced: "os",
		GPU:             strings.TrimSpace(caps.GPURequest) != "",
		GPUEnforced:     caps.GPURuntimeAvailable,
		OS:              runtime.GOOS,
		Notes:           notes,
	}, nil
}

func (p *DockerProvider) Create(ctx context.Context, spec CreateSpec) (Session, error) {
	id, err := p.engine.Create(ctx, CreateOptions{
		Image:       spec.Image,
		MountPath:   spec.MountPath,
		Labels:      spec.Labels,
		NetworkMode: spec.NetworkMode,
		Limits:      spec.Limits,
	})
	if err != nil {
		return Session{}, err
	}
	return Session{
		ID:          "dk-" + id,
		Provider:    "docker",
		Isolation:   "container",
		Workspace:   spec.MountPath,
		ContainerID: id,
		CreatedAt:   time.Now(),
	}, nil
}

func (p *DockerProvider) Delete(ctx context.Context, id string) error {
	return p.engine.Delete(ctx, strings.TrimPrefix(id, "dk-"))
}

func (p *DockerProvider) Exec(ctx context.Context, id string, cmd Command, emit func(Event) error) (*Result, error) {
	started := time.Now()
	response, err := p.engine.ExecuteCommandStreamSpec(ctx, strings.TrimPrefix(id, "dk-"), cmd, func(event ExecutionStreamEvent) error {
		if emit == nil {
			return nil
		}
		if event.Type == "chunk" {
			return emit(Event{Type: "chunk", Stream: event.Stream, Message: event.Message})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	result := &Result{
		Stdout:     response.Stdout,
		Stderr:     response.Stderr,
		ExitCode:   response.ExitCode,
		Truncated:  response.Truncated,
		DurationMS: time.Since(started).Milliseconds(),
	}
	return result, nil
}

func (p *DockerProvider) InstallPackages(ctx context.Context, id string, packages []string) (*Result, error) {
	argv := append([]string{"python", "-m", "pip", "install"}, packages...)
	return p.Exec(ctx, id, Command{Argv: argv, Cwd: "/workspace"}, nil)
}

func (p *DockerProvider) SetNetwork(ctx context.Context, id string, on bool) error {
	mode := NetworkNone
	if on {
		mode = NetworkBridge
	}
	return p.engine.SetNetwork(ctx, strings.TrimPrefix(id, "dk-"), mode)
}

func (p *DockerProvider) Usage(ctx context.Context, id string) (Usage, error) {
	containerID := strings.TrimPrefix(id, "dk-")
	usage := Usage{}
	if rx, tx, err := p.engine.NetworkBytes(ctx, containerID); err == nil {
		usage.NetworkRXBytes = rx
		usage.NetworkTXBytes = tx
	}
	if disk, err := p.engine.DiskUsageMB(ctx, containerID); err == nil {
		usage.DiskUsedMB = disk
	}
	return usage, nil
}

func (p *DockerProvider) Reap(context.Context, time.Duration) (int, error) { return 0, nil }
func (p *DockerProvider) Close() error                                     { return nil }
