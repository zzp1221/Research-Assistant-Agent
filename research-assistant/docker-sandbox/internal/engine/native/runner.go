package native

import (
	"context"
	"errors"
	"time"

	"docker-sandbox/internal/engine"
)

// ErrUnsupportedPlatform is returned when native sandboxing is unavailable.
var ErrUnsupportedPlatform = errors.New("native sandbox is only supported on windows")

type runSpec struct {
	Argv        []string
	Cwd         string
	Env         map[string]string
	Stdin       string
	MaxOutputKB int
	Timeout     time.Duration
	Limits      engine.Limits
	GPURequired bool
	NetworkOn   bool
	Emit        func(engine.Event) error
}

type commandRunner interface {
	Run(ctx context.Context, spec runSpec) (*engine.Result, error)
	SetProbe(report *ProbeReport)
	Close() error
}
