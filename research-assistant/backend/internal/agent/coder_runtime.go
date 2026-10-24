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
	for _, dep := range spec.PythonDependencies {
		module := strings.ReplaceAll(strings.TrimSpace(dep.Name), "-", "_")
		if module == "" {
			continue
		}
		check, checkErr := a.Sandbox.ExecCommand(ctx, session, []string{"python3", "-c", "import " + module})
		if checkErr != nil || check == nil || check.ExitCode != 0 {
			missing = append(missing, models.MissingRequirement{Kind: "dependency_install_failed", Name: dep.Name, Detail: "runtime import check failed", Remediation: "检查 pip 安装日志和 Python 版本兼容性。"})
		}
	}
	image := extractTaskInputLike(task, "runtime_image")
	digest := extractTaskInputLike(task, "image_digest")
	finished := time.Now()
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
