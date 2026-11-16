package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
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
	NetworkAllowed      bool     `json:"network_allowed"`
	SecurityProfile     string   `json:"security_profile"`
	ContainerUser       string   `json:"container_user,omitempty"`
	SandboxCount        int      `json:"sandbox_count"`
	ReaperTTLMinutes    int      `json:"reaper_ttl_minutes"`
}

// ImageInfo is returned by EnsureImage.
type ImageInfo struct {
	Image         string `json:"image"`
	ResolvedImage string `json:"resolved_image"`
	Digest        string `json:"digest"`
	DigestSource  string `json:"digest_source"`
	RepoDigest    string `json:"repo_digest,omitempty"`
	ImageID       string `json:"image_id"`
	Platform      string `json:"platform,omitempty"`
	SizeBytes     int64  `json:"size_bytes"`
	Pulled        bool   `json:"pulled"`
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
		NetworkMode:         envOrDefault("SANDBOX_NETWORK_MODE", NetworkNone),
		NetworkAllowed:      envBool("SANDBOX_ALLOW_NETWORK", true),
		SecurityProfile:     envOrDefault("SANDBOX_SECURITY_PROFILE", "standard"),
		ContainerUser:       strings.TrimSpace(os.Getenv("SANDBOX_CONTAINER_USER")),
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
	authorized, err := authorizeDockerImage(image)
	if err != nil {
		return nil, err
	}
	key := authorized.logical.named.String() + "|" + authorized.resolved.named.String()
	// Serialize first resolution so concurrent requests cannot prepare two
	// different contents for one mutable tag in the same service lifetime.
	e.imageMu.Lock()
	defer e.imageMu.Unlock()
	if pinned, ok := e.images[key]; ok {
		current, inspectErr := e.inspectImage(ctx, pinned.ImageID)
		if inspectErr != nil || current.ImageID != pinned.ImageID {
			return nil, fmt.Errorf("image_pin_unavailable: previously prepared image %s is unavailable; prepare again after restarting the sandbox service", pinned.ImageID)
		}
		pinned.Pulled = false
		return &pinned, nil
	}
	info, inspectErr := e.inspectImage(ctx, authorized.resolved.named.String())
	pulled := false
	if inspectErr != nil {
		pullCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		output, pullErr := e.runDockerOutput(pullCtx, "pull", "--", authorized.resolved.named.String())
		if pullErr != nil {
			return nil, fmt.Errorf("image_pull_failed: %v: %s", pullErr, truncateOutput(string(output), 800))
		}
		info, err = e.inspectImage(ctx, authorized.resolved.named.String())
		if err != nil {
			return nil, fmt.Errorf("image_pull_failed: pull completed but inspect failed: %w", err)
		}
		pulled = true
	}
	if err := verifyInspectedImage(info, authorized); err != nil {
		return nil, err
	}
	if authorized.resolved.tag != "" && authorized.resolved.digest != "" {
		// Docker's tag@digest syntax selects the digest; it does not prove the
		// named tag still points at that content. Require the independently
		// prepared local tag to resolve to the same immutable config ID.
		tagRef := authorized.resolved.repository + ":" + authorized.resolved.tag
		tagInfo, tagErr := e.inspectImage(ctx, tagRef)
		if tagErr != nil || tagInfo.ImageID != info.ImageID {
			return nil, fmt.Errorf("image_digest_mismatch: tag %s is absent or differs from its approved digest; prepare the matching tag before using tag@digest", tagRef)
		}
	}
	info.Image = authorized.logical.named.String()
	info.ResolvedImage = authorized.resolved.named.String()
	info.Pulled = pulled
	if e.images == nil {
		e.images = make(map[string]ImageInfo)
	}
	e.images[key] = *info
	return info, nil
}

func (e *NativeDockerEngine) inspectImage(ctx context.Context, image string) (*ImageInfo, error) {
	args := []string{"image", "inspect", "--format", "{{json .}}", "--", image}
	var output []byte
	var err error
	if e.commandOutput != nil {
		output, err = e.commandOutput(ctx, args...)
	} else {
		output, err = exec.CommandContext(ctx, dockerCommand(), args...).Output()
	}
	if err != nil {
		return nil, err
	}
	var inspected struct {
		ID           string   `json:"Id"`
		Size         int64    `json:"Size"`
		RepoDigests  []string `json:"RepoDigests"`
		OS           string   `json:"Os"`
		Architecture string   `json:"Architecture"`
		Variant      string   `json:"Variant"`
	}
	if err := json.Unmarshal(output, &inspected); err != nil {
		return nil, fmt.Errorf("invalid image inspect metadata: %w", err)
	}
	if _, err := parseImageReference("local.invalid/image@" + inspected.ID); err != nil || inspected.Size < 0 {
		return nil, fmt.Errorf("invalid image inspect ID or size")
	}
	info := &ImageInfo{Image: image, ImageID: inspected.ID, Digest: inspected.ID, DigestSource: "image_config", SizeBytes: inspected.Size}
	if inspected.OS != "" && inspected.Architecture != "" {
		info.Platform = inspected.OS + "/" + inspected.Architecture
		if inspected.Variant != "" {
			info.Platform += "/" + inspected.Variant
		}
	}
	// RepoDigests can contain aliases. Only select a digest belonging to the
	// requested repository, rather than borrowing a hash from another image.
	requested, requestErr := parseImageReference(image)
	if requestErr == nil {
		sort.Strings(inspected.RepoDigests)
		for _, raw := range inspected.RepoDigests {
			ref, err := parseImageReference(raw)
			if err != nil || ref.repository != requested.repository || ref.digest == "" {
				continue
			}
			if requested.digest != "" && ref.digest != requested.digest {
				continue
			}
			info.RepoDigest = ref.named.String()
			info.Digest = ref.digest.String()
			info.DigestSource = "repository_manifest"
			break
		}
	}
	return info, nil
}

func imageAllowlist() []string {
	policy, err := loadImagePolicy()
	if err != nil {
		return []string{}
	}
	out := make([]string, 0, len(policy.rules))
	for _, rule := range policy.rules {
		out = append(out, rule.named.String())
	}
	return out
}

func verifyInspectedImage(info *ImageInfo, authorized authorizedImage) error {
	if authorized.resolved.digest == "" {
		return nil
	}
	ref, err := parseImageReference(info.RepoDigest)
	if err != nil || ref.repository != authorized.resolved.repository || ref.digest != authorized.resolved.digest {
		return fmt.Errorf("image_digest_mismatch: image metadata does not establish the authorized repository digest %s", authorized.resolved.named.String())
	}
	return nil
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
