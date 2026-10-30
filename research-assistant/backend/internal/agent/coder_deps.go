package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

func (a *CoderAgent) resolveDependenciesTask(ctx context.Context, task *models.Task) error {
	workspacePath := strings.TrimSpace(extractTaskInputLike(task, "workspace_path"))
	codeFilePath := strings.TrimSpace(extractTaskInputLike(task, "code_file_path"))
	code := extractTaskInputLike(task, "generated_code")
	if strings.TrimSpace(code) == "" && strings.TrimSpace(codeFilePath) != "" {
		if raw, err := os.ReadFile(codeFilePath); err == nil {
			code = string(raw)
		}
	}
	if strings.TrimSpace(code) == "" {
		code = task.Code
	}

	dependencies := make([]string, 0, 16)
	codeDependencies := detectPythonDependencies(code)
	if isReproductionSmokeRunnerPath(codeFilePath) {
		dependencies = append(dependencies, codeDependencies...)
	} else if strings.TrimSpace(workspacePath) != "" {
		workspaceDependencies := filterWorkspaceLocalDependencies(detectWorkspacePythonDependencies(workspacePath), workspacePath)
		dependencies = append(dependencies, workspaceDependencies...)
		repoDependencies := detectRepositoryDependencies(workspacePath)
		if len(workspaceDependencies) > 0 || len(codeDependencies) > 0 {
			repoDependencies = filterDependenciesToRoots(repoDependencies, dependencyRootSet(append(append([]string{}, workspaceDependencies...), codeDependencies...)))
		}
		dependencies = append(dependencies, repoDependencies...)
	}
	dependencies = append(dependencies, codeDependencies...)
	if strings.TrimSpace(workspacePath) != "" {
		dependencies = filterWorkspaceLocalDependencies(dependencies, workspacePath)
	}
	dependencies = filterFrameworkBenchmarkDependencies(task, dependencies)
	dependencies = normalizeDependenciesForPython39(uniqueDependencies(dependencies))
	dependencies = filterFrameworkBenchmarkDependencies(task, dependencies)
	payload, err := json.Marshal(dependencies)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("failed to marshal dependency spec: %v", err)
		return err
	}

	task.Result = string(payload)
	task.Status = models.StatusCompleted
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = map[string]string{
		"dependency_spec": string(payload),
	}
	logToContext(ctx, "[%s] 识别到依赖: %s", a.Name, chooseNonEmpty(strings.Join(dependencies, ", "), "none"))
	return nil
}

func isReproductionSmokeRunnerPath(path string) bool {
	return strings.EqualFold(filepath.Base(strings.TrimSpace(path)), "research-assistant_repro_smoke.py")
}

func sandboxIdentityForTask(task *models.Task) sandbox.SandboxIdentity {
	identity := sandbox.SandboxIdentity{}
	if task == nil {
		return identity
	}
	identity.TaskID = task.ID
	if task.Metadata != nil {
		identity.TenantID = metadataString(task, "tenant_id")
		identity.PlanID = metadataString(task, "plan_id")
	}
	return identity
}

