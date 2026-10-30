package agent

import (
	"context"
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

func (a *CoderAgent) runPythonTaskInSandbox(ctx context.Context, sandboxID string, workspacePath string, codeFilePath string, code string) (*sandbox.PythonRunResponse, error) {
	if strings.TrimSpace(workspacePath) != "" && strings.TrimSpace(codeFilePath) != "" {
		relPath, relErr := filepath.Rel(workspacePath, codeFilePath)
		if relErr == nil && !strings.HasPrefix(relPath, "..") {
			cmd := []string{"bash", "-lc", buildMountedWorkspacePythonCommand(relPath)}
			logToContext(ctx, "[%s] Executing repo entry file in mounted workspace: %s", a.Name, relPath)
			started := time.Now()
			response, err := a.Sandbox.ExecCommandStream(ctx, sandboxID, cmd, func(stream string, line string) {
				logToContext(ctx, "[%s] python %s: %s", a.Name, stream, line)
			})
			recordSandboxDuration(ctx, started)
			return response, err
		}
	}

	if err := a.validatePythonSyntaxInSandbox(ctx, sandboxID, code); err != nil {
		return nil, err
	}
	started := time.Now()
	response, err := a.Sandbox.RunPythonCodeStream(ctx, sandboxID, code, func(stream string, line string) {
		logToContext(ctx, "[%s] python %s: %s", a.Name, stream, line)
	})
	recordSandboxDuration(ctx, started)
	return response, err
}

func (a *CoderAgent) installMissingPythonModule(ctx context.Context, sandboxID string, errText string) (bool, error) {
	missingModule := extractMissingPythonModule(errText)
	if missingModule == "" || isPythonStandardLibraryModule(missingModule) {
		return false, nil
	}

	pkg := mapPythonImportToPackage(missingModule)
	if pkg == "" || isPythonStandardLibraryModule(pkg) {
		return false, nil
	}

	// 运行期兜底：依赖识别漏掉模块时，在当前 prepared_runtime 内最小补装一次。
	logToContext(ctx, "[%s] Missing module detected at runtime: %s -> %s, retrying with pip install", a.Name, missingModule, pkg)
	pipArgs, pipArgsErr := pipInstallArgs(pkg)
	if pipArgsErr != nil {
		return false, pipArgsErr
	}
	closeNetwork, networkErr := openSandboxNetwork(ctx, a.Sandbox, sandboxID, "bridge")
	if networkErr != nil {
		return false, networkErr
	}
	defer closeNetwork()
	execStarted := time.Now()
	res, err := a.Sandbox.ExecCommandStream(ctx, sandboxID, pipArgs, func(stream string, line string) {
		logToContext(ctx, "[%s] pip %s: %s", a.Name, stream, line)
	})
	recordSandboxDuration(ctx, execStarted)
	if err != nil {
		return false, err
	}
	if res == nil {
		return false, fmt.Errorf("pip install returned nil response while installing %s", pkg)
	}
	if res.ExitCode != 0 {
		return false, fmt.Errorf("%s", chooseNonEmpty(strings.TrimSpace(res.Stderr), strings.TrimSpace(res.Stdout), fmt.Sprintf("pip install %s exited with code %d", pkg, res.ExitCode)))
	}
	return true, nil
}

func (a *CoderAgent) executeCodeInSandbox(ctx context.Context, task *models.Task) error {
	workspacePath := strings.TrimSpace(extractTaskInputLike(task, "workspace_path"))
	codeFilePath := strings.TrimSpace(extractTaskInputLike(task, "code_file_path"))
	code := extractTaskInputLike(task, "generated_code")
	if strings.TrimSpace(code) == "" && strings.TrimSpace(codeFilePath) == "" {
		task.Status = models.StatusFailed
		task.Error = "missing generated_code/code_file_path input for sandbox execution"
		return fmt.Errorf("%s", task.Error)
	}

	runtimeSession := chooseNonEmpty(extractTaskInputLike(task, "prepared_runtime"), extractTaskInputLike(task, "runtime_session"), extractTaskInputLike(task, "runtime_env"))
	sandboxID := runtimeSession
	ownsEphemeralSandbox := false
	if strings.TrimSpace(sandboxID) == "" || (!strings.HasPrefix(sandboxID, "dk-") && !strings.HasPrefix(sandboxID, "os-")) {
		if strings.TrimSpace(runtimeSession) != "" {
			task.Status = models.StatusFailed
			task.Error = "invalid prepared_runtime artifact: expected sandbox id"
			task.Result = strings.TrimSpace(runtimeSession)
			return fmt.Errorf("%s", task.Error)
		}
		image := sandboxID
		if strings.TrimSpace(image) == "" {
			image = configuredSandboxImage()
		}
		logToContext(ctx, "[%s] Running %s in sandbox image %s", a.Name, task.Name, image)
		var err error
		sandboxID, err = a.Sandbox.CreatePersistentSandboxWithOptions(ctx, sandboxIdentityForTask(task), image, "", sandboxOptionsForTask(task))
		if err != nil {
			task.Status = models.StatusFailed
			task.Error = fmt.Sprintf("failed to create execution sandbox: %v", err)
			return err
		}
		ownsEphemeralSandbox = true
	}
	if ownsEphemeralSandbox {
		defer a.Sandbox.CleanupSandbox(context.Background(), sandboxID)
	}

	// Network egress for code execution is only opened when the plan approved a
	// network_access runtime action; otherwise the sandbox stays offline.
	if taskNeedsNetwork(task) {
		closeNetwork, networkErr := openSandboxNetwork(ctx, a.Sandbox, sandboxID, "bridge")
		if networkErr != nil {
			task.Status = models.StatusFailed
			task.Error = fmt.Sprintf("execution needs network, but enabling it failed: %v", networkErr)
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["missing_requirements"] = []models.MissingRequirement{{
				Kind:        "network_control_failed",
				Name:        sandboxID,
				Detail:      networkErr.Error(),
				Remediation: "检查 docker-sandbox 网络策略后重试。",
			}}
			return fmt.Errorf("%s", task.Error)
		}
		defer closeNetwork()
	}

	executesMountedFile := strings.TrimSpace(workspacePath) != "" && strings.TrimSpace(codeFilePath) != ""
	codeRepaired := false
	// 缺模块恢复改为循环：最多补装 3 个模块（2 轮内完成），避免"装完 numpy
	// 又缺 matplotlib"时只恢复一次就放弃。
	const maxModuleRecoveries = 3
	moduleRecoveries := 0
	installedModules := make([]string, 0, maxModuleRecoveries)
	var (
		res *sandbox.PythonRunResponse
		err error
	)
	for {
		res, err = a.runPythonTaskInSandbox(ctx, sandboxID, workspacePath, codeFilePath, code)
		if err != nil {
			errText := err.Error()
			if !executesMountedFile && !codeRepaired {
				if repairedCode, repaired, repairErr := a.repairGeneratedPythonCodeForRuntimeError(ctx, code, errText); repairErr == nil && repaired {
					code = repairedCode
					codeRepaired = true
					logToContext(ctx, "[%s] runtime code repair applied after validation error, rerunning generated Python code", a.Name)
					continue
				} else if repairErr != nil {
					logToContext(ctx, "[%s] runtime code repair after validation error failed: %v", a.Name, repairErr)
				}
			}
			task.Status = models.StatusFailed
			task.Error = fmt.Sprintf("sandbox execution failed: %v", err)
			return err
		}
		if res == nil {
			task.Status = models.StatusFailed
			task.Error = "sandbox execution returned nil response"
			return fmt.Errorf("%s", task.Error)
		}
		if res.ExitCode == 0 {
			break
		}

		errText := strings.TrimSpace(res.Stderr)
		if errText == "" {
			errText = fmt.Sprintf("sandbox exited with code %d", res.ExitCode)
		}

		// 第一层兜底：模块缺失时优先在当前运行时最小补装，再重跑；循环直到
		// 补齐所有缺失模块或达到上限。
		if moduleRecoveries < maxModuleRecoveries {
			if retried, installErr := a.installMissingPythonModule(ctx, sandboxID, errText); installErr == nil && retried {
				moduleRecoveries++
				installedModules = append(installedModules, extractMissingPythonModule(errText))
				logToContext(ctx, "[%s] runtime missing-module recovery %d/%d applied for %s", a.Name, moduleRecoveries, maxModuleRecoveries, extractMissingPythonModule(errText))
				continue
			} else if installErr != nil {
				logToContext(ctx, "[%s] runtime missing-module recovery failed: %v", a.Name, installErr)
			}
		}

		// 第二层兜底：仅对直接执行的生成代码做一次 API 兼容修复，避免改动挂载仓库源码。
		if !executesMountedFile && !codeRepaired {
			if repairedCode, repaired, repairErr := a.repairGeneratedPythonCodeForRuntimeError(ctx, code, errText); repairErr == nil && repaired {
				code = repairedCode
				codeRepaired = true
				logToContext(ctx, "[%s] runtime code repair applied, rerunning generated Python code", a.Name)
				continue
			} else if repairErr != nil {
				logToContext(ctx, "[%s] runtime code repair failed: %v", a.Name, repairErr)
			}
		}
		break
	}
	if res.ExitCode != 0 {
		task.Status = models.StatusFailed
		task.Result = strings.TrimSpace(res.Stdout)
		task.Error = strings.TrimSpace(res.Stderr)
		if task.Error == "" {
			task.Error = fmt.Sprintf("sandbox exited with code %d", res.ExitCode)
		}
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		if missingModule := extractMissingPythonModule(task.Error); missingModule != "" {
			task.Metadata["missing_requirements"] = []models.MissingRequirement{{
				Kind:        "dependency_missing",
				Name:        missingModule,
				Detail:      fmt.Sprintf("模块 %s 在运行时缺失；已自动补装 %d 个模块仍未解决", missingModule, len(installedModules)),
				Remediation: "把该模块加入 dependency_spec 或在预装科学栈镜像中提供。",
			}}
		}
		logToContext(ctx, "[%s] sandbox run exit_code=%d", a.Name, res.ExitCode)
		return fmt.Errorf("%s", task.Error)
	}

	logToContext(ctx, "[%s] sandbox run exit_code=%d", a.Name, res.ExitCode)
	if len(res.Images) > 0 {
		logToContext(ctx, "[%s] sandbox produced %d image artifact(s)", a.Name, len(res.Images))
	}
	task.Code = code
	task.Result = strings.TrimSpace(res.Stdout)
	if task.Result == "" {
		task.Result = strings.TrimSpace(res.Stderr)
	}
	if len(res.Images) > 0 {
		task.ImageBase64 = res.Images[0]
	}
	task.Status = models.StatusCompleted
	return nil
}
