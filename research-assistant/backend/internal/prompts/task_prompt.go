package prompts

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"research-assistant-backend/internal/models"
)

// TaskPromptInput is the single input for task prompt assembly. Plan and Task
// are optional so unit tests and lightweight agents can build a prompt from a
// task description alone. PlanContextText lets the scheduler pass an already
// rendered plan context through sharedContext without re-marshalling the plan.
type TaskPromptInput struct {
	System          string
	Plan            *models.PlanGraph
	PlanContextText string
	Task            *models.TaskNode
	TaskName        string
	TaskType        string
	TaskDescription string
	OutputArtifacts []string
	Inputs          map[string]any
	Volatile        string
	Budget          PromptBudget
	Counter         TokenCounter
}

// TaskPrompt is the assembled prompt. System is passed through unchanged so
// agents keep their role-specific system prompt.
type TaskPrompt struct {
	System string
	User   string
	Report PromptReport
}

// PlanContext renders plan-level stable context. It deliberately contains no
// artifact bodies: those belong to task_inputs and must appear exactly once.
func PlanContext(plan *models.PlanGraph) string {
	if plan == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Plan intent type: ")
	builder.WriteString(strings.TrimSpace(plan.IntentType))
	builder.WriteString("\nUser intent:\n")
	builder.WriteString(strings.TrimSpace(plan.UserIntent))
	if plan.Context != nil && plan.Context.Paper != nil {
		if payload, err := json.Marshal(plan.Context.Paper); err == nil {
			builder.WriteString("\n\nPaper metadata:\n")
			builder.WriteString(TruncateRunes(string(payload), 4000))
		}
	}
	if plan.Context != nil && len(plan.Context.Attachments) > 0 {
		builder.WriteString("\n\nAttachments:")
		for _, attachment := range plan.Context.Attachments {
			builder.WriteString(fmt.Sprintf("\n- %s (role=%s, id=%s)", attachment.Name, attachment.Role, attachment.ID))
		}
	}
	return builder.String()
}

// PlanContextForTask renders plan context without repeating content that the
// task description or task inputs already carry. This is what guarantees the
// "user intent appears once / artifact appears once" contract.
func PlanContextForTask(plan *models.PlanGraph, taskDescription string, inputs map[string]any) string {
	text := PlanContext(plan)
	if plan == nil {
		return ""
	}
	intent := strings.TrimSpace(plan.UserIntent)
	if intent != "" && strings.Contains(taskDescription, intent) {
		text = strings.Replace(text, "\nUser intent:\n"+intent, "", 1)
	}
	if _, ok := inputs["paper_metadata"]; ok && plan.Context != nil && plan.Context.Paper != nil {
		if payload, err := json.Marshal(plan.Context.Paper); err == nil {
			text = strings.Replace(text, "\n\nPaper metadata:\n"+TruncateRunes(string(payload), 4000), "", 1)
		}
	}
	if _, ok := inputs["uploaded_files"]; ok {
		if index := strings.Index(text, "\n\nAttachments:"); index >= 0 {
			text = text[:index]
		}
	}
	return strings.TrimSpace(text)
}

// TaskInstructionFor renders the node-local instruction from primitives.
func TaskInstructionFor(name, taskType, description string, outputs []string) string {
	return fmt.Sprintf("Task: %s\nType: %s\nDescription: %s\nExpected outputs: %s",
		name, taskType, strings.TrimSpace(description), strings.Join(outputs, ", "))
}

// TaskInstruction renders the node-local instruction.
func TaskInstruction(task *models.TaskNode) string {
	if task == nil {
		return ""
	}
	return TaskInstructionFor(task.Name, task.Type, task.Description, task.OutputArtifacts)
}

// BuildTaskPrompt assembles system + plan_context + task_instruction +
// task_inputs + volatile. The same artifact appears exactly once, and volatile
// content is truncated first.
func BuildTaskPrompt(in TaskPromptInput) (TaskPrompt, error) {
	counter := counterOr(in.Counter)
	budget := in.Budget
	if budget.PromptLimit() <= 0 {
		budget = DefaultPromptBudget()
	}
	limit := budget.PromptLimit()
	systemTokens := counter.Count(in.System)
	if systemTokens > limit {
		return TaskPrompt{}, fmt.Errorf("%w: system=%d limit=%d", ErrPromptBudgetExceeded, systemTokens, limit)
	}

	description := in.TaskDescription
	if in.Task != nil {
		description = in.Task.Description
	}
	planContext := strings.TrimSpace(in.PlanContextText)
	if in.Plan != nil {
		planContext = PlanContextForTask(in.Plan, description, in.Inputs)
	} else if planContext == "" && in.Plan != nil {
		planContext = PlanContext(in.Plan)
	}
	instruction := ""
	if in.Task != nil {
		instruction = TaskInstruction(in.Task)
	} else if strings.TrimSpace(in.TaskName) != "" || strings.TrimSpace(in.TaskDescription) != "" {
		instruction = TaskInstructionFor(in.TaskName, in.TaskType, in.TaskDescription, in.OutputArtifacts)
	}

	sections := []Section{
		{Name: "plan_context", Content: planContext, Priority: 1},
		{Name: "task_instruction", Content: instruction, Priority: 2},
	}
	if len(in.Inputs) > 0 {
		if inputsText, _ := FormatTaskInputsForPrompt(in.Inputs, limit-systemTokens, counter); strings.TrimSpace(inputsText) != "" {
			sections = append(sections, Section{Name: "task_inputs", Content: inputsText, Priority: 3})
		}
	}
	if strings.TrimSpace(in.Volatile) != "" {
		sections = append(sections, Section{Name: "volatile", Content: in.Volatile, Priority: 5})
	}
	user, report, err := BuildSectionsWithLimit(sections, limit-systemTokens, counter)
	if err != nil {
		return TaskPrompt{}, err
	}
	report.Budget = limit
	report.TotalTokens += systemTokens
	return TaskPrompt{System: in.System, User: user, Report: report}, nil
}

// BuildLegacyTaskPrompt reproduces the pre-PROMPT_CONTEXT_V2 concatenation. It
// exists only for the rollback switch and A/B comparisons.
func BuildLegacyTaskPrompt(description string, inputs map[string]any, perValueLimit int) string {
	if len(inputs) == 0 {
		return strings.TrimSpace(description)
	}
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(description))
	builder.WriteString("\n\n上游输入:\n")
	for _, key := range keys {
		builder.WriteString("- ")
		builder.WriteString(key)
		builder.WriteString(": ")
		builder.WriteString(renderPromptValue(inputs[key], perValueLimit))
		builder.WriteByte('\n')
	}
	return strings.TrimSpace(builder.String())
}