func metadataString(task *models.Task, key string) string {
	if task == nil || task.Metadata == nil {
		return ""
	}
	value, ok := task.Metadata[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func extractTaskInput(task *models.Task, key string) string {
	if task == nil || task.Inputs == nil {
		return ""
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}

func extractTaskInputLike(task *models.Task, key string) string {
	if value := extractTaskInput(task, key); strings.TrimSpace(value) != "" {
		return value
	}
	if task == nil || task.Inputs == nil {
		return ""
	}
	for inputKey, value := range task.Inputs {
		if strings.HasSuffix(strings.TrimSpace(inputKey), "_"+key) || strings.EqualFold(strings.TrimSpace(inputKey), key) {
			if stringValue := extractTaskInput(task, inputKey); strings.TrimSpace(stringValue) != "" {
				return stringValue
			}
		}
		if value == nil {
			continue
		}
	}
	return ""
}

func shouldRunDirectCodeExecution(task *models.Task) bool {
	if task == nil {
		return false
	}
	if strings.TrimSpace(task.AssignedTo) != "coder_agent" {
		return false
	}
	taskType := strings.ToLower(strings.TrimSpace(task.Type))
	switch taskType {
	case "code_execution", "render_plot", "result_visualization":
		return true
	default:
		return false
	}
}

func inferSandboxTaskKind(task *models.Task) string {
	if task == nil {
		return "execute_code"
	}

	switch strings.ToLower(strings.TrimSpace(task.Type)) {
	case "prepare_runtime", "env_setup", "prepare_environment", "setup_runtime", "test_environment":
		return "prepare_runtime"
	case "install_dependencies", "dependency_install":
		return "install_dependencies"
	case "runtime_probe":
		return "runtime_probe"
	case "image_prepare":
		return "image_prepare"
	case "runtime_validate":
		return "runtime_validate"
	case "execute_code", "baseline_run", "run_benchmark":
		return "execute_code"
	}

	context := strings.ToLower(strings.Join([]string{task.Name, task.Description}, " "))
	switch {
	case strings.Contains(context, "prepare_runtime"), strings.Contains(context, "env_setup"), strings.Contains(context, "prepare runtime"), strings.Contains(context, "prepare environment"), strings.Contains(context, "setup runtime"), strings.Contains(context, "test environment"):
		return "prepare_runtime"
	case strings.Contains(context, "install_depend"), strings.Contains(context, "pip install"):
		return "install_dependencies"
	case strings.Contains(context, "execute_code"), strings.Contains(context, "baseline_run"), strings.Contains(context, "run benchmark"), strings.Contains(context, "benchmark"), strings.Contains(context, "execute"):
		return "execute_code"
	default:
		if strings.TrimSpace(extractTaskInputLike(task, "dependency_spec")) != "" && strings.TrimSpace(extractTaskInputLike(task, "runtime_session")) != "" && strings.TrimSpace(extractTaskInputLike(task, "generated_code")) == "" {
			return "install_dependencies"
		}
		if strings.TrimSpace(extractTaskInputLike(task, "generated_code")) == "" {
			return "prepare_runtime"
		}
		return "execute_code"
	}
}

func parseDependencySpec(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	var deps []string
	if err := json.Unmarshal([]byte(raw), &deps); err == nil {
		cleaned := sanitizeDependencyTokens(deps)
		for _, item := range cleaned {
			if !isValidDependencyToken(item) {
				return nil, fmt.Errorf("invalid dependency token %q", item)
			}
		}
		return uniqueDependencies(cleaned), nil
	}

	if looksLikeRichTextDependencySpec(raw) {
		return nil, fmt.Errorf("expected structured dependency list but received report-like content")
	}

	parts := strings.Split(raw, ",")
	cleaned := sanitizeDependencyTokens(parts)
	if len(cleaned) == 0 {
		return nil, nil
	}
	for _, item := range cleaned {
		if !isValidDependencyToken(item) {
			return nil, fmt.Errorf("invalid dependency token %q", item)
		}
	}
	return uniqueDependencies(cleaned), nil
}

// sanitizeDependencyTokens normalizes dependency_spec into a pip-friendly argv list.
//
// Why:
// - LLM outputs sometimes include Markdown backticks or trailing commas.
// - Some entries may embed multiple argv parts, e.g. "--find-links https://...".
// This function keeps things simple and safe: it strips obvious formatting noise and
// splits on whitespace (no shell involved; we pass argv directly).
func sanitizeDependencyTokens(items []string) []string {
	out := make([]string, 0, len(items))
	for _, raw := range items {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}

		// Remove Markdown/code formatting and common punctuation noise.
		item = strings.Trim(item, `"'`)
		item = strings.ReplaceAll(item, "`", "")
		item = strings.TrimSpace(item)

		// Some outputs append commas to tokens (or even to URLs within backticks).
		item = strings.TrimRight(item, ",")
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}

		// If a single list entry contains multiple argv parts (e.g. pip options),
		// split them so pip gets correct arguments.
		for _, part := range strings.Fields(item) {
			part = strings.TrimSpace(strings.Trim(part, `"'`))
			part = strings.ReplaceAll(part, "`", "")
			part = strings.TrimRight(part, ",")
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			out = append(out, part)
		}
	}
	return out
}

func detectPythonDependencies(code string) []string {
	deps := make([]string, 0, 8)
	lines := strings.Split(code, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import ") {
			modules := strings.Split(strings.TrimPrefix(trimmed, "import "), ",")
			for _, module := range modules {
				name := strings.Fields(strings.TrimSpace(module))
				if len(name) == 0 {
					continue
				}
				importPath := name[0]
				if isPythonStandardLibraryModule(importPath) {
					continue
				}
				deps = append(deps, mapPythonImportToPackage(importPath))
			}
		}
		if strings.HasPrefix(trimmed, "from ") {
			parts := strings.Fields(trimmed)
			if len(parts) < 2 {
				continue
			}
			importPath := parts[1]
			if isPythonStandardLibraryModule(importPath) {
				continue
			}
			deps = append(deps, mapPythonImportToPackage(importPath))
		}
	}

	return filterStandardLibraryDependencies(uniqueDependencies(deps))
}

func uniqueDependencies(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(strings.Trim(item, `"'`))
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	return out
}

func dependencyRootName(dependency string) string {
	root := strings.ToLower(strings.TrimSpace(dependency))
	root = strings.Split(root, "[")[0]
	root = strings.Split(root, "<")[0]
	root = strings.Split(root, ">")[0]
	root = strings.Split(root, "=")[0]
	return strings.TrimSpace(root)
}

func canonicalDependencyRoot(dependency string) string {
	root := strings.ReplaceAll(dependencyRootName(dependency), "_", "-")
	switch root {
	case "pytorch":
		return "torch"
	case "msgpack-python":
		return "msgpack"
	default:
		return root
	}
}

