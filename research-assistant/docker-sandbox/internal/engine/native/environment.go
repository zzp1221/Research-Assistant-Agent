package native

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

const maxEnvironmentUnits = 30000

var taskParameterName = regexp.MustCompile(`^RA_TASK_[A-Z][A-Z0-9_]{0,95}$`)

// validateTaskEnvironment is an allowlist, not a list of known secret names.
// Names are compared without case because Windows environment names are
// case-insensitive. Errors never include values, which may be sensitive.
func validateTaskEnvironment(task map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(task))
	units := 0
	for name, value := range task {
		key := strings.ToUpper(name)
		if name == "" || len(name) > 128 || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("invalid task environment variable name or value")
		}
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("ambiguous case-insensitive task environment name %q", key)
		}
		allowed := false
		switch key {
		case "PYTHONPATH":
			// Paths are checked against the workspace at translation and launch.
			allowed = true
		case "PYTHONDONTWRITEBYTECODE":
			allowed = value == "0" || value == "1"
		case "SEED", "RANDOM_SEED", "NUMPY_SEED", "PYTHONHASHSEED":
			_, err := strconv.ParseUint(value, 10, 32)
			allowed = err == nil && value != ""
		case "OMP_NUM_THREADS", "OPENBLAS_NUM_THREADS", "MKL_NUM_THREADS", "NUMEXPR_NUM_THREADS", "VECLIB_MAXIMUM_THREADS", "NUM_WORKERS":
			n, err := strconv.Atoi(value)
			allowed = err == nil && n >= 1 && n <= 4096
		case "TOKENIZERS_PARALLELISM", "WANDB_DISABLED":
			allowed = value == "true" || value == "false" || value == "0" || value == "1"
		case "HF_HUB_OFFLINE", "TRANSFORMERS_OFFLINE":
			allowed = value == "0" || value == "1"
		case "WANDB_MODE":
			allowed = value == "offline" || value == "disabled"
		default:
			allowed = taskParameterName.MatchString(key)
		}
		if !allowed {
			return nil, fmt.Errorf("task environment variable %q is not permitted by the native environment policy", key)
		}
		units += len(utf16.Encode([]rune(key))) + len(utf16.Encode([]rune(value))) + 2
		if len(value) > 8192 || units > maxEnvironmentUnits {
			return nil, fmt.Errorf("task environment exceeds the native environment size limit")
		}
		out[key] = value
	}
	return out, nil
}

// containedEnvironmentPath resolves existing components, including symlinks,
// before accepting a path. Missing descendants may be created later, but may
// never be reached through an existing link outside the workspace.
func containedEnvironmentPath(root, candidate string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("invalid environment workspace")
	}
	resolvedRoot, err := canonicalEnvironmentPath(root)
	if err != nil {
		return "", fmt.Errorf("environment workspace is unavailable")
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return "", fmt.Errorf("invalid environment path")
	}
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("environment path is outside the workspace")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		if _, err := os.Lstat(current); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return "", fmt.Errorf("cannot inspect environment path")
		}
		resolved, err := canonicalEnvironmentPath(current)
		if err != nil {
			return "", fmt.Errorf("cannot resolve environment path")
		}
		inside, err := filepath.Rel(resolvedRoot, resolved)
		if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
			return "", fmt.Errorf("environment path crosses a link outside the workspace")
		}
	}
	return candidate, nil
}

func translatePythonPath(root, value string) (string, error) {
	if value == "" || strings.ContainsAny(value, "\\;\x00") {
		return "", fmt.Errorf("PYTHONPATH must contain logical workspace paths")
	}
	var paths []string
	for _, path := range strings.Split(value, ":") {
		if path != "/workspace" && !strings.HasPrefix(path, "/workspace/") {
			return "", fmt.Errorf("PYTHONPATH must contain logical workspace paths")
		}
		for _, part := range strings.Split(path, "/") {
			if part == ".." || part == "." {
				return "", fmt.Errorf("PYTHONPATH must not contain traversal components")
			}
		}
		path, err := containedEnvironmentPath(root, filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(strings.TrimPrefix(path, "/workspace"), "/"))))
		if err != nil {
			return "", err
		}
		paths = append(paths, path)
	}
	return strings.Join(paths, string(os.PathListSeparator)), nil
}

