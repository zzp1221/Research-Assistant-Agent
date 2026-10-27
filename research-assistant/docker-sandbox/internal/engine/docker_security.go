package engine

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func secureDockerRunArgs(image string, labels map[string]string, options ...CreateOptions) ([]string, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return nil, fmt.Errorf("sandbox image is empty")
	}
	if !sandboxImageAllowed(image) {
		return nil, fmt.Errorf("sandbox image %q is not in SANDBOX_IMAGE_ALLOWLIST", image)
	}
	var opts CreateOptions
	if len(options) > 0 {
		opts = options[0]
	}
	networkMode, err := EffectiveNetworkMode(opts.NetworkMode)
	if err != nil {
		return nil, err
	}
	networkArg := networkMode
	if networkMode == NetworkNone {
		// Isolation uses an internal bridge (no gateway) because Docker cannot
		// attach the default bridge to a container created with --network none.
		networkArg = offlineNetworkName()
	}

	args := []string{
		"run", "-d", "--rm",
		"--label", sandboxLabelKey + "=" + sandboxLabelValue,
		"--cpus", envOrDefault("SANDBOX_CPU_LIMIT", "2"),
		"--memory", envOrDefault("SANDBOX_MEMORY_LIMIT", "4g"),
		"--pids-limit", envOrDefault("SANDBOX_PIDS_LIMIT", "256"),
		"--security-opt", "no-new-privileges:true",
		"--cap-drop", "ALL",
		"--network", networkArg,
		"--workdir", "/workspace",
	}
	if runtimeName := strings.TrimSpace(opts.Runtime); runtimeName != "" {
		allowed := strings.TrimSpace(os.Getenv("SANDBOX_ALLOWED_RUNTIMES"))
		if allowed != "" && !containsCommaSeparated(allowed, runtimeName) {
			return nil, fmt.Errorf("sandbox runtime %q is not in SANDBOX_ALLOWED_RUNTIMES", runtimeName)
		}
		args = append(args, "--runtime", runtimeName)
	}
	if opts.Limits.MaxDiskGB > 0 && strings.TrimSpace(opts.MountPath) == "" {
		// A tmpfs workspace gets a hard size limit enforced by the kernel. Host
		// bind mounts cannot be quota'd this way and are measured after each
		// command instead.
		args = append(args, "--tmpfs", fmt.Sprintf("/workspace:rw,exec,size=%dg", int(math.Ceil(opts.Limits.MaxDiskGB))))
	}
	labelKeys := make([]string, 0, len(labels))
	for key := range labels {
		if key == sandboxLabelKey || strings.TrimSpace(labels[key]) == "" {
			continue
		}
		labelKeys = append(labelKeys, key)
	}
	sort.Strings(labelKeys)
	for _, key := range labelKeys {
		args = append(args, "--label", key+"="+labels[key])
	}
	user := strings.TrimSpace(os.Getenv("SANDBOX_CONTAINER_USER"))
	readOnlyRoot := envBool("SANDBOX_READ_ONLY_ROOT", false)
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SANDBOX_SECURITY_PROFILE")), "strict") {
		// Strict profile: unprivileged user, immutable rootfs and tmpfs scratch
		// space. It is opt-in because pip installs into /root and some runtime
		// images assume uid 0.
		readOnlyRoot = true
		if user == "" {
			user = "65534:65534"
		}
	}
	if user != "" {
		args = append(args, "--user", user)
	}
	if readOnlyRoot {
		args = append(args, "--read-only",
			"--tmpfs", "/tmp:rw,noexec,nosuid,size=512m",
			"--tmpfs", "/root/.cache:rw,size=1g")
	}
	return args, nil
}

func containsCommaSeparated(raw, want string) bool {
	for _, item := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(item), want) {
			return true
		}
	}
	return false
}

func sandboxImageAllowed(image string) bool {
	raw := strings.TrimSpace(os.Getenv("SANDBOX_IMAGE_ALLOWLIST"))
	if raw == "" {
		return true
	}
	for _, prefix := range strings.Split(raw, ",") {
		prefix = strings.TrimSpace(prefix)
		if prefix == "" {
			continue
		}
		if strings.HasPrefix(image, prefix) {
			return true
		}
		// A registry mirror prefixes the logical image (for example
		// docker.m.daocloud.io/library/python:3.11-bullseye); the allowlist
		// entry still matches the logical suffix.
		if strings.HasSuffix(image, "/"+prefix) {
			return true
		}
	}
	return false
}

func allowedSandboxMountRoots() []string {
	raw := strings.TrimSpace(os.Getenv("SANDBOX_WORKSPACE_ROOTS"))
	if raw == "" {
		raw = os.TempDir()
	}
	roots := make([]string, 0)
	for _, root := range filepath.SplitList(raw) {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if absolute, err := filepath.Abs(root); err == nil {
			absolute = filepath.Clean(absolute)
			if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
				absolute = filepath.Clean(resolved)
			}
			roots = append(roots, absolute)
		}
	}
	return roots
}

func mountPathAllowed(path string) bool {
	cleanPath := filepath.Clean(path)
	for _, root := range allowedSandboxMountRoots() {
		relative, err := filepath.Rel(root, cleanPath)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func normalizeAndAuthorizeMountPath(mountPath string) (string, error) {
	absPath, err := filepath.Abs(mountPath)
	if err != nil {
		return "", fmt.Errorf("resolve mount path failed: %w", err)
	}
	absPath = filepath.Clean(absPath)
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("inspect mount path: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("mount path %q is not a directory", absPath)
	}
	resolvedPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve mount path symlinks: %w", err)
	}
	absPath = filepath.Clean(resolvedPath)
	if !mountPathAllowed(absPath) {
		return "", fmt.Errorf("mount path %q is outside SANDBOX_WORKSPACE_ROOTS", absPath)
	}
	if runtime.GOOS == "windows" {
		absPath = strings.ReplaceAll(absPath, "\\", "/")
	}
	return absPath, nil
}
