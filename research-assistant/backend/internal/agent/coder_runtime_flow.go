package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"research-assistant-backend/internal/llmclient"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"

	"github.com/cloudwego/eino/schema"
)

func (a *CoderAgent) prepareRuntime(ctx context.Context, task *models.Task) error {
	logToContext(ctx, "[%s] Preparing runtime for %s", a.Name, task.Name)

	if existing := chooseNonEmpty(extractTaskInputLike(task, "runtime_session"), extractTaskInputLike(task, "runtime_env"), extractTaskInputLike(task, "prepared_runtime")); strings.HasPrefix(existing, "dk-") || strings.HasPrefix(existing, "os-") {
		task.Result = existing
		logToContext(ctx, "[%s] Reusing existing runtime: %s", a.Name, existing)
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["artifact_values"] = map[string]string{
			"runtime_session": existing,
		}
		task.Status = models.StatusCompleted
		return nil
	}

	mountPath := strings.TrimSpace(extractTaskInputLike(task, "workspace_path"))
	sandboxImage := strings.TrimSpace(extractTaskInputLike(task, "runtime_image"))
	runtimeReason := ""
	if sandboxImage == "" {
		if spec, specErr := runtimeSpecFromTask(task); specErr == nil && strings.TrimSpace(spec.BaseImage) != "" {
			sandboxImage = strings.TrimSpace(spec.BaseImage)
			runtimeReason = "reproduction_runtime_spec"
		}
	}
	if sandboxImage == "" {
		sandboxImage, runtimeReason = sandboxImageForWorkspace(configuredSandboxImage(), mountPath)
	}
	if runtimeReason != "" {
		logToContext(ctx, "[%s] Runtime image selected from repository metadata: %s (%s)", a.Name, sandboxImage, runtimeReason)
	}
	// Image preflight: every execution chain ensures the image exists (and is
	// allowlisted) before creating a container. This turns the previous opaque
	// "Unable to find image" 500 into an actionable missing requirement.
	if info, ensureErr := a.Sandbox.EnsureImage(ctx, sandboxImage); ensureErr != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("runtime image preparation failed: %v", ensureErr)
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["missing_requirements"] = []models.MissingRequirement{{
			Kind:        "image_pull_failed",
			Name:        sandboxImage,
			Detail:      ensureErr.Error(),
			Remediation: "检查 SANDBOX_REGISTRY_MIRROR / 网络连通性，或改用 allowlist 中已预装科学栈的镜像。",
		}}
		logToContext(ctx, "[%s] image preflight failed for %s: %v", a.Name, sandboxImage, ensureErr)
		return ensureErr
	} else if info != nil {
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["runtime_image_digest"] = info.Digest
		task.Metadata["runtime_image_size_bytes"] = info.SizeBytes
	}
	sandboxOptions := sandboxOptionsForTask(task)
	sandboxID, err := a.Sandbox.CreatePersistentSandboxWithOptions(ctx, sandboxIdentityForTask(task), sandboxImage, mountPath, sandboxOptions)
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("failed to create runtime sandbox: %v", err)
		return err
	}
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["sandbox_network_mode"] = sandboxOptions.NetworkMode
	task.Result = sandboxID
	logToContext(ctx, "[%s] Runtime ready: %s", a.Name, sandboxID)
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["runtime_image"] = sandboxImage
	task.Metadata["runtime_image_reason"] = runtimeReason
	task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
		"runtime_session": sandboxID,
		"runtime_image":   sandboxImage,
	})
	task.Status = models.StatusCompleted
	return nil
}

func (a *CoderAgent) completeWithoutDependencies(task *models.Task, runtimeSession, reason string) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	if strings.TrimSpace(reason) == "" {
		reason = "no external dependencies detected"
	}
	task.Result = reason
	task.Status = models.StatusCompleted
	task.Error = ""
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
		"prepared_runtime":          runtimeSession,
		"dependency_install_report": reason,
	})
	return nil
}

