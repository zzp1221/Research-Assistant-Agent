package planner

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestContextBinderResolvesPaperAttachmentAndMetadata(t *testing.T) {
	plan := &models.PlanGraph{
		Context: &models.ResearchContext{
			Attachments: []models.AttachmentRef{{ID: "u1", Name: "paper.pdf", DocumentID: "doc-1", Role: "paper"}},
			Documents:   []models.DocumentRef{{DocumentID: "doc-1", UploadID: "u1"}},
			Paper:       &models.PaperMetadata{Title: "Fast Algorithms for Mining Association Rules"},
		},
		Artifacts: map[string]models.Artifact{
			"paper_metadata": {Key: "paper_metadata", Type: "json", Kind: "paper_metadata", Value: `{"title":"Fast Algorithms for Mining Association Rules"}`},
		},
	}
	task := &models.TaskNode{
		InputBindings: []models.InputBinding{
			{Target: "document_id", Source: models.BindingSource{Kind: "context_document"}, Required: true},
			{Target: "paper_metadata", Source: models.BindingSource{Kind: "context_paper"}, Required: true},
		},
		RequiredArtifacts: []string{"paper_metadata"},
	}
	inputs, err := (&ContextBinder{}).BuildInputs(plan, task)
	if err != nil {
		t.Fatal(err)
	}
	if inputs["document_id"] != "doc-1" {
		t.Fatalf("document_id=%v", inputs["document_id"])
	}
	metadata, ok := inputs["paper_metadata"].(map[string]any)
	if !ok || metadata["title"] != "Fast Algorithms for Mining Association Rules" {
		t.Fatalf("paper_metadata=%#v", inputs["paper_metadata"])
	}
}

func TestContextBinderRejectsMissingRequiredAttachment(t *testing.T) {
	plan := &models.PlanGraph{Context: &models.ResearchContext{}}
	task := &models.TaskNode{InputBindings: []models.InputBinding{
		{Target: "document_id", Source: models.BindingSource{Kind: "context_document"}, Required: true},
	}}
	if _, err := (&ContextBinder{}).BuildInputs(plan, task); err == nil {
		t.Fatal("expected missing required binding to fail")
	}
}

func TestValidateFinalAnswerRejectsMissingPaperTemplate(t *testing.T) {
	plan := &models.PlanGraph{
		IntentType: "General",
		Context:    &models.ResearchContext{Paper: &models.PaperMetadata{Title: "Apriori"}},
		Artifacts: map[string]models.Artifact{
			"general_response": {Key: "general_response", Value: "当前对话中尚未提供具体的论文标题，无法对该论文进行实质性内容解析。"},
		},
	}
	if err := ValidateFinalAnswer(plan); err == nil {
		t.Fatal("expected missing-paper template to be rejected")
	}
}
