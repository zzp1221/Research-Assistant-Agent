package scheduler

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/google/uuid"
)

// runtimeActionForTask returns the important operation that should be gated
// before the task runs. Only real runtime-affecting tasks are gated.
func runtimeActionForTask(plan *models.PlanGraph, task *models.TaskNode) (models.RuntimeActionKind, string, map[string]any, string, bool) {
	if plan == nil || task == nil {
		return "", "", nil, "", false
	}
	spec := runtimeSpecForPlan(plan)
	switch task.Type {
	case "image_prepare":
		image := strings.TrimSpace(spec.BaseImage)
		if image == "" {
			image = "python:3.11-bullseye"
		}
		return models.RuntimeActionImagePull, runtimeActionTitle(plan, "拉取运行时镜像", "拉取论文运行时镜像"), map[string]any{
			"image":    image,
			"registry": imageRegistry(image),
		}, "medium", true
	case "install_dependencies":
		deps := dependencySpecForPlan(plan, spec)
		if len(deps) == 0 {
			return "", "", nil, "", false
		}
		return models.RuntimeActionDependencyInstall, runtimeActionTitle(plan, "安装运行依赖", "安装论文依赖"), map[string]any{
			"packages": deps,
		}, "medium", true
	case "runtime_probe":
		if spec.GPURequired {
			return models.RuntimeActionGPUUse, "使用 GPU 运行论文实验", map[string]any{
				"gpu_type": spec.GPUType,
				"cuda":     spec.CUDAVersion,
			}, "high", true
		}
		if len(spec.DatasetRefs) > 0 {
			return models.RuntimeActionDatasetDownload, "下载论文数据集", map[string]any{
				"datasets": spec.DatasetRefs,
			}, "medium", true
		}
		if len(spec.ModelRefs) > 0 {
			return models.RuntimeActionModelDownload, "下载论文模型权重", map[string]any{
				"models": spec.ModelRefs,
			}, "high", true
		}
	case "paper_code_execute", "fix_and_rerun", "benchmark_execute",
		"benchmark_adapter_preflight", "benchmark_split_materialize", "benchmark_dataset_audit",
		"autoresearch_run", "autoresearch_validate", "experiment_run", "experiment_dataset_prepare",
		"framework_run_left", "framework_run_right":
		// Execution tasks may fetch datasets, model weights or repositories.
		// Request an explicit network decision in confirm mode; full_access
		// auto-approves it. Simple offline code execution is not gated.
		details := map[string]any{"network_access": true, "task_type": task.Type}
		if len(spec.DatasetRefs) > 0 {
			details["datasets"] = spec.DatasetRefs
		}
		if len(spec.ModelRefs) > 0 {
			details["models"] = spec.ModelRefs
		}
		return models.RuntimeActionNetworkAccess, "运行阶段访问网络", details, "medium", true
	}
	return "", "", nil, "", false
}

// runtimeActionTitle keeps the approval copy honest: a plotting plan is not a
// paper reproduction, so its dependency card must not claim to install paper
// dependencies.
func runtimeActionTitle(plan *models.PlanGraph, generic, paper string) string {
	if plan == nil {
		return generic
	}
	intentType := strings.ToLower(strings.TrimSpace(plan.IntentType))
	if strings.Contains(intentType, "paper") || strings.Contains(intentType, "reproduction") {
		return paper
	}
	return generic
}

func runtimeSpecForPlan(plan *models.PlanGraph) models.ReproductionRuntimeSpec {
	var spec models.ReproductionRuntimeSpec
	if plan == nil {
		return spec
	}
	artifact, ok := plan.Artifacts["reproduction_runtime_spec"]
	if !ok {
		return spec
	}
	_ = json.Unmarshal([]byte(artifact.EffectiveValue()), &spec)
	return spec
}

func dependencySpecForPlan(plan *models.PlanGraph, spec models.ReproductionRuntimeSpec) []string {
	if plan != nil {
		if artifact, ok := plan.Artifacts["dependency_spec"]; ok {
			var deps []string
			if json.Unmarshal([]byte(artifact.EffectiveValue()), &deps) == nil && len(deps) > 0 {
				return deps
			}
		}
	}
	deps := make([]string, 0, len(spec.PythonDependencies))
	for _, dep := range spec.PythonDependencies {
		if strings.TrimSpace(dep.Version) != "" {
			deps = append(deps, dep.Name+"=="+dep.Version)
		} else if strings.TrimSpace(dep.Name) != "" {
			deps = append(deps, dep.Name)
		}
	}
	return deps
}