func (a *CoderAgent) installDependencies(ctx context.Context, task *models.Task) error {
	runtimeSession := chooseNonEmpty(extractTaskInputLike(task, "runtime_session"), extractTaskInputLike(task, "runtime_env"))
	if strings.TrimSpace(runtimeSession) == "" {
		task.Status = models.StatusFailed
		task.Error = "missing runtime_session input for dependency installation"
		return fmt.Errorf("%s", task.Error)
	}

	// 用于在需要时重建沙箱（例如依赖声明 Requires-Python>=3.10/3.11）
	// 说明：mountPath 允许把 repo/workspace 挂回同一个执行环境，避免后续 execute_code 找不到代码文件。
	mountPath := strings.TrimSpace(extractTaskInputLike(task, "workspace_path"))

	rawDependencySpec := extractTaskInputLike(task, "dependency_spec")
	dependencies, parseErr := parseDependencySpec(rawDependencySpec)
	if parseErr != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("invalid dependency_spec: %v", parseErr)
		task.Result = strings.TrimSpace(rawDependencySpec)
		return fmt.Errorf("%s", task.Error)
	}
	if len(dependencies) == 0 {
		if spec, specErr := runtimeSpecFromTask(task); specErr == nil {
			dependencies = dependencySpecsFromSpec(spec)
		}
	}
	if len(dependencies) == 0 {
		return a.completeWithoutDependencies(task, runtimeSession, "no external dependencies detected")
	}

	dependencies = filterFrameworkBenchmarkDependencies(task, dependencies)
	dependencies = normalizeDependenciesForPython39(dependencies)
	dependencies = filterFrameworkBenchmarkDependencies(task, dependencies)
	dependencies = filterStandardLibraryDependencies(dependencies)
	if len(dependencies) == 0 {
		return a.completeWithoutDependencies(task, runtimeSession, "no external dependencies detected")
	}

	// Dependency installation is the one step that legitimately needs egress.
	// The runtime action approval already happened before this task started, so
	// open the sandbox network for the install and always close it afterwards.
	closeNetwork, networkErr := openSandboxNetwork(ctx, a.Sandbox, runtimeSession, "bridge")
	if networkErr != nil {
		task.Status = models.StatusFailed
		task.Error = fmt.Sprintf("dependency installation needs network, but enabling it failed: %v", networkErr)
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["missing_requirements"] = []models.MissingRequirement{{
			Kind:        "network_control_failed",
			Name:        runtimeSession,
			Detail:      networkErr.Error(),
			Remediation: "检查 docker-sandbox 的网络策略（SANDBOX_ALLOW_NETWORK）与服务连通性后重试。",
		}}
		return fmt.Errorf("%s", task.Error)
	}
	defer closeNetwork()

	// ReAct 依赖恢复：优先让模型根据 pip 日志给出结构化动作，
	// 再由规则兜底，避免把“重试”做成纯盲试。
	maxAttempts := 3
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		logToContext(ctx, "[%s] Installing dependencies in sandbox %s (attempt %d/%d): %s", a.Name, runtimeSession, attempt, maxAttempts, strings.Join(dependencies, ", "))
		cmd, argsErr := pipInstallArgs(dependencies...)
		if argsErr != nil {
			task.Status = models.StatusFailed
			task.Error = argsErr.Error()
			return argsErr
		}
		execStarted := time.Now()
		res, err := a.Sandbox.ExecCommandStream(ctx, runtimeSession, cmd, func(stream string, line string) {
			logToContext(ctx, "[%s] pip %s: %s", a.Name, stream, line)
		})
		recordSandboxDuration(ctx, execStarted)
		if err != nil {
			task.Status = models.StatusFailed
			task.Error = fmt.Sprintf("dependency installation failed: %v", err)
			return err
		}
		if res == nil {
			task.Status = models.StatusFailed
			task.Error = "dependency installation returned nil response"
			return fmt.Errorf("%s", task.Error)
		}

		logToContext(ctx, "[%s] pip install exit_code=%d", a.Name, res.ExitCode)
		if res.ExitCode == 0 {
			report := strings.TrimSpace(res.Stdout)
			if report == "" {
				report = "dependencies installed successfully"
			}
			task.Result = report
			task.Status = models.StatusCompleted
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["artifact_values"] = buildArtifactValueMap(task, map[string]string{
				"prepared_runtime":          runtimeSession,
				"dependency_install_report": report,
			})
			return nil
		}

		stdout := strings.TrimSpace(res.Stdout)
		stderr := strings.TrimSpace(res.Stderr)
		errText := chooseNonEmpty(stderr, stdout, fmt.Sprintf("pip install exited with code %d", res.ExitCode))

		// 没有下一次尝试了，直接失败返回
		if attempt >= maxAttempts {
			task.Status = models.StatusFailed
			task.Result = stdout
			task.Error = errText
			return fmt.Errorf("%s", task.Error)
		}

		// 第一优先级：模型基于 pip 错误做一次结构化 ReAct 决策。
		if plan, reactErr := a.planDependencyRecovery(ctx, dependencies, errText); reactErr == nil && plan.Action != "" {
			nextDependencies, nextRuntimeSession, changed, applyErr := a.applyDependencyRecoveryPlan(ctx, task, dependencies, runtimeSession, mountPath, plan)
			if applyErr == nil && changed {
				dependencies = nextDependencies
				runtimeSession = nextRuntimeSession
				logToContext(ctx, "[%s] ReAct repair applied: action=%s, reason=%s", a.Name, plan.Action, plan.Reason)
				if len(dependencies) == 0 {
					return a.completeWithoutDependencies(task, runtimeSession, "no external dependencies detected after removing packages")
				}
				continue
			}
			if applyErr != nil {
				logToContext(ctx, "[%s] ReAct repair plan apply failed, fallback to rules: %v", a.Name, applyErr)
			} else {
				logToContext(ctx, "[%s] ReAct repair returned no effective change, fallback to rules.", a.Name)
			}
		}

		// 第二优先级：规则兜底，覆盖已知稳定场景。
		nextDependencies, nextRuntimeSession, changed, fallbackReason, fallbackErr := a.applyRuleBasedDependencyFallback(ctx, task, dependencies, runtimeSession, mountPath, errText)
		if fallbackErr != nil {
			task.Status = models.StatusFailed
			task.Result = stdout
			task.Error = fallbackErr.Error()
			return fallbackErr
		}
		if changed {
			dependencies = nextDependencies
			runtimeSession = nextRuntimeSession
			logToContext(ctx, "[%s] Rule-based dependency fallback applied: %s", a.Name, fallbackReason)
			if len(dependencies) == 0 {
				return a.completeWithoutDependencies(task, runtimeSession, "no external dependencies detected after removing packages")
			}
			continue
		}

		// 没有命中的自动纠正规则：保留原错误直接返回，避免无限重试
		task.Status = models.StatusFailed
		task.Result = stdout
		task.Error = errText
		return fmt.Errorf("%s", task.Error)
	}

	// 理论上不会到达这里（for 循环内已 return）
	task.Status = models.StatusFailed
	task.Error = "dependency installation failed after retries"
	return fmt.Errorf("%s", task.Error)
}