func dependencyRootSet(dependencies []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, dependency := range dependencies {
		root := canonicalDependencyRoot(dependency)
		if root == "" {
			continue
		}
		out[root] = struct{}{}
	}
	return out
}

func filterDependenciesToRoots(dependencies []string, roots map[string]struct{}) []string {
	if len(dependencies) == 0 || len(roots) == 0 {
		return dependencies
	}
	out := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		if _, ok := roots[canonicalDependencyRoot(dependency)]; ok {
			out = append(out, dependency)
		}
	}
	return out
}

func filterWorkspaceLocalDependencies(dependencies []string, workspacePath string) []string {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" || len(dependencies) == 0 {
		return dependencies
	}
	out := make([]string, 0, len(dependencies))
	for _, dependency := range dependencies {
		root := strings.ReplaceAll(dependencyRootName(dependency), "-", "_")
		if root != "" && workspaceHasPythonModule(workspacePath, root) {
			continue
		}
		out = append(out, dependency)
	}
	return out
}

func workspaceHasPythonModule(workspacePath, module string) bool {
	found := false
	_ = filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".github", "__pycache__", ".venv", "venv", "node_modules", "dist", "build":
				return filepath.SkipDir
			}
			if name == module {
				found = true
				return filepath.SkipDir
			}
			return nil
		}
		if name == module+".py" {
			found = true
		}
		return nil
	})
	return found
}

func detectWorkspacePythonDependencies(workspacePath string) []string {
	deps := make([]string, 0, 16)
	_ = filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".github", "__pycache__", ".venv", "venv", "node_modules", "dist", "build", "docs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(name), ".py") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		deps = append(deps, detectPythonDependencies(string(raw))...)
		return nil
	})
	return uniqueDependencies(deps)
}

func normalizeDependenciesForPython39(items []string) []string {
	normalized := make([]string, 0, len(items)+1)
	seen := map[string]struct{}{}
	appendOnce := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}

	// Some packages have optional submodules split into extra distributions.
	// Example: newer langchain code paths may import langchain_community at runtime.
	// We add a minimal compatibility shim: if langchain is requested, ensure
	// langchain-community is present to avoid ModuleNotFoundError during execution.
	hasLangchain := false
	hasLangchainCommunity := false
	for _, item := range items {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		// Normalize to root package name for detection (strip version/extras).
		root := strings.Split(name, "[")[0]
		root = strings.Split(root, "<")[0]
		root = strings.Split(root, ">")[0]
		root = strings.Split(root, "=")[0]
		root = strings.TrimSpace(root)
		switch root {
		case "langchain":
			hasLangchain = true
		case "langchain-community":
			hasLangchainCommunity = true
		}
	}

	for _, item := range filterStandardLibraryDependencies(items) {
		name := strings.TrimSpace(item)
		lowerName := strings.ToLower(name)
		root := canonicalDependencyRoot(lowerName)
		switch {
		case root == "python" || root == "python3":
			continue
		case lowerName == "oscar==2.2.1":
			// 最小纠错：PyPI 上 `oscar` 没有 2.2.1；结合实际安装日志，
			// `django-oscar` 可用到 2.2，因此降到已存在的稳定版本。
			appendOnce("django-oscar==2.2")
		case root == "torch" && strings.HasPrefix(lowerName, "pytorch"):
			appendOnce("torch")
		case root == "msgpack" && strings.HasPrefix(lowerName, "msgpack-python"):
			appendOnce("msgpack")
		case lowerName == "tensorflow==1.14.0" || lowerName == "tensorflow==1.15.0":
			continue
		case lowerName == "llama-index":
			appendOnce("llama-index<0.12")
			appendOnce("pydantic<2.10")
		default:
			appendOnce(name)
		}
	}

	if hasLangchain && !hasLangchainCommunity {
		appendOnce("langchain-community")
	}
	return normalized
}

func filterStandardLibraryDependencies(items []string) []string {
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(strings.Trim(item, `"'`))
		if isPythonStandardLibraryModule(name) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func pipInstallArgs(packages ...string) ([]string, error) {
	cleaned := make([]string, 0, len(packages))
	for _, pkg := range packages {
		if trimmed := strings.TrimSpace(pkg); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return nil, fmt.Errorf("pip install requires at least one package")
	}
	args := []string{"python3", "-m", "pip", "install", "--default-timeout", defaultPipTimeoutSec}
	if indexURL := strings.TrimSpace(os.Getenv("PIP_INDEX_URL")); indexURL != "" {
		args = append(args, "--index-url", indexURL)
	}
	return append(args, cleaned...), nil
}

func configuredSandboxImage() string {
	if image := strings.TrimSpace(os.Getenv("SANDBOX_DEFAULT_IMAGE")); image != "" {
		return image
	}
	return defaultSandboxImage
}
