package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

func runtimeSpecFromTask(task *models.Task) (models.ReproductionRuntimeSpec, error) {
	var spec models.ReproductionRuntimeSpec
	raw := inputJSONString(task, "reproduction_runtime_spec")
	if strings.TrimSpace(raw) == "" {
		return spec, fmt.Errorf("missing reproduction_runtime_spec input")
	}
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return spec, fmt.Errorf("invalid reproduction_runtime_spec: %w", err)
	}
	return spec, nil
}

func imageAllowedByList(image string, allowlist []string) bool {
	image = strings.TrimSpace(image)
	if image == "" || len(allowlist) == 0 {
		return true
	}
	for _, allowed := range allowlist {
		allowed = strings.TrimSpace(allowed)
		if allowed == image || strings.HasPrefix(image, allowed) {
			return true
		}
	}
	return false
}

func (a *CoderAgent) runtimeProbe(ctx context.Context, task *models.Task) error {
	spec, err := runtimeSpecFromTask(task)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
		return nil
	}
	caps, err := a.Sandbox.Capabilities(ctx)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("runtime capabilities probe failed: %v", err)
		return nil
	}
	missing := make([]models.MissingRequirement, 0)
	if spec.GPURequired && !caps.GPURuntimeAvailable {
		missing = append(missing, models.MissingRequirement{Kind: "gpu_unavailable", Name: spec.GPUType, Detail: "当前 sandbox 没有可用的 NVIDIA runtime", Remediation: "安装 NVIDIA Container Toolkit，或改用 CPU 可运行的论文实验。"})
	}
	if spec.DiskGB > 0 && float64(caps.DiskFreeBytes)/float64(1<<30) < spec.DiskGB {
		missing = append(missing, models.MissingRequirement{Kind: "disk_insufficient", Name: "workspace", Detail: fmt.Sprintf("需要 %.1f GB，当前可用 %.2f GB", spec.DiskGB, float64(caps.DiskFreeBytes)/float64(1<<30)), Remediation: "清理磁盘或扩容后重试。"})
	}
	baseImage := strings.TrimSpace(spec.BaseImage)
	if baseImage == "" {
		baseImage = "python:3.11-bullseye"
	}
	if !imageAllowedByList(baseImage, caps.ImageAllowlist) {
		missing = append(missing, models.MissingRequirement{Kind: "image_not_allowed", Name: baseImage, Detail: "镜像不在 SANDBOX_IMAGE_ALLOWLIST 中", Remediation: "改用 allowlist 中的预构建镜像。"})
	}
	capsJSON, _ := json.Marshal(caps)
	missingJSON, _ := json.Marshal(missing)
	task.Result = string(capsJSON)
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = map[string]string{
		"runtime_capabilities": string(capsJSON),
		"missing_requirements": string(missingJSON),
	}
	if len(missing) > 0 {
		task.Status = models.StatusFailed
		task.Error = "runtime probe found missing requirements"
		return nil
	}
	task.Status = models.StatusCompleted
	return nil
}

func (a *CoderAgent) imagePrepare(ctx context.Context, task *models.Task) error {
	spec, err := runtimeSpecFromTask(task)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
		return nil
	}
	image := strings.TrimSpace(spec.BaseImage)
	if image == "" {
		image = "python:3.11-bullseye"
	}
	info, err := a.Sandbox.EnsureImage(ctx, image)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
		return nil
	}
	task.Result = info.Image
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = map[string]string{
		"runtime_image": info.Image,
		"image_digest":  info.Digest,
	}
	task.Status = models.StatusCompleted
	return nil
}