type dependencyRecoveryPlan struct {
	Action           string   `json:"action"`
	Reason           string   `json:"reason"`
	RemovePackage    string   `json:"remove_package"`
	ReplacePackage   string   `json:"replace_package"`
	WithPackage      string   `json:"with_package"`
	TargetImage      string   `json:"target_image"`
	NextDependencies []string `json:"next_dependencies"`
}

func (a *CoderAgent) planDependencyRecovery(ctx context.Context, dependencies []string, pipError string) (*dependencyRecoveryPlan, error) {
	if a == nil || a.ChatModel == nil {
		return nil, fmt.Errorf("chat model is not configured")
	}

	pipError = truncateToTokens(pipError, agentEnvInt("DEPENDENCY_ERROR_MAX_TOKENS", 8000))
	userPrompt := prompts.DependencyRecoveryUserPrompt(mustJSON(dependencies), pipError)
	msg, err := llmclient.GenerateWithUsage(ctx, a.ChatModel, []*schema.Message{
		{Role: schema.System, Content: prompts.DependencyRecoverySystemPrompt},
		{Role: schema.User, Content: userPrompt},
	})
	if err != nil {
		return nil, err
	}

	cleaned := cleanJSONFence(msg.Content)
	var plan dependencyRecoveryPlan
	if err := json.Unmarshal([]byte(cleaned), &plan); err != nil {
		return nil, fmt.Errorf("parse dependency recovery plan failed: %w", err)
	}
	plan.Action = strings.TrimSpace(plan.Action)
	plan.Reason = strings.TrimSpace(plan.Reason)
	return &plan, nil
}

