package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"research-assistant-backend/internal/models"
)

// ContextBinder resolves a task's declared inputs from the plan's ResearchContext
// and artifacts. It is the single data-flow entry point used by the scheduler.
type ContextBinder struct{}

// BuildInputs implements the binding order:
//  1. task.Inputs constants
//  2. InputBindings (context.* / artifacts.*)
//  3. RequiredArtifacts implicit injection (compatibility with old plans)
func (b *ContextBinder) BuildInputs(plan *models.PlanGraph, task *models.TaskNode) (map[string]any, error) {
	inputs := map[string]any{}
	if plan == nil || task == nil {
		return inputs, fmt.Errorf("plan or task is nil")
	}
	for key, value := range task.Inputs {
		if value == nil {
			continue
		}
		inputs[key] = value
	}

	for _, binding := range task.InputBindings {
		value, ok, err := b.resolveBinding(plan, binding)
		if err != nil {
			return nil, err
		}
		if !ok {
			if binding.Required {
				return nil, fmt.Errorf("required input binding %q from %s is unavailable", binding.Target, binding.Source.Kind)
			}
			continue
		}
		inputs[binding.Target] = value
	}

	for _, key := range task.RequiredArtifacts {
		if _, exists := inputs[key]; exists {
			continue
		}
		artifact, ok := plan.Artifacts[key]
		if !ok {
			return nil, fmt.Errorf("required artifact %q has no producer value", key)
		}
		inputs[key] = artifactInputValue(artifact, key)
	}
	return inputs, nil
}

func (b *ContextBinder) resolveBinding(plan *models.PlanGraph, binding models.InputBinding) (any, bool, error) {
	source := binding.Source
	switch source.Kind {
	case "context_attachment":
		attachment, ok := findAttachment(plan.Context, source.Role, source.Index)
		if !ok {
			return nil, false, nil
		}
		switch binding.Target {
		case "document_id":
			if strings.TrimSpace(attachment.DocumentID) == "" {
				return nil, false, fmt.Errorf("attachment %q has no document_id", attachment.Name)
			}
			return attachment.DocumentID, true, nil
		case "uploaded_files":
			return plan.Context.Attachments, true, nil
		default:
			return attachment, true, nil
		}
	case "context_document":
		if plan.Context == nil || len(plan.Context.Documents) == 0 {
			return nil, false, nil
		}
		index := source.Index
		if index < 0 || index >= len(plan.Context.Documents) {
			return nil, false, fmt.Errorf("document index %d is out of range", index)
		}
		doc := plan.Context.Documents[index]
		if binding.Target == "document_id" {
			return doc.DocumentID, true, nil
		}
		return doc, true, nil
	case "context_paper":
		if plan.Context == nil || plan.Context.Paper == nil {
			return nil, false, nil
		}
		if binding.Target == "paper_metadata" {
			return paperMetadataMap(plan.Context.Paper), true, nil
		}
		return plan.Context.Paper, true, nil
	case "artifact":
		name := strings.TrimSpace(source.Name)
		if name == "" {
			return nil, false, fmt.Errorf("artifact binding %q has empty artifact name", binding.Target)
		}
		artifact, ok := plan.Artifacts[name]
		if !ok {
			return nil, false, nil
		}
		return artifactInputValue(artifact, binding.Target), true, nil
	default:
		return nil, false, fmt.Errorf("unsupported binding source kind %q", source.Kind)
	}
}

func findAttachment(ctx *models.ResearchContext, role string, index int) (models.AttachmentRef, bool) {
	if ctx == nil || len(ctx.Attachments) == 0 {
		return models.AttachmentRef{}, false
	}
	if role != "" {
		for _, attachment := range ctx.Attachments {
			if strings.EqualFold(attachment.Role, role) {
				return attachment, true
			}
		}
		return models.AttachmentRef{}, false
	}
	if index < 0 || index >= len(ctx.Attachments) {
		return models.AttachmentRef{}, false
	}
	return ctx.Attachments[index], true
}

func artifactInputValue(artifact models.Artifact, target string) any {
	raw := artifact.EffectiveValue()
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	// Only paper_metadata is exposed as a decoded value. Other JSON artifacts
	// (for example claim_rubric) are consumed by code that expects the raw JSON
	// string and performs its own strict decoding.
	if artifact.Kind == "paper_metadata" || target == "paper_metadata" {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			return value
		}
	}
	return raw
}

func paperMetadataMap(paper *models.PaperMetadata) map[string]any {
	if paper == nil {
		return map[string]any{}
	}
	raw, _ := json.Marshal(paper)
	var value map[string]any
	_ = json.Unmarshal(raw, &value)
	return value
}
