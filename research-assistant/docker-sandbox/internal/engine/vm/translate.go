package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"docker-sandbox/internal/engine"
)

// ToWSLPath converts a Windows absolute path to its WSL /mnt equivalent.
func ToWSLPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty path")
	}
	if strings.HasPrefix(path, "\\") {
		return "", fmt.Errorf("UNC paths are not supported by the WSL provider: %s", path)
	}
	if len(path) >= 2 && path[1] == ':' {
		drive := strings.ToLower(string(path[0]))
		rest := strings.ReplaceAll(path[2:], "\\", "/")
		if !strings.HasPrefix(rest, "/") {
			rest = "/" + rest
		}
		return "/mnt/" + drive + rest, nil
	}
	if strings.HasPrefix(path, "/") {
		return path, nil
	}
	return "", fmt.Errorf("path is not absolute: %s", path)
}

// FromWSLPath converts a /mnt/<drive>/... path back to a Windows path.
func FromWSLPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if len(path) >= 7 && strings.HasPrefix(path, "/mnt/") && path[6] == '/' {
		drive := strings.ToUpper(path[5:6])
		rest := strings.TrimPrefix(path[7:], "/")
		return drive + ":\\" + strings.ReplaceAll(rest, "/", "\\"), nil
	}
	return "", fmt.Errorf("not a /mnt drive path: %s", path)
}

// BuildWSLArgs builds the argv for wsl.exe. Empty distro omits -d; empty cwd
// omits --cd.
func BuildWSLArgs(distro, cwd string, argv []string) []string {
	args := make([]string, 0, len(argv)+6)
	if distro = strings.TrimSpace(distro); distro != "" {
		args = append(args, "-d", distro)
	}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		args = append(args, "--cd", cwd)
	}
	args = append(args, "--")
	args = append(args, argv...)
	return args
}

// BuildWSLArgsWithEnv inserts an env(1) invocation after the -- separator.
func BuildWSLArgsWithEnv(distro, cwd string, env map[string]string, argv []string) []string {
	args := BuildWSLArgs(distro, cwd, nil)
	if len(env) > 0 {
		args = append(args, "env")
		keys := make([]string, 0, len(env))
		for key := range env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			args = append(args, key+"="+env[key])
		}
	}
	return append(args, argv...)
}

// TranslateVMCommand maps logical /workspace and python names to the WSL
// runtime.
func TranslateVMCommand(workspace string, command engine.Command) ([]string, string, error) {
	if len(command.Argv) == 0 {
		return nil, "", fmt.Errorf("vm command argv is empty")
	}
	argv := append([]string(nil), command.Argv...)
	switch strings.ToLower(argv[0]) {
	case "python", "python3":
		argv[0] = "python3"
	}
	workspaceWSL, err := ToWSLPath(workspace)
	if err != nil {
		return nil, "", err
	}
	cwd := workspaceWSL
	logical := strings.TrimSpace(command.Cwd)
	switch {
	case logical == "" || logical == "/workspace":
		cwd = workspaceWSL
	case strings.HasPrefix(logical, "/workspace/"):
		cwd = workspaceWSL + strings.TrimPrefix(logical, "/workspace")
	case strings.HasPrefix(logical, "/"):
		cwd = logical
	default:
		cwd, err = ToWSLPath(logical)
		if err != nil {
			return nil, "", err
		}
	}
	return argv, cwd, nil
}

// DefaultConfig derives the per-user workspace root for the VM provider.
func DefaultConfig() Config {
	dataDir := strings.TrimSpace(os.Getenv("APP_DATA_DIR"))
	if dataDir == "" {
		dataDir = filepath.Join(os.TempDir(), "research-assistant-vm")
	}
	workspaceRoot := ""
	if roots := filepath.SplitList(strings.TrimSpace(os.Getenv("SANDBOX_WORKSPACE_ROOTS"))); len(roots) > 0 {
		workspaceRoot = strings.TrimSpace(roots[0])
	}
	if workspaceRoot == "" {
		workspaceRoot = filepath.Join(dataDir, "workspaces")
	}
	return Config{DataDir: dataDir, WorkspaceRoot: workspaceRoot}
}