func (a *CoderAgent) runtimeValidate(ctx context.Context, task *models.Task) error {
	spec, err := runtimeSpecFromTask(task)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
		return nil
	}
	session := chooseNonEmpty(extractTaskInputLike(task, "prepared_runtime"), extractTaskInputLike(task, "runtime_session"))
	if strings.TrimSpace(session) == "" {
		task.Status = models.StatusFailed
		task.Error = "missing prepared_runtime for runtime validation"
		return nil
	}
	started := time.Now()
	response, err := a.Sandbox.ExecCommandStream(ctx, session, []string{"bash", "-lc", "cd /workspace && python3 --version && python3 -m pip freeze"}, func(stream, line string) {
		logToContext(ctx, "[%s] runtime validate %s: %s", a.Name, stream, line)
	})
	if err != nil || response == nil || response.ExitCode != 0 {
		detail := "runtime validation command failed"
		if err != nil {
			detail = err.Error()
		} else if response != nil {
			detail = chooseNonEmpty(response.Stderr, response.Stdout, detail)
		}
		task.Status = models.StatusFailed
		task.Error = detail
		return nil
	}
	pythonVersion := ""
	packages := make([]models.PackageSpec, 0)
	for _, line := range strings.Split(response.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "Python ") {
			pythonVersion = strings.TrimSpace(strings.TrimPrefix(line, "Python "))
			continue
		}
		if idx := strings.Index(line, "=="); idx > 0 {
			packages = append(packages, models.PackageSpec{Name: strings.TrimSpace(line[:idx]), Version: strings.TrimSpace(line[idx+2:])})
		}
	}
	missing := make([]models.MissingRequirement, 0)
	depNames := make([]string, 0, len(spec.PythonDependencies))
	for _, dep := range spec.PythonDependencies {
		if name := strings.TrimSpace(dep.Name); name != "" {
			depNames = append(depNames, name)
		}
	}
	if len(depNames) > 0 {
		results, checkErr := a.checkPythonDependencies(ctx, session, depNames)
		if checkErr != nil {
			// The batch probe failed (for example a very old Python without
			// importlib.metadata.packages_distributions). Fall back to the
			// built-in distribution→module mapping one dependency at a time.
			results = map[string]pythonDependencyCheck{}
			for _, name := range depNames {
				results[name] = a.checkSinglePythonDependency(ctx, session, name)
			}
		}
		for _, name := range depNames {
			check := results[name]
			if check.Imported {
				continue
			}
			detail := "runtime import check failed"
			if check.Installed {
				detail = fmt.Sprintf("distribution 已安装，但候选模块 %s 均无法导入", strings.Join(check.Candidates, ", "))
			} else if len(check.Candidates) > 0 {
				detail = fmt.Sprintf("distribution 未安装或不可见；候选模块 %s", strings.Join(check.Candidates, ", "))
			}
			missing = append(missing, models.MissingRequirement{
				Kind:        "dependency_install_failed",
				Name:        name,
				Detail:      detail,
				Remediation: fmt.Sprintf("检查 pip 安装日志；package=%s，module=%s", name, strings.Join(check.Candidates, "|")),
			})
		}
	}
	image := extractTaskInputLike(task, "runtime_image")
	digest := extractTaskInputLike(task, "image_digest")
	finished := time.Now()
	networkMode := "none"
	if task.Metadata != nil {
		if value, ok := task.Metadata["sandbox_network_mode"].(string); ok && strings.TrimSpace(value) != "" {
			networkMode = strings.TrimSpace(value)
		}
	}
	var networkRX, networkTX int64
	if a.Sandbox != nil && session != "" {
		if usage, usageErr := a.Sandbox.SandboxUsage(ctx, session); usageErr == nil && usage != nil {
			networkRX = usage.NetworkRXBytes
			networkTX = usage.NetworkTXBytes
			if strings.TrimSpace(usage.NetworkMode) != "" && usage.NetworkMode != "none" {
				networkMode = usage.NetworkMode
			}
		}
	}
	policy := runtimePolicyForTask(task)
	hardLimits := policy.HardLimits
	manifest := models.EnvironmentManifest{
		Version:        "environment.manifest/v1",
		Image:          image,
		ImageDigest:    digest,
		Language:       "python",
		PythonVersion:  pythonVersion,
		PipFreeze:      packages,
		SystemPackages: spec.SystemPackages,
		RunCommand:     spec.RunCommand,
		WorkingDir:     chooseNonEmpty(spec.WorkingDir, "/workspace"),
		NetworkMode:    networkMode,
		NetworkRXBytes: networkRX,
		NetworkTXBytes: networkTX,
		HardLimits:     &hardLimits,
		StartedAt:      started,
		FinishedAt:     &finished,
		Status:         "ready",
	}
	if len(missing) > 0 {
		manifest.Status = "blocked"
		manifest.Missing = missing
	}
	manifestJSON, _ := json.Marshal(manifest)
	report := fmt.Sprintf("runtime validation %s; image=%s; python=%s; pip_packages=%d; missing=%d", manifest.Status, image, pythonVersion, len(packages), len(missing))
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = map[string]string{
		"environment_manifest":      string(manifestJSON),
		"runtime_validation_report": report,
	}
	task.Result = string(manifestJSON)
	if len(missing) > 0 {
		task.Status = models.StatusFailed
		task.Error = "runtime validation found missing requirements"
		return nil
	}
	task.Status = models.StatusCompleted
	return nil
}

// pythonDependencyCheck is the structured result of one distribution probe.
type pythonDependencyCheck struct {
	Installed  bool     `json:"installed"`
	Imported   bool     `json:"imported"`
	Module     string   `json:"module"`
	Candidates []string `json:"candidates"`
}

// pythonDependencyProbeScript resolves distribution→top-level module names with
// importlib.metadata (top_level.txt / packages_distributions) and verifies that
// at least one candidate actually imports. This fixes the naive "-"→"_"
// replacement that broke scikit-learn, pyyaml, Pillow and opencv-python.
const pythonDependencyProbeScript = `import importlib, importlib.metadata, json, sys
deps = json.loads(sys.argv[1])
out = {}
for name in deps:
    info = {"installed": False, "imported": False, "module": "", "candidates": []}
    cands = [name.replace("-", "_").replace(".", "_")]
    try:
        dist = importlib.metadata.distribution(name)
        info["installed"] = True
        top = None
        try:
            top = dist.read_text("top_level.txt")
        except Exception:
            top = None
        if top:
            cands = [line.strip() for line in top.splitlines() if line.strip()] or cands
        else:
            try:
                mapping = importlib.metadata.packages_distributions()
                cands = [module for module, dists in mapping.items() if name in dists] or cands
            except Exception:
                pass
    except Exception:
        pass
    info["candidates"] = cands
    for cand in cands:
        try:
            importlib.import_module(cand)
            info["imported"] = True
            info["module"] = cand
            break
        except Exception:
            continue
    out[name] = info
print(json.dumps(out))
`