func imageRegistry(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if !strings.Contains(image, "/") {
		return "docker.io"
	}
	first := strings.Split(image, "/")[0]
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return "docker.io"
}

// prepareRuntimeGate is called by the scheduler before a task starts. It
// persists either a pending approval request or an auto-approved audit action.
func (s *Scheduler) prepareRuntimeGate(planID, taskID string) (*models.RuntimeActionRequest, bool, error) {
	var action *models.RuntimeActionRequest
	pending := false
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		kind, title, details, risk, needed := runtimeActionForTask(plan, task)
		if !needed {
			return nil
		}
		if existing := findRuntimeAction(plan, taskID, kind); existing != nil {
			switch existing.Status {
			case models.RuntimeActionPending:
				task.Status = models.StatusAwaitingApproval
				plan.Status = models.StatusAwaitingApproval
				action = existing
				pending = true
				return nil
			case models.RuntimeActionApproved:
				action = existing
				return nil
			case models.RuntimeActionRejected:
				task.Status = models.StatusBlocked
				task.Error = "runtime action rejected by user: " + string(kind)
				plan.Status = models.StatusFailed
				plan.Outcome = models.OutcomeBlocked
				plan.MissingRequirements = append(plan.MissingRequirements, models.MissingRequirement{
					Kind: "user_rejected", Name: string(kind), Detail: task.Error, Remediation: "重新运行并允许该操作。",
				})
				action = existing
				return nil
			}
		}
		now := time.Now()
		action = &models.RuntimeActionRequest{
			ID:          "ra-" + uuid.NewString(),
			PlanID:      plan.ID,
			TaskID:      taskID,
			NodeType:    task.Type,
			Kind:        kind,
			Title:       title,
			Details:     details,
			Risk:        risk,
			Status:      models.RuntimeActionPending,
			RequestedAt: now,
		}
		// One approved network decision covers the rest of the plan so users are
		// not prompted once per execution node.
		if kind == models.RuntimeActionNetworkAccess && planHasApprovedNetworkAccess(plan) {
			action.Status = models.RuntimeActionApproved
			action.AutoApproved = true
			action.DecidedAt = &now
			action.DecidedBy = "prior_approval"
		}
		plan.RuntimeActions = append(plan.RuntimeActions, *action)
		task.RuntimeActionIDs = append(task.RuntimeActionIDs, action.ID)
		if plan.RuntimePolicy.Mode == models.RuntimePermissionFullAccess {
			action.Status = models.RuntimeActionApproved
			action.AutoApproved = true
			action.DecidedAt = &now
			action.DecidedBy = "policy:full_access"
			plan.RuntimeActions[len(plan.RuntimeActions)-1] = *action
			return nil
		}
		if action.Status == models.RuntimeActionApproved && action.AutoApproved {
			// Covered by a previous explicit approval for this plan.
			return nil
		}
		task.Status = models.StatusAwaitingApproval
		plan.Status = models.StatusAwaitingApproval
		pending = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if action != nil {
		if action.Status == models.RuntimeActionApproved && !action.AutoApproved {
			return action, pending, nil
		}
		eventType := "runtime_action_requested"
		if action.Status == models.RuntimeActionApproved {
			eventType = "runtime_action_resolved"
		}
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:     planID,
			TaskID:     taskID,
			EventType:  eventType,
			TaskStatus: string(models.StatusAwaitingApproval),
			Payload: map[string]any{
				"action":        action,
				"auto_approved": action.AutoApproved,
			},
			Timestamp: time.Now(),
		})
	}
	return action, pending, nil
}

// planHasApprovedNetworkAccess reports whether the user already approved a
// network action for this plan.
func planHasApprovedNetworkAccess(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	for _, action := range plan.RuntimeActions {
		if action.Kind == models.RuntimeActionNetworkAccess && action.Status == models.RuntimeActionApproved && !action.AutoApproved {
			return true
		}
	}
	return false
}

func findRuntimeAction(plan *models.PlanGraph, taskID string, kind models.RuntimeActionKind) *models.RuntimeActionRequest {
	if plan == nil {
		return nil
	}
	for i := range plan.RuntimeActions {
		action := &plan.RuntimeActions[i]
		if action.TaskID == taskID && action.Kind == kind {
			return action
		}
	}
	return nil
}

func hasAwaitingApproval(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusAwaitingApproval {
			return true
		}
	}
	return false
}
