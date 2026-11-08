package prompts

import (
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestBuildTaskPromptIncludesMemorySectionsInOrder(t *testing.T) {
	plan := &models.PlanGraph{
		ID: "p-1", UserIntent: "复现 Attention 论文", IntentType: "Paper_Reproduction",
		Context: &models.ResearchContext{ParentPlanSummary: "parent plan summary", MemoryContext: "memory context"},
	}
	task := &models.TaskNode{Name: "parse", Type: "paper_parse", Description: "解析论文", OutputArtifacts: []string{"paper_summary"}}
	prompt, err := BuildTaskPrompt(TaskPromptInput{
		Budget:          PromptBudget{ContextWindow: 32000, MaxOutput: 8192, Safety: 1600},
		System:          "system",
		CoreMemory:      "<memory kind=\"profile\">core memory</memory>",
		RetrievedMemory: "<memory kind=\"fact\">retrieved memory</memory>",
		Plan:            plan,
		Task:            task,
		Inputs:          map[string]any{"paper_metadata": "metadata"},
	})
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	text := prompt.User
	names := []string{"core_memory", "plan_context", "task_instruction", "retrieved_memory", "task_inputs"}
	last := -1
	for _, name := range names {
		index := strings.Index(text, "[section: "+name+"]")
		if index < 0 {
			t.Fatalf("missing section %s in prompt:\n%s", name, text)
		}
		if index <= last {
			t.Fatalf("section %s is out of order in prompt:\n%s", name, text)
		}
		last = index
	}
	if !strings.Contains(text, "parent plan summary") || !strings.Contains(text, "memory context") {
		t.Fatalf("plan memory context missing:\n%s", text)
	}
}
