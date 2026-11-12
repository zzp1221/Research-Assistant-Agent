package planner

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"research-assistant-backend/internal/models"
)

var (
	blobLoaderMu sync.RWMutex
	blobLoader   func(string) ([]byte, error)
)

// SetBlobLoader wires the content-addressed blob store into artifact binding so
// artifacts externalised as "blob:<sha256>" still reach downstream tasks.
func SetBlobLoader(fn func(string) ([]byte, error)) {
	blobLoaderMu.Lock()
	defer blobLoaderMu.Unlock()
	blobLoader = fn
}

// LoadBlobForResponse exposes blob loading to API response hydration.
func LoadBlobForResponse(ref string) ([]byte, error) {
	return loadBlob(ref)
}

// IsImageBytes reports whether raw bytes are a PNG or JPEG image. Content-based
// detection avoids mis-encoding text artifacts whose key happens to contain
// "plot" (for example plot_render_report).
func IsImageBytes(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	if data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return true
	}
	return data[0] == 0xFF && data[1] == 0xD8
}

func loadBlob(ref string) ([]byte, error) {
	blobLoaderMu.RLock()
	loader := blobLoader
	blobLoaderMu.RUnlock()
	if loader == nil {
		return nil, fmt.Errorf("blob loader is not configured")
	}
	return loader(ref)
}

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

	// An artifact produced only by an optional dependency is a diagnostic, not a
	// trusted input: the failure path keeps it for inspection, but the consumer
	// must not silently receive it as if the producer had succeeded.
	optionalOnly := optionalProducerArtifacts(plan, task)
	for _, key := range task.RequiredArtifacts {
		if _, exists := inputs[key]; exists {
			continue
		}
		if optionalOnly[key] {
			return nil, fmt.Errorf("required artifact %q is produced only by an optional dependency and cannot be trusted", key)
		}
		artifact, ok := plan.Artifacts[key]
		if !ok {
			return nil, fmt.Errorf("required artifact %q has no producer value", key)
		}
		inputs[key] = artifactInputValue(artifact, key)
	}
	// Optional inputs that are missing are reported explicitly instead of being
	// invented: downstream prompts can then say which metric is unavailable.
	for _, depID := range task.OptionalDependencies {
		dep := findPlanTaskNode(plan, depID)
		if dep == nil {
			continue
		}
		for _, key := range dep.OutputArtifacts {
			if _, exists := inputs[key]; exists {
				continue
			}
			if _, produced := plan.Artifacts[key]; produced && dep.Status == models.StatusCompleted {
				inputs[key] = artifactInputValue(plan.Artifacts[key], key)
			}
		}
	}
	return inputs, nil
}

// optionalProducerArtifacts lists artifact keys whose producer is an optional
// dependency of the task. Those values are diagnostics: the failure path keeps
// them for inspection, but a consumer must not receive them as trusted input.
func optionalProducerArtifacts(plan *models.PlanGraph, task *models.TaskNode) map[string]bool {
	out := map[string]bool{}
	if plan == nil || task == nil {
		return out
	}
	for _, depID := range task.OptionalDependencies {
		dep := findPlanTaskNode(plan, depID)
		if dep == nil {
			continue
		}
		for _, key := range dep.OutputArtifacts {
			out[key] = true
		}
	}
	return out
}

func findPlanTaskNode(plan *models.PlanGraph, taskID string) *models.TaskNode {
	if plan == nil {
		return nil
	}
	for _, node := range plan.Nodes {
		if node != nil && node.ID == taskID {
			return node
		}
	}
	return nil
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
		// An explicit binding obeys the same trust rule as implicit injection: a
		// diagnostic produced by a failed or superseded task is not an input. A
		// required binding then reports the input as unavailable, which is the
		// contract failure the planner declared.
		if _, trusted := TrustedArtifact(plan, name); !trusted {
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
	if strings.TrimSpace(raw) == "" && strings.TrimSpace(artifact.BlobRef) != "" {
		if data, err := loadBlob(artifact.BlobRef); err == nil {
			if IsImageBytes(data) {
				raw = base64.StdEncoding.EncodeToString(data)
			} else {
				raw = string(data)
			}
		}
	}
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
