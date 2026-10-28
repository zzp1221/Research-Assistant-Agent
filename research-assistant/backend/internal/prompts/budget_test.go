package prompts

import (
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestTruncateUTF8BytesKeepsValidUTF8(t *testing.T) {
	text := "论文复现与实验分析"
	for limit := 1; limit <= len(text); limit++ {
		out := TruncateUTF8Bytes(text, limit)
		if strings.ContainsRune(out, '\uFFFD') {
			t.Fatalf("limit %d produced replacement rune", limit)
		}
		if !utf8Valid(out) {
			t.Fatalf("limit %d produced invalid UTF-8", limit)
		}
	}
}

func utf8Valid(value string) bool {
	return len([]rune(value)) == len([]rune(strings.ToValidUTF8(value, ""))) && strings.ToValidUTF8(value, "") == value
}

func TestBuildSectionsPriorityAndBudget(t *testing.T) {
	sections := []Section{
		{Name: "volatile", Content: strings.Repeat("日志", 500), Priority: 5},
		{Name: "task", Content: "task instruction", Priority: 2},
		{Name: "inputs", Content: strings.Repeat("材料", 500), Priority: 3},
	}
	text, report, err := BuildSectionsWithLimit(sections, 200, EstimatedCounter{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "[section: task]") {
		t.Fatalf("mandatory task section missing: %s", text)
	}
	if !strings.Contains(text, "[section: inputs]") {
		t.Fatalf("inputs section missing: %s", text)
	}
	if report.TotalTokens > 200 {
		t.Fatalf("report tokens=%d exceeds budget 200", report.TotalTokens)
	}
}

func TestBuildSectionsFailsWhenMandatoryOverBudget(t *testing.T) {
	sections := []Section{{Name: "task", Content: strings.Repeat("长任务", 2000), Priority: 2}}
	if _, _, err := BuildSectionsWithLimit(sections, 100, EstimatedCounter{}); err == nil {
		t.Fatal("expected ErrPromptBudgetExceeded")
	}
}

func TestBuildTaskPromptDeduplicatesIntentAndArtifact(t *testing.T) {
	plan := &models.PlanGraph{
		UserIntent: "总结 RAG 技术进展",
		IntentType: "General",
	}
	task := &models.TaskNode{
		Name:            "Synthesize Response",
		Type:            "general_synthesis",
		Description:     "任务目标: 综合生成回答\n具体要求: 总结 RAG 技术进展",
		OutputArtifacts: []string{"general_response"},
	}
	inputs := map[string]any{"background_context": "FULL ARTIFACT CONTENT"}
	result, err := BuildTaskPrompt(TaskPromptInput{
		System: "system prompt",
		Plan:   plan,
		Task:   task,
		Inputs: inputs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(result.User, "总结 RAG 技术进展"); count != 1 {
		t.Fatalf("user intent appears %d times, want 1:\n%s", count, result.User)
	}
	if count := strings.Count(result.User, "FULL ARTIFACT CONTENT"); count != 1 {
		t.Fatalf("artifact appears %d times, want 1:\n%s", count, result.User)
	}
	if !strings.Contains(result.User, "[section: plan_context]") || !strings.Contains(result.User, "[section: task_instruction]") {
		t.Fatalf("section markers missing:\n%s", result.User)
	}
}

func TestBuildTaskPromptOmitsDuplicatePaperMetadata(t *testing.T) {
	plan := &models.PlanGraph{
		UserIntent: "分析这篇论文",
		IntentType: "General",
		Context: &models.ResearchContext{
			Paper: &models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017},
		},
	}
	task := &models.TaskNode{Name: "Parse Paper", Type: "paper_parse", Description: "任务目标: 解析论文"}
	inputs := map[string]any{"paper_metadata": map[string]any{"title": "Attention Is All You Need"}}
	result, err := BuildTaskPrompt(TaskPromptInput{System: "s", Plan: plan, Task: task, Inputs: inputs})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.User, "Paper metadata:") {
		t.Fatalf("paper metadata duplicated in plan context:\n%s", result.User)
	}
	if count := strings.Count(result.User, "Attention Is All You Need"); count != 1 {
		t.Fatalf("paper title appears %d times, want 1", count)
	}
}
