package agent

import (
	"errors"
	"os"
	"strings"

	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
)

// promptContextV2Enabled controls the single-assembly prompt path. The legacy
// path remains available for A/B comparison and rollback.
func promptContextV2Enabled() bool {
	raw := strings.TrimSpace(os.Getenv("PROMPT_CONTEXT_V2"))
	if raw == "" {
		return true
	}
	return !strings.EqualFold(raw, "false") && raw != "0"
}

func promptBudgetFromShared(sharedContext map[string]interface{}) prompts.PromptBudget {
	if sharedContext != nil {
		switch value := sharedContext["prompt_budget"].(type) {
		case prompts.PromptBudget:
			return value
		case map[string]any:
			return prompts.PromptBudget{
				ContextWindow: intValue(value["context_window"]),
				MaxOutput:     intValue(value["max_output"]),
				Safety:        intValue(value["safety"]),
			}
		}
	}
	return prompts.DefaultPromptBudget()
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	}
	return 0
}

// buildAgentPromptText is the single entry point used by the four task agents.
// It returns the user prompt plus the budget report. In legacy mode it
// reproduces the pre-v2 concatenation.
func buildAgentPromptText(sharedContext map[string]interface{}, task *models.Task, systemPrompt, volatile string, legacyLimit int) (string, prompts.PromptReport, error) {
	if task == nil {
		return "", prompts.PromptReport{}, nil
	}
	return buildAgentPromptTextWithDescription(sharedContext, task, systemPrompt, task.Description, volatile, legacyLimit)
}

// buildAgentPromptTextWithDescription is buildAgentPromptText with an explicit
// task description override (for example the coder's plot instructions).
func buildAgentPromptTextWithDescription(sharedContext map[string]interface{}, task *models.Task, systemPrompt, description, volatile string, legacyLimit int) (string, prompts.PromptReport, error) {
	if task == nil {
		return "", prompts.PromptReport{}, nil
	}
	if !promptContextV2Enabled() {
		return prompts.BuildLegacyTaskPrompt(description, task.Inputs, legacyLimit), prompts.PromptReport{}, nil
	}
	planContext := ""
	var plan *models.PlanGraph
	if sharedContext != nil {
		if value, ok := sharedContext["plan_context"].(string); ok {
			planContext = value
		}
		if value, ok := sharedContext["plan_graph"].(*models.PlanGraph); ok {
			plan = value
		}
	}
	assembled, err := prompts.BuildTaskPrompt(prompts.TaskPromptInput{
		System:          systemPrompt,
		Plan:            plan,
		PlanContextText: planContext,
		TaskName:        task.Name,
		TaskType:        task.Type,
		TaskDescription: description,
		OutputArtifacts: task.OutputArtifacts,
		Inputs:          task.Inputs,
		Volatile:        volatile,
		Budget:          promptBudgetFromShared(sharedContext),
	})
	if err != nil {
		if errors.Is(err, prompts.ErrPromptBudgetExceeded) {
			metrics.RecordPromptOverBudget("default")
		}
		return "", prompts.PromptReport{}, err
	}
	return assembled.User, assembled.Report, nil
}

// recordPromptReport stores the budget report on task metadata so the API can
// show which materials were truncated.
func recordPromptReport(task *models.Task, report prompts.PromptReport) {
	if task == nil || len(report.Sections) == 0 {
		return
	}
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["prompt_budget"] = report
	if truncated := report.TruncatedSections(); len(truncated) > 0 {
		task.Metadata["prompt_truncated_sections"] = truncated
	}
}
