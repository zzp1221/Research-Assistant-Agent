package engine

import (
	"context"
	"time"
)

// Provider is the sandbox execution contract shared by docker, native and
// future vm/remote backends. It intentionally uses logical paths and logical
// interpreter names; each implementation translates them.
type Provider interface {
	Name() string
	Capabilities(ctx context.Context) (ProviderCapabilities, error)
	Create(ctx context.Context, spec CreateSpec) (Session, error)
	Delete(ctx context.Context, id string) error
	Exec(ctx context.Context, id string, cmd Command, emit func(Event) error) (*Result, error)
	InstallPackages(ctx context.Context, id string, packages []string) (*Result, error)
	SetNetwork(ctx context.Context, id string, on bool) error
	Usage(ctx context.Context, id string) (Usage, error)
	Reap(ctx context.Context, ttl time.Duration) (int, error)
	Close() error
}

// ProbeCase is one AppContainer compatibility probe result.
type ProbeCase struct {
	Name     string `json:"name"`
	Security bool   `json:"security"`
	Passed   bool   `json:"passed"`
	Detail   string `json:"detail,omitempty"`
}

// ProbeReport is the persisted AppContainer go/no-go report.
type ProbeReport struct {
	Cases          []ProbeCase `json:"cases"`
	AppContainerOK bool        `json:"appcontainer_ok"`
	Reason         string      `json:"reason,omitempty"`
}

// ProviderCapabilities is the honest capability matrix shown in the UI.
type ProviderCapabilities struct {
	Provider        string       `json:"provider"`
	Isolation       string       `json:"isolation"`
	Images          bool         `json:"images"`
	NetworkControl  bool         `json:"network_control"`
	NetworkEnforced string       `json:"network_enforced"` // os | firewall | none
	GPU             bool         `json:"gpu"`
	GPUEnforced     bool         `json:"gpu_enforced"`
	PythonVersions  []string     `json:"python_versions,omitempty"`
	OS              string       `json:"os"`
	Notes           []string     `json:"notes,omitempty"`
	Probe           *ProbeReport `json:"probe,omitempty"`
}

// CreateSpec describes one sandbox request.
type CreateSpec struct {
	Image       string
	MountPath   string
	Labels      map[string]string
	Limits      Limits
	NetworkMode string
	PythonVer   string
	EnvHash     string
}

// Session identifies a live sandbox.
type Session struct {
	ID          string
	Provider    string
	Isolation   string
	Workspace   string
	VenvPath    string
	ContainerID string
	CreatedAt   time.Time
}

// Event is one streamed chunk or the final result.
type Event struct {
	Type    string // chunk | final
	Stream  string // stdout | stderr
	Message string
	Result  *Result
}

// Result is the outcome of one execution.
type Result struct {
	Stdout     string
	Stderr     string
	ExitCode   int
	DurationMS int64
	Truncated  bool
}

// Usage reports measured resource usage.
type Usage struct {
	NetworkRXBytes int64   `json:"network_rx_bytes,omitempty"`
	NetworkTXBytes int64   `json:"network_tx_bytes,omitempty"`
	DiskUsedMB     float64 `json:"disk_used_mb,omitempty"`
	ProcessCount   int     `json:"process_count,omitempty"`
	RuntimeHash    string  `json:"runtime_hash,omitempty"`
}
