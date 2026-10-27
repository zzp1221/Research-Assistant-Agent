package engine

import (
	"fmt"
	"os"
	"strings"
)

// NetworkNone isolates a sandbox from every network, including DNS.
const NetworkNone = "none"

// NetworkBridge gives a sandbox outbound connectivity. The backend enables it
// only around an explicitly approved install/download step.
const NetworkBridge = "bridge"

// offlineNetworkName is the isolated bridge network used by default. Docker
// refuses to attach a second network to a container created with --network
// none, so isolation is implemented with an internal bridge that has no
// gateway/NAT. Connecting the default bridge later grants egress and
// disconnecting it restores isolation.
func offlineNetworkName() string {
	name := strings.TrimSpace(os.Getenv("SANDBOX_OFFLINE_NETWORK"))
	if name == "" {
		name = "research-assistant-offline"
	}
	return name
}

// Limits mirror the RuntimePolicy hard limits that the backend forwards to the
// sandbox service. Zero means "use the deployment default / unlimited".
type Limits struct {
	MaxImageBytes    int64   `json:"max_image_bytes,omitempty"`
	MaxDownloadBytes int64   `json:"max_download_bytes,omitempty"`
	MaxDiskGB        float64 `json:"max_disk_gb,omitempty"`
	MaxRuntimeSec    int     `json:"max_runtime_sec,omitempty"`
}

// CreateOptions is the complete sandbox creation request.
type CreateOptions struct {
	Image       string
	MountPath   string
	Labels      map[string]string
	NetworkMode string
	Runtime     string
	Limits      Limits
}

// EffectiveNetworkMode resolves the requested mode against the deployment
// policy. The safe default is "none": a sandbox only reaches the network when
// the caller explicitly asks for it (after approval) and policy allows it.
func EffectiveNetworkMode(requested string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(requested))
	if mode == "" {
		mode = strings.ToLower(strings.TrimSpace(envOrDefault("SANDBOX_NETWORK_MODE", NetworkNone)))
	}
	switch mode {
	case NetworkNone, NetworkBridge:
	default:
		return "", fmt.Errorf("unsupported network mode %q (expected none or bridge)", requested)
	}
	if mode == NetworkBridge && !envBool("SANDBOX_ALLOW_NETWORK", true) {
		return "", fmt.Errorf("sandbox network access is disabled by policy (SANDBOX_ALLOW_NETWORK=false)")
	}
	return mode, nil
}

func envBool(key string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