// researchEnvironment never imports os.Environ. Only OS-discovered system
// directories, a trusted interpreter and validated task parameters enter it.
func researchEnvironment(root, interpreter string, task map[string]string) ([]string, error) {
	validated, err := validateTaskEnvironment(task)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("environment workspace is required")
	}
	root = filepath.Clean(root)
	if interpreter != "" && !filepath.IsAbs(interpreter) {
		return nil, fmt.Errorf("trusted interpreter must be an absolute path")
	}
	if value, ok := validated["PYTHONPATH"]; ok {
		if value == "" {
			return nil, fmt.Errorf("PYTHONPATH must contain workspace paths")
		}
		for _, path := range filepath.SplitList(value) {
			if !filepath.IsAbs(path) {
				return nil, fmt.Errorf("PYTHONPATH must contain absolute workspace paths")
			}
			if _, err := containedEnvironmentPath(root, path); err != nil {
				return nil, err
			}
		}
	}
	base, err := systemEnvironment()
	if err != nil {
		return nil, err
	}
	private, err := containedEnvironmentPath(root, filepath.Join(root, ".ra-env"))
	if err != nil {
		return nil, err
	}
	workspace, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open private environment workspace: %w", err)
	}
	defer workspace.Close()
	dirs := map[string]string{
		"HOME": "home", "USERPROFILE": "home", "APPDATA": "config", "LOCALAPPDATA": "cache",
		"XDG_CONFIG_HOME": "config", "XDG_CACHE_HOME": "cache", "XDG_DATA_HOME": "data",
		"TEMP": "tmp", "TMP": "tmp", "TMPDIR": "tmp", "MPLCONFIGDIR": "matplotlib",
		"HF_HOME": "huggingface", "TORCH_HOME": "torch", "PIP_CACHE_DIR": "pip", "PYTHONPYCACHEPREFIX": "pycache",
	}
	privateDirectories := make([]string, 0, len(dirs))
	for key, leaf := range dirs {
		dir := filepath.Join(private, leaf)
		if _, err := containedEnvironmentPath(root, dir); err != nil {
			return nil, err
		}
		if err := workspace.MkdirAll(filepath.Join(".ra-env", leaf), 0o700); err != nil {
			return nil, fmt.Errorf("prepare private process environment: %w", err)
		}
		base[key] = dir
		privateDirectories = append(privateDirectories, leaf)
	}
	if _, err := containedEnvironmentPath(root, private); err != nil {
		return nil, err
	}
	if err := prepareEnvironmentDirectories(private, privateDirectories...); err != nil {
		return nil, err
	}
	if interpreter != "" {
		base["PATH"] = filepath.Dir(interpreter) + string(os.PathListSeparator) + base["PATH"]
		if parent := filepath.Base(filepath.Dir(interpreter)); strings.EqualFold(parent, "Scripts") || parent == "bin" {
			base["VIRTUAL_ENV"] = filepath.Dir(filepath.Dir(interpreter))
		}
	}
	base["PYTHONNOUSERSITE"] = "1"
	base["PYTHONUNBUFFERED"] = "1"
	base["PYTHONIOENCODING"] = "utf-8"
	base["PYTHONUTF8"] = "1"
	base["MPLBACKEND"] = "Agg"
	base["NoDefaultCurrentDirectoryInExePath"] = "1"
	for key, value := range validated {
		base[key] = value
	}
	return serializeEnvironment(base)
}

func installationEnvironment(root, runtimeRoot string) ([]string, error) {
	env, err := researchEnvironment(root, "", nil)
	if err != nil {
		return nil, err
	}
	return append(env,
		"UV_PYTHON_INSTALL_DIR="+filepath.Join(runtimeRoot, "python"),
		"UV_CACHE_DIR="+filepath.Join(root, ".ra-env", "uv-cache"),
		// Runtime ACL changes must never affect a hardlinked cache or host
		// file. Every installed package belongs to this private runtime.
		"UV_LINK_MODE=copy", "UV_NO_CONFIG=1", "UV_NO_PROGRESS=1", "PIP_CONFIG_FILE="+os.DevNull,
	), nil
}

func serializeEnvironment(values map[string]string) ([]string, error) {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	units := 1
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		value := values[key]
		if strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("invalid native environment entry")
		}
		entry := key + "=" + value
		units += len(utf16.Encode([]rune(entry))) + 1
		env = append(env, entry)
	}
	if units > maxEnvironmentUnits {
		return nil, fmt.Errorf("native environment exceeds the size limit")
	}
	return env, nil
}