// knownDistributionModules covers the most common packages whose import name
// differs from the distribution name, for runtimes where metadata is missing.
var knownDistributionModules = map[string][]string{
	"scikit-learn":           {"sklearn"},
	"scikit-image":           {"skimage"},
	"pyyaml":                 {"yaml"},
	"pillow":                 {"PIL"},
	"opencv-python":          {"cv2"},
	"opencv-python-headless": {"cv2"},
	"python-dateutil":        {"dateutil"},
	"beautifulsoup4":         {"bs4"},
	"msgpack-python":         {"msgpack"},
	"pycryptodome":           {"Crypto"},
	"python-dotenv":          {"dotenv"},
	"protobuf":               {"google.protobuf"},
	"pytorch":                {"torch"},
	"tensorflow-gpu":         {"tensorflow"},
	"faiss-cpu":              {"faiss"},
	"faiss-gpu":              {"faiss"},
	"graphviz":               {"graphviz"},
	"python-igraph":          {"igraph"},
	"scikit-optimize":        {"skopt"},
	"umap-learn":             {"umap"},
	"python-louvain":         {"community"},
	"networkx":               {"networkx"},
	"pillow-simd":            {"PIL"},
	"pyshp":                  {"shapefile"},
	"pydantic-settings":      {"pydantic_settings"},
	"langchain-community":    {"langchain_community"},
	"langchain-core":         {"langchain_core"},
	"opencv-contrib-python":  {"cv2"},
	"tables":                 {"tables"},
	"h5py":                   {"h5py"},
}

func distributionRoot(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if idx := strings.Index(name, "["); idx >= 0 {
		name = name[:idx]
	}
	for _, sep := range []string{"==", ">=", "<=", "~=", "!=", ">", "<", ";"} {
		if idx := strings.Index(name, sep); idx >= 0 {
			name = name[:idx]
		}
	}
	return strings.TrimSpace(name)
}

func pythonModuleCandidates(name string) []string {
	root := distributionRoot(name)
	if root == "" {
		return nil
	}
	candidates := make([]string, 0, 2)
	if mapped, ok := knownDistributionModules[root]; ok {
		candidates = append(candidates, mapped...)
	}
	naive := strings.ReplaceAll(root, "-", "_")
	naive = strings.ReplaceAll(naive, ".", "_")
	if naive != "" {
		candidates = append(candidates, naive)
	}
	return candidates
}

// checkPythonDependencies probes all dependencies in one sandbox command.
func (a *CoderAgent) checkPythonDependencies(ctx context.Context, session string, names []string) (map[string]pythonDependencyCheck, error) {
	payload, err := json.Marshal(names)
	if err != nil {
		return nil, err
	}
	response, err := a.Sandbox.ExecCommand(ctx, session, []string{"python3", "-c", pythonDependencyProbeScript, string(payload)})
	if err != nil {
		return nil, err
	}
	if response == nil || response.ExitCode != 0 {
		return nil, fmt.Errorf("dependency probe failed")
	}
	results := map[string]pythonDependencyCheck{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(response.Stdout)), &results); err != nil {
		return nil, err
	}
	return results, nil
}

// checkSinglePythonDependency is the fallback probe using the built-in mapping.
func (a *CoderAgent) checkSinglePythonDependency(ctx context.Context, session string, name string) pythonDependencyCheck {
	check := pythonDependencyCheck{Candidates: pythonModuleCandidates(name)}
	for _, module := range check.Candidates {
		res, err := a.Sandbox.ExecCommand(ctx, session, []string{"python3", "-c", "import " + module})
		if err == nil && res != nil && res.ExitCode == 0 {
			check.Imported = true
			check.Installed = true
			check.Module = module
			return check
		}
	}
	// Distinguish "not installed" from "installed with a different module name".
	show, showErr := a.Sandbox.ExecCommand(ctx, session, []string{"python3", "-m", "pip", "show", distributionRoot(name)})
	if showErr == nil && show != nil && show.ExitCode == 0 && strings.Contains(strings.ToLower(show.Stdout), "name:") {
		check.Installed = true
	}
	return check
}

func dependencySpecsFromSpec(spec models.ReproductionRuntimeSpec) []string {
	out := make([]string, 0, len(spec.PythonDependencies))
	for _, dep := range spec.PythonDependencies {
		if strings.TrimSpace(dep.Version) != "" {
			out = append(out, dep.Name+"=="+dep.Version)
		} else if strings.TrimSpace(dep.Name) != "" {
			out = append(out, dep.Name)
		}
	}
	return out
}
