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
	System            string
	CoreMemory        string
	RetrievedMemory   string
	WikiContext       string
	Plan              *models.PlanGraph
	PlanContextText   string
	Task              *models.TaskNode
	TaskName          string
	TaskType          string
	TaskDescription   string
	OutputArtifacts   []string
	Inputs            map[string]any
	Volatile          string
	Budget            PromptBudget
	Counter           TokenCounter
	PreserveMaterials bool
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
	return renderPlanContext(plan, "", nil, false)
}

// renderPlanContext omits duplicate sections while rendering them, so removing one
// source never removes unrelated material that follows it. Managed callers
// defer metadata capacity checks to the model runtime.
func renderPlanContext(plan *models.PlanGraph, taskDescription string, inputs map[string]any, preserveMaterials bool) string {
	if plan == nil {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Plan intent type: ")
	builder.WriteString(strings.TrimSpace(plan.IntentType))
	intent := strings.TrimSpace(plan.UserIntent)
	if intent == "" || !strings.Contains(taskDescription, intent) {
		builder.WriteString("\nUser intent:\n")
		builder.WriteString(intent)
	}
	_, paperBound := inputs["paper_metadata"]
	if !paperBound && plan.Context != nil && plan.Context.Paper != nil {
		if payload, err := json.Marshal(plan.Context.Paper); err == nil {
			builder.WriteString("\n\nPaper metadata:\n")
			if preserveMaterials {
				builder.Write(payload)
			} else {
				builder.WriteString(TruncateRunes(string(payload), 4000))
			}
		}
	}
	_, attachmentsBound := inputs["uploaded_files"]
	if !attachmentsBound && plan.Context != nil && len(plan.Context.Attachments) > 0 {
		builder.WriteString("\n\nAttachments:")
		for _, attachment := range plan.Context.Attachments {
			builder.WriteString(fmt.Sprintf("\n- %s (role=%s, id=%s)", attachment.Name, attachment.Role, attachment.ID))
		}
	}
	if plan.Context != nil && strings.TrimSpace(plan.Context.ParentPlanSummary) != "" {
		builder.WriteString("\n\nParent plan summary:\n")
		builder.WriteString(strings.TrimSpace(plan.Context.ParentPlanSummary))
	}
	if plan.ParentPlanID != "" {
		builder.WriteString("\nParent plan id: ")
		builder.WriteString(plan.ParentPlanID)
	}
	if plan.Context != nil && strings.TrimSpace(plan.Context.MemoryContext) != "" {
		builder.WriteString("\n\nMemory context:\n")
		builder.WriteString(strings.TrimSpace(plan.Context.MemoryContext))
	}
	return builder.String()
}

// PlanContextForTask renders plan context without repeating content that the
// task description or task inputs already carry. This is what guarantees the
// "user intent appears once / artifact appears once" contract.
func PlanContextForTask(plan *models.PlanGraph, taskDescription string, inputs map[string]any) string {
	return strings.TrimSpace(renderPlanContext(plan, taskDescription, inputs, false))
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
	if budget == (PromptBudget{}) {
		budget = DefaultPromptBudget()
	}
	if err := budget.Validate(); err != nil {
		return TaskPrompt{}, err
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
		planContext = strings.TrimSpace(renderPlanContext(in.Plan, description, in.Inputs, in.PreserveMaterials))
	}
	instruction := ""
	if in.Task != nil {
		instruction = TaskInstruction(in.Task)
	} else if strings.TrimSpace(in.TaskName) != "" || strings.TrimSpace(in.TaskDescription) != "" {
		instruction = TaskInstructionFor(in.TaskName, in.TaskType, in.TaskDescription, in.OutputArtifacts)
	}

	sections := []Section{
		{Name: "core_memory", Content: strings.TrimSpace(in.CoreMemory), Priority: 1},
		{Name: "plan_context", Content: planContext, Priority: 2},
		{Name: "task_instruction", Content: instruction, Priority: 2},
	}
	if retrieved := strings.TrimSpace(in.RetrievedMemory); retrieved != "" {
		sections = append(sections, Section{Name: "retrieved_memory", Content: retrieved, Priority: 3})
	}
	// wiki_context shares priority 3 with retrieved_memory; stable sorting keeps
	// the documented order (retrieved first, wiki second) because it is appended
	// after it.
	if wikiContext := strings.TrimSpace(in.WikiContext); wikiContext != "" {
		sections = append(sections, Section{Name: "wiki_context", Content: wikiContext, Priority: 3})
	}
	if len(in.Inputs) > 0 {
		inputLimit := limit - systemTokens
		if in.PreserveMaterials {
			raw, _ := json.Marshal(in.Inputs)
			inputLimit = counter.Count(string(raw))*4 + 1024
		}
		policy := DefaultInputPolicy()
		if in.PreserveMaterials {
			raw, _ := json.Marshal(in.Inputs)
			policy.MaxValueRunes = len(raw) + 1
		}
		if inputsText, _ := FormatTaskInputsForPromptWithPolicy(in.Inputs, inputLimit, counter, policy); strings.TrimSpace(inputsText) != "" {
			sections = append(sections, Section{Name: "task_inputs", Content: inputsText, Priority: 4})
		}
	}
	if strings.TrimSpace(in.Volatile) != "" {
		sections = append(sections, Section{Name: "volatile", Content: in.Volatile, Priority: 5})
	}
	if in.PreserveMaterials {
		var builder strings.Builder
		report := PromptReport{Budget: limit, TotalTokens: systemTokens}
		for _, section := range sections {
			if strings.TrimSpace(section.Content) == "" {
				continue
			}
			writeSection(&builder, section.Name, section.Content, false, 0, 0)
			n := counter.Count(section.Content)
			report.Sections = append(report.Sections, SectionReport{Name: section.Name, OriginalTokens: n, IncludedTokens: n})
		}
		user := builder.String()
		report.TotalTokens += counter.Count(user)
		return TaskPrompt{System: in.System, User: user, Report: report}, nil
	}
	user, report, err := BuildSectionsWithLimit(sections, limit-systemTokens, counter)
	if err != nil {
		return TaskPrompt{}, err
	}
	report.Budget = limit
	report.TotalTokens += systemTokens
	for _, section := range report.Sections {
		if section.Name == "wiki_context" {
			report.WikiTokens = section.IncludedTokens
		}
	}
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
