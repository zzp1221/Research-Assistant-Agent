package wiki

import (
	"context"
	"encoding/json"
	"strings"

	"research-assistant-backend/internal/models"
)

// RefreshForTask tags the task/procedure records written by memory.RecordTask,
// ingests paper_parse summaries as pending points and records metrics artifacts
// as result points. All steps are best-effort from the scheduler's perspective.
func (b *Builder) RefreshForTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode, result *models.TaskExecutionResult) error {
	if b == nil || b.Memory == nil || plan == nil || task == nil {
		return nil
	}
	paperID := ""
	if plan.Context != nil && plan.Context.Paper != nil && b.Aliases != nil {
		if resolved, _, err := b.Aliases.Resolve(plan.OwnerID, *plan.Context.Paper, nil); err == nil {
			paperID = resolved
		}
	}
	if paperID != "" {
		if err := TagTaskRecord(ctx, b.Memory, plan, task, paperID); err != nil {
			return err
		}
	}
	if task.Type == "paper_parse" {
		meta := TaskPaperMetadata(task, result)
		if strings.TrimSpace(meta.Title) != "" {
			documentID := ""
			if task.Inputs != nil {
				documentID = strings.TrimSpace(stringValue(task.Inputs["document_id"]))
			}
			summary := task.Result
			if result != nil {
				for _, artifact := range result.Artifacts {
					if artifact.Key == "paper_summary" && strings.TrimSpace(artifact.Value) != "" {
						summary = artifact.Value
						break
					}
				}
			}
			page, err := b.IngestPaperParse(ctx, plan.OwnerID, plan.ProjectID, meta, documentID, summary)
			if err != nil {
				return err
			}
			paperID = page.PaperID
		}
	}
	if paperID != "" && result != nil {
		for _, artifact := range result.Artifacts {
			if artifact.Kind != "metrics" {
				continue
			}
			if _, err := b.AddResultPointFromArtifact(ctx, plan.OwnerID, plan.ProjectID, paperID, artifact, plan.ID, task.ID); err != nil {
				continue
			}
		}
	}
	if paperID != "" {
		_, err := b.RefreshPaper(ctx, plan.OwnerID, paperID)
		return err
	}
	return nil
}

// TaskPaperMetadata decodes the paper metadata carried by a paper_parse task.
func TaskPaperMetadata(task *models.TaskNode, result *models.TaskExecutionResult) models.PaperMetadata {
	var meta models.PaperMetadata
	if task != nil && task.Inputs != nil {
		if raw, ok := task.Inputs["paper_metadata"]; ok {
			decodePaperMetadata(raw, &meta)
		}
	}
	if strings.TrimSpace(meta.Title) == "" && result != nil && strings.TrimSpace(result.StructuredData) != "" {
		_ = json.Unmarshal([]byte(result.StructuredData), &meta)
	}
	return meta
}

func decodePaperMetadata(raw any, meta *models.PaperMetadata) {
	if meta == nil || raw == nil {
		return
	}
	switch value := raw.(type) {
	case string:
		_ = json.Unmarshal([]byte(value), meta)
	case map[string]any:
		encoded, err := json.Marshal(value)
		if err == nil {
			_ = json.Unmarshal(encoded, meta)
		}
	}
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return strings.Trim(string(encoded), "\"")
}
