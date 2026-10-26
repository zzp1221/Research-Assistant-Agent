package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Capabilities describes the Docker host visible to the sandbox executor.
type Capabilities struct {
	DockerAvailable     bool     `json:"docker_available"`
	ServerVersion       string   `json:"server_version,omitempty"`
	GPURequest          string   `json:"gpu_request,omitempty"`
	GPURuntimeAvailable bool     `json:"gpu_runtime_available"`
	CPUCount            int      `json:"cpu_count"`
	MemoryTotalBytes    int64    `json:"memory_total_bytes"`
	DiskFreeBytes       int64    `json:"disk_free_bytes"`
	ImageAllowlist      []string `json:"image_allowlist"`
	NetworkMode         string   `json:"network_mode"`
	SandboxCount        int      `json:"sandbox_count"`
	ReaperTTLMinutes    int      `json:"reaper_ttl_minutes"`
}

// ImageInfo is returned by EnsureImage.
type ImageInfo struct {
	Image     string `json:"image"`
	Digest    string `json:"digest"`
	SizeBytes int64  `json:"size_bytes"`
	Pulled    bool   `json:"pulled"`
}

func (e *NativeDockerEngine) Capabilities(ctx context.Context) Capabilities {
	health := e.Health(ctx)
	caps := Capabilities{
		DockerAvailable:     health.Available,
		ServerVersion:       health.ServerVersion,
		GPURequest:          health.GPURequest,
		GPURuntimeAvailable: health.GPURuntimeAvailable,
		CPUCount:            runtime.NumCPU(),
		MemoryTotalBytes:    readMemoryTotal(),
		DiskFreeBytes:       diskFreeBytes("/tmp"),
		ImageAllowlist:      imageAllowlist(),
		NetworkMode:         envOrDefault("SANDBOX_NETWORK_MODE", "bridge"),
		SandboxCount:        countSandboxContainers(ctx),
		ReaperTTLMinutes:    reaperTTLMinutes(),
	}
	return caps
}

func reaperTTLMinutes() int {
	const fallback = 120
	raw := strings.TrimSpace(os.Getenv("SANDBOX_TTL_MINUTES"))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// countSandboxContainers reports how many sandbox containers are currently
// running so operators can watch for leaks.
func countSandboxContainers(ctx context.Context) int {
	output, err := exec.CommandContext(ctx, dockerCommand(), "ps", "-q",
		"--filter", "label="+sandboxLabelKey+"="+sandboxLabelValue).Output()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}

func (e *NativeDockerEngine) EnsureImage(ctx context.Context, image string) (*ImageInfo, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return nil, fmt.Errorf("image is empty")
	}
	if !sandboxImageAllowed(image) {
		return nil, fmt.Errorf("image_not_allowed: %s is not in SANDBOX_IMAGE_ALLOWLIST", image)
	}
	resolvedImage := rewriteImageWithRegistryMirror(image)
	info, err := inspectImage(ctx, resolvedImage)
	if err == nil {
		info.Pulled = false
		info.Image = image
		return info, nil
	}
	pullCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	output, pullErr := exec.CommandContext(pullCtx, dockerCommand(), "pull", resolvedImage).CombinedOutput()
	if pullErr != nil {
		return nil, fmt.Errorf("image_pull_failed: %v: %s", pullErr, truncateOutput(string(output), 800))
	}
	info, err = inspectImage(ctx, resolvedImage)
	if err != nil {
		return nil, fmt.Errorf("image_pull_failed: pull completed but inspect failed: %w", err)
	}
	info.Pulled = true
	// Report the logical image name the caller asked for while keeping the
	// resolved (mirrored) image available for diagnostics.
	info.Image = image
	return info, nil
}

func inspectImage(ctx context.Context, image string) (*ImageInfo, error) {
	output, err := exec.CommandContext(ctx, dockerCommand(), "image", "inspect", "--format", "{{.Id}}|{{.Size}}", image).Output()
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(strings.TrimSpace(string(output)), "|", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("unexpected image inspect output: %q", strings.TrimSpace(string(output)))
	}
	size, _ := strconv.ParseInt(parts[1], 10, 64)
	return &ImageInfo{Image: image, Digest: parts[0], SizeBytes: size}, nil
}

func imageAllowlist() []string {
	raw := strings.TrimSpace(os.Getenv("SANDBOX_IMAGE_ALLOWLIST"))
	if raw == "" {
		return []string{}
	}
	out := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func readMemoryTotal() int64 {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, _ := strconv.ParseInt(fields[1], 10, 64)
		return kb * 1024
	}
	return 0
}

func truncateOutput(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}
