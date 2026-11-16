package native

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"docker-sandbox/internal/engine"
)

// ErrUnsupportedPOSIXShell is returned when a legacy shell command reaches a
// native provider. The scheduler must mark the node blocked instead of
// pretending the command succeeded.
var ErrUnsupportedPOSIXShell = errors.New("unsupported_posix_shell")

// TranslateCwd maps the logical /workspace root to a real host directory.
func TranslateCwd(workspaceRoot, logical string) (string, error) {
	logical = strings.TrimSpace(logical)
	if logical == "" || logical == "/workspace" {
		return workspaceRoot, nil
	}
	if strings.ContainsAny(logical, "\\:\x00") {
		return "", fmt.Errorf("working directory must be a logical workspace path")
	}
	for _, component := range strings.Split(logical, "/") {
		if component == ".." {
			return "", fmt.Errorf("working directory must not traverse outside the workspace")
		}
	}
	if strings.HasPrefix(logical, "/workspace/") {
		rel := strings.TrimPrefix(logical, "/workspace/")
		return containedEnvironmentPath(workspaceRoot, filepath.Join(workspaceRoot, filepath.FromSlash(rel)))
	}
	if filepath.IsAbs(logical) || strings.HasPrefix(logical, "/") {
		return "", fmt.Errorf("path %q is outside the workspace", logical)
	}
	return containedEnvironmentPath(workspaceRoot, filepath.Join(workspaceRoot, filepath.FromSlash(logical)))
}

// TranslateArgv maps logical python interpreters and /workspace paths to the
// host venv and workspace.
func TranslateArgv(workspaceRoot, venvPython string, argv []string) []string {
	out := make([]string, len(argv))
	for i, arg := range argv {
		switch {
		case i == 0 && (arg == "python" || arg == "python3"):
			out[i] = venvPython
		case arg == "/workspace":
			out[i] = workspaceRoot
		case strings.HasPrefix(arg, "/workspace/"):
			out[i] = filepath.Join(workspaceRoot, filepath.FromSlash(strings.TrimPrefix(arg, "/workspace/")))
		default:
			out[i] = arg
		}
	}
	return out
}

// TranslateCommand validates and translates one command for a native run.
func TranslateCommand(workspaceRoot, venvPython string, command engine.Command) (engine.Command, error) {
	if command.Shell {
		return engine.Command{}, ErrUnsupportedPOSIXShell
	}
	env, err := validateTaskEnvironment(command.Env)
	if err != nil {
		return engine.Command{}, err
	}
	if pythonPath, ok := env["PYTHONPATH"]; ok {
		env["PYTHONPATH"], err = translatePythonPath(workspaceRoot, pythonPath)
		if err != nil {
			return engine.Command{}, err
		}
	}
	cwd, err := TranslateCwd(workspaceRoot, command.Cwd)
	if err != nil {
		return engine.Command{}, err
	}
	out := command
	out.Env = env
	out.Cwd = cwd
	out.Argv = TranslateArgv(workspaceRoot, venvPython, command.Argv)
	if len(out.Env) > 0 {
		for key, value := range out.Env {
			if key == "PYTHONPATH" {
				continue
			}
			if value == "/workspace" {
				value = workspaceRoot
			} else if strings.HasPrefix(value, "/workspace/") {
				value = filepath.Join(workspaceRoot, filepath.FromSlash(strings.TrimPrefix(value, "/workspace/")))
			}
			env[key] = value
		}
	}
	return out, nil
}
