package agent

import (
	"context"
	"fmt"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
)

type researchCodingSandbox interface {
	ExecCommandStream(ctx context.Context, sandboxID string, cmd []string, onChunk func(stream string, line string)) (*sandbox.PythonRunResponse, error)
}

// ResearchCodingAgent is the repository-aware coding sub-agent. It owns bounded
// benchmark adaptation and paper-code debugging while reusing CoderAgent for
// low-level model and sandbox capabilities.
type ResearchCodingAgent struct {
	Name      string
	ChatModel *openaiModel.ChatModel
	Sandbox   researchCodingSandbox
	Optimizer researchOptimizer
}

func NewResearchCodingAgent(coder *CoderAgent) *ResearchCodingAgent {
	agent := &ResearchCodingAgent{Name: "research_coding_agent", Optimizer: newHTTPResearchOptimizerFromEnv()}
	if coder != nil {
		agent.ChatModel = coder.ChatModel
		agent.Sandbox = coder.Sandbox
	}
	return agent
}

func (a *ResearchCodingAgent) ExecuteTask(ctx context.Context, task *models.Task, sharedContext map[string]interface{}) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	logToContext(ctx, "[%s] executing %s", a.Name, task.Type)
	switch task.Type {
	case "experiment_dataset_prepare":
		return a.withTaskNetwork(ctx, task, func() error { return a.executeExperimentDatasetPrepare(ctx, task) })
	case "experiment_spec":
		return a.executeExperimentSpec(ctx, task)
	case "experiment_run":
		return a.withTaskNetwork(ctx, task, func() error { return a.executeExperimentRun(ctx, task) })
	case "experiment_validate":
		return a.executeExperimentValidation(ctx, task)
	case "dataset_profile":
		return a.executeDatasetProfile(ctx, task)
	case "benchmark_adapter_generate":
		return a.executeAdapterGeneration(ctx, task)
	case "benchmark_adapter_preflight":
		return a.withTaskNetwork(ctx, task, func() error { return a.executeAdapterPreflight(ctx, task) })
	case "benchmark_execute":
		return a.withTaskNetwork(ctx, task, func() error { return a.executeBenchmark(ctx, task) })
	case "benchmark_validate":
		return a.executeBenchmarkValidation(ctx, task)
	case "autoresearch_spec":
		return a.executeAutoResearchSpec(ctx, task)
	case "autoresearch_run":
		return a.withTaskNetwork(ctx, task, func() error { return a.executeAutoResearchRun(ctx, task) })
	case "autoresearch_validate":
		return a.withTaskNetwork(ctx, task, func() error { return a.executeAutoResearchValidation(ctx, task) })
	case "paper_code_execute", "fix_and_rerun":
		return a.withTaskNetwork(ctx, task, func() error { return a.executePaperCodeTask(ctx, task, sharedContext) })
	case "reproduction_spec":
		return a.executeReproductionSpec(ctx, task)
	default:
		return failResearchCodingTask(task, fmt.Errorf("unsupported research coding task type %q", task.Type))
	}
}

// withTaskNetwork opens sandbox egress for a network-approved execution task and
// always restores the offline state afterwards.
func (a *ResearchCodingAgent) withTaskNetwork(ctx context.Context, task *models.Task, run func() error) error {
	if !taskNeedsNetwork(task) {
		return run()
	}
	session := taskRuntimeSession(task)
	closeNetwork, err := openSandboxNetwork(ctx, a.Sandbox, session, "bridge")
	if err != nil {
		if task.Metadata == nil {
			task.Metadata = map[string]any{}
		}
		task.Metadata["missing_requirements"] = []models.MissingRequirement{{
			Kind:        "network_control_failed",
			Name:        session,
			Detail:      err.Error(),
			Remediation: "检查 docker-sandbox 网络策略（SANDBOX_ALLOW_NETWORK）与服务连通性后重试。",
		}}
		return failResearchCodingTask(task, fmt.Errorf("execution needs network, but enabling it failed: %w", err))
	}
	defer closeNetwork()
	return run()
}

func setResearchCodingArtifacts(task *models.Task, values map[string]string) {
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["artifact_values"] = values
}

func failResearchCodingTask(task *models.Task, err error) error {
	if task != nil {
		task.Status = models.StatusFailed
		task.Error = err.Error()
	}
	return err
}
