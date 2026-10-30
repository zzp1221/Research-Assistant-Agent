package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"research-assistant-backend/internal/models"

	"github.com/pelletier/go-toml/v2"
)

var pythonImageVersionRE = regexp.MustCompile(`^python:3\.(\d+)(-[A-Za-z0-9._-]+)?$`)

type pyprojectRuntimeMetadata struct {
	Project struct {
		RequiresPython string `toml:"requires-python"`
	} `toml:"project"`
}

func sandboxImageForWorkspace(defaultImage, workspacePath string) (string, string) {
	defaultImage = strings.TrimSpace(defaultImage)
	if defaultImage == "" {
		defaultImage = defaultSandboxImage
	}
	requiredMinor, constraint, source := repositoryPythonRequirement(workspacePath)
	if requiredMinor <= 0 {
		return defaultImage, ""
	}
	currentMinor, suffix, ok := pythonImageMinor(defaultImage)
	if !ok || currentMinor >= requiredMinor {
		return defaultImage, fmt.Sprintf("%s declares requires-python %q", source, constraint)
	}
	return fmt.Sprintf("python:3.%d%s", requiredMinor, suffix), fmt.Sprintf("%s declares requires-python %q", source, constraint)
}

func pythonImageMinor(image string) (int, string, bool) {
	matches := pythonImageVersionRE.FindStringSubmatch(strings.TrimSpace(image))
	if len(matches) != 3 {
		return 0, "", false
	}
	minor, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, "", false
	}
	return minor, matches[2], true
}

func repositoryPythonRequirement(workspacePath string) (int, string, string) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return 0, "", ""
	}
	pyprojectPath := filepath.Join(workspacePath, "pyproject.toml")
	raw, err := os.ReadFile(pyprojectPath)
	if err != nil {
		return 0, "", ""
	}
	var metadata pyprojectRuntimeMetadata
	if err := toml.Unmarshal(raw, &metadata); err != nil {
		return 0, "", ""
	}
	constraint := strings.TrimSpace(metadata.Project.RequiresPython)
	minor := minimumSupportedPythonMinor(constraint)
	if minor <= 0 {
		return 0, "", ""
	}
	return minor, constraint, "pyproject.toml"
}

func minimumSupportedPythonMinor(constraint string) int {
	constraint = strings.ReplaceAll(strings.TrimSpace(constraint), " ", "")
	minimum := 0
	for _, clause := range strings.Split(constraint, ",") {
		operator := ""
		for _, candidate := range []string{">=", "~=", "==", ">"} {
			if strings.HasPrefix(clause, candidate) {
				operator = candidate
				clause = strings.TrimPrefix(clause, candidate)
				break
			}
		}
		if operator == "" {
			continue
		}
		parts := strings.Split(clause, ".")
		if len(parts) < 2 || parts[0] != "3" {
			continue
		}
		minor, err := strconv.Atoi(strings.TrimRight(parts[1], ".*"))
		if err != nil {
			continue
		}
		if operator == ">" {
			minor++
		}
		if minor > minimum {
			minimum = minor
		}
	}
	return minimum
}

func (a *CoderAgent) validatePythonSyntaxInSandbox(ctx context.Context, sandboxID string, code string) error {
	encoded, err := json.Marshal(code)
	if err != nil {
		return fmt.Errorf("marshal python source failed: %w", err)
	}

	validator := fmt.Sprintf(
		"import ast\nsource = %s\nast.parse(source)\nprint('syntax_ok')\n",
		string(encoded),
	)
	res, err := a.Sandbox.RunPythonCodeStream(ctx, sandboxID, validator, func(stream string, line string) {
		logToContext(ctx, "[%s] syntax %s: %s", a.Name, stream, line)
	})
	if err != nil {
		return fmt.Errorf("syntax validation sandbox error: %w", err)
	}
	if res == nil {
		return fmt.Errorf("syntax validation returned nil response")
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("%s", chooseNonEmpty(strings.TrimSpace(res.Stderr), strings.TrimSpace(res.Stdout), fmt.Sprintf("syntax validation exited with code %d", res.ExitCode)))
	}
	return nil
}

func chooseNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func buildArtifactValueMap(task *models.Task, base map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range base {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	if task == nil {
		return out
	}
	for _, outputKey := range task.OutputArtifacts {
		lowerKey := strings.ToLower(strings.TrimSpace(outputKey))
		switch {
		case strings.Contains(lowerKey, "runtime_session"):
			out[outputKey] = chooseNonEmpty(base["runtime_session"], base["prepared_runtime"])
		case strings.Contains(lowerKey, "prepared_runtime"):
			out[outputKey] = chooseNonEmpty(base["prepared_runtime"], base["runtime_session"])
		case strings.Contains(lowerKey, "dependency_install_report"):
			out[outputKey] = base["dependency_install_report"]
		case strings.Contains(lowerKey, "dependency_spec"):
			out[outputKey] = base["dependency_spec"]
		}
	}
	return out
}

func detectRepositoryDependencies(workspacePath string) []string {
	candidates := []string{
		filepath.Join(workspacePath, "requirements.txt"),
		filepath.Join(workspacePath, "environment.yml"),
		filepath.Join(workspacePath, "environment.yaml"),
		filepath.Join(workspacePath, "setup.py"),
		filepath.Join(workspacePath, "pyproject.toml"),
	}

	deps := make([]string, 0, 16)
	for _, candidate := range candidates {
		raw, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		text := string(raw)
		switch filepath.Base(candidate) {
		case "requirements.txt":
			for _, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				deps = append(deps, line)
			}
		default:
			deps = append(deps, detectPythonDependencies(text)...)
		}
	}
	return uniqueDependencies(deps)
}

func shellEscape(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func buildMountedWorkspacePythonCommand(relPath string) string {
	entryDir := filepath.ToSlash(filepath.Dir(relPath))
	if entryDir == "." || entryDir == "/" {
		entryDir = ""
	}

	workspaceEntryDir := "/workspace"
	if entryDir != "" {
		workspaceEntryDir = "/workspace/" + strings.TrimPrefix(entryDir, "/")
	}

	// 兼容部分旧仓库：
	// 1. 目录里有 Python 源码但没有 __init__.py
	// 2. 与 site-packages 中的同名包（如 utils）发生导入冲突
	// 这里在执行前最小化补齐包标记，并显式把源码目录放到 PYTHONPATH 最前面。
	return fmt.Sprintf(
		"cd /workspace && "+
			"ENTRY_DIR=%s && "+
			"find \"$ENTRY_DIR\" -type d | while read -r d; do "+
			"if find \"$d\" -maxdepth 1 -type f -name '*.py' | grep -q . && [ ! -f \"$d/__init__.py\" ]; then : > \"$d/__init__.py\"; fi; "+
			"done && "+
			"PYTHONPATH=%s:/workspace:${PYTHONPATH:-} python3 %s",
		shellEscape(workspaceEntryDir),
		shellEscape(workspaceEntryDir),
		shellEscape(filepath.ToSlash(relPath)),
	)
}

func looksLikeRichTextDependencySpec(raw string) bool {
	normalized := strings.ToLower(raw)
	markers := []string{
		"\n#",
		"\n##",
		"```",
		"|",
		"任务目标",
		"结论",
		"建议",
		"report",
		"summary",
		"分析",
	}
	for _, marker := range markers {
		if strings.Contains(normalized, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func isValidDependencyToken(value string) bool {
	if value == "" {
		return false
	}
	// pip argv is passed without a shell, but we still forbid whitespace and quoting chars
	// to avoid accidental token merging or Markdown artifacts.
	if strings.ContainsAny(value, " \t\r\n`\"'") {
		return false
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		// Allow common requirement spec chars and URL chars used by pip options.
		case strings.ContainsRune("._-<>!=[]:+/@%?&=#~", r):
		case r == '+':
		default:
			return false
		}
	}
	return true
}