func (a *CoderAgent) applyDependencyRecoveryPlan(ctx context.Context, task *models.Task, dependencies []string, runtimeSession string, mountPath string, plan *dependencyRecoveryPlan) ([]string, string, bool, error) {
	if plan == nil {
		return dependencies, runtimeSession, false, nil
	}

	switch strings.TrimSpace(plan.Action) {
	case "remove_package":
		name := strings.TrimSpace(plan.RemovePackage)
		if name == "" {
			return dependencies, runtimeSession, false, fmt.Errorf("react remove_package plan missing package name")
		}
		next := dropDependencyByRoot(dependencies, name)
		if sameStringSlice(next, dependencies) {
			return dependencies, runtimeSession, false, nil
		}
		return next, runtimeSession, true, nil
	case "replace_package":
		from := strings.TrimSpace(plan.ReplacePackage)
		to := strings.TrimSpace(plan.WithPackage)
		if from == "" || to == "" {
			return dependencies, runtimeSession, false, fmt.Errorf("react replace_package plan missing fields")
		}
		next := replaceDependencyByRoot(dependencies, from, to)
		if sameStringSlice(next, dependencies) {
			return dependencies, runtimeSession, false, nil
		}
		return next, runtimeSession, true, nil
	case "rewrite_dependencies":
		next := filterStandardLibraryDependencies(normalizeDependenciesForPython39(uniqueDependencies(plan.NextDependencies)))
		if len(next) == 0 {
			return dependencies, runtimeSession, false, fmt.Errorf("react rewrite_dependencies plan produced empty dependency list")
		}
		if sameStringSlice(next, dependencies) {
			return dependencies, runtimeSession, false, nil
		}
		return next, runtimeSession, true, nil
	case "upgrade_python":
		targetImage := strings.TrimSpace(plan.TargetImage)
		if targetImage == "" {
			targetImage = choosePythonUpgradeImage(plan.Reason)
		}
		nextRuntimeSession, err := a.recreateSandboxForDependencies(ctx, task, runtimeSession, mountPath, targetImage)
		if err != nil {
			return dependencies, runtimeSession, false, err
		}
		return dependencies, nextRuntimeSession, true, nil
	case "abort", "":
		return dependencies, runtimeSession, false, nil
	default:
		return dependencies, runtimeSession, false, fmt.Errorf("unknown react action %q", plan.Action)
	}
}

func (a *CoderAgent) applyRuleBasedDependencyFallback(ctx context.Context, task *models.Task, dependencies []string, runtimeSession string, mountPath string, errText string) ([]string, string, bool, string, error) {
	if pipErrorIndicatesPythonVersionMismatch(errText) {
		targetImage := choosePythonUpgradeImage(errText)
		nextRuntimeSession, err := a.recreateSandboxForDependencies(ctx, task, runtimeSession, mountPath, targetImage)
		if err != nil {
			return dependencies, runtimeSession, false, "", fmt.Errorf("%s; additionally failed to create upgraded sandbox %q: %v", errText, targetImage, err)
		}
		return dependencies, nextRuntimeSession, true, "upgrade python sandbox after Requires-Python mismatch", nil
	}

	if missing, ok := pipErrorMissingRequirement(errText); ok {
		// 如果 missing 是标准库模块，说明依赖识别链路还漏了一层清洗，直接删掉再试。
		if len(filterStandardLibraryDependencies([]string{missing})) == 0 {
			nextDependencies := dropDependencyByRoot(dependencies, missing)
			if !sameStringSlice(nextDependencies, dependencies) {
				return nextDependencies, runtimeSession, true, fmt.Sprintf("remove stdlib module %q from dependency list", missing), nil
			}
		}
	}

	return dependencies, runtimeSession, false, "", nil
}

func (a *CoderAgent) recreateSandboxForDependencies(ctx context.Context, task *models.Task, runtimeSession string, mountPath string, targetImage string) (string, error) {
	if a == nil || a.Sandbox == nil {
		return "", fmt.Errorf("sandbox client is not configured")
	}
	newSandboxID, err := a.Sandbox.CreatePersistentSandboxWithOptions(ctx, sandboxIdentityForTask(task), targetImage, mountPath, sandboxOptionsForTask(task))
	if err != nil {
		return "", err
	}
	logToContext(ctx, "[%s] Switching sandbox %s -> %s (%s) for dependency retry.", a.Name, runtimeSession, newSandboxID, targetImage)
	_ = a.Sandbox.CleanupSandbox(context.Background(), runtimeSession)
	return newSandboxID, nil
}
