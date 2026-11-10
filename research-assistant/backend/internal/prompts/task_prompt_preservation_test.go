package prompts

import (
	"encoding/json"
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestUploadedFilesDeduplicationPreservesFollowingPlanContext(t *testing.T) {
	plan := &models.PlanGraph{
		UserIntent:   "compare the results",
		ParentPlanID: "parent-plan-42",
		Context: &models.ResearchContext{
			Attachments:       []models.AttachmentRef{{ID: "upload-1", Name: "paper.pdf", Role: "paper"}},
			ParentPlanSummary: "The previous experiment failed on the test split.",
			MemoryContext:     "Use the frozen evaluation protocol.",
		},
	}
	inputs := map[string]any{"uploaded_files": plan.Context.Attachments}
	context := PlanContextForTask(plan, "summarize the experiment", inputs)
	if strings.Contains(context, "Attachments:") {
		t.Fatalf("attachments already supplied as inputs were duplicated: %s", context)
	}
	for _, required := range []string{plan.UserIntent, plan.ParentPlanID, plan.Context.ParentPlanSummary, plan.Context.MemoryContext} {
		if !strings.Contains(context, required) {
			t.Errorf("attachment deduplication discarded %q: %s", required, context)
		}
	}
	if text := PlanContext(plan); !strings.Contains(text, "Attachments:") || !strings.Contains(text, "paper.pdf") {
		t.Fatalf("attachments without matching task inputs should remain: %s", text)
	}
}

func TestManagedTaskPromptPreservesFullPaperMetadata(t *testing.T) {
	paper := &models.PaperMetadata{
		Title:    "Long research context",
		Abstract: strings.Repeat("研究材料", 1200) + "END_OF_FULL_ABSTRACT",
		Warnings: []string{"Preserve the original test split."},
	}
	payload, err := json.Marshal(paper)
	if err != nil {
		t.Fatal(err)
	}
	plan := &models.PlanGraph{UserIntent: "review the paper", Context: &models.ResearchContext{Paper: paper}}
	for _, bound := range []bool{false, true} {
		name := "plan_material"
		inputs := map[string]any{}
		if bound {
			name = "bound_input"
			inputs["paper_metadata"] = string(payload)
		}
		t.Run(name, func(t *testing.T) {
			prompt, err := BuildTaskPrompt(TaskPromptInput{
				Plan: plan, TaskDescription: "Extract the evaluation protocol", Inputs: inputs,
				Budget: PromptBudget{ContextWindow: 32000, MaxOutput: 8192, Safety: 1600}, PreserveMaterials: true,
			})
			if err != nil {
				t.Fatal(err)
			}
			if count := strings.Count(prompt.User, string(payload)); count != 1 {
				t.Fatalf("complete paper metadata appears %d times, want one", count)
			}
			if len(prompt.Report.TruncatedSections()) != 0 {
				t.Fatalf("managed materials must reach the runtime intact: %+v", prompt.Report)
			}
		})
	}
	if strings.Contains(PlanContext(plan), "END_OF_FULL_ABSTRACT") {
		t.Fatal("legacy plan context should retain its explicit metadata size cap")
	}
}
