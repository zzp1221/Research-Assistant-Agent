package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/models"
	uploadspkg "research-assistant-backend/internal/uploads"
)

// buildResearchContext converts resolved uploads into the typed plan context.
// It never puts storage_path or text_excerpt into ResearchContext.
func buildResearchContext(ownerID, sessionID, rawIntent string, attachments []map[string]any, intentType string) (*models.ResearchContext, error) {
	ctx := &models.ResearchContext{
		Version:     "research.context/v1",
		UserIntent:  rawIntent,
		Attachments: make([]models.AttachmentRef, 0, len(attachments)),
		Documents:   make([]models.DocumentRef, 0, len(attachments)),
		Entities:    map[string]any{},
		Constraints: map[string]any{},
	}
	requirePaper := false
	for _, item := range attachments {
		if stringValue(item["role"]) == "paper" {
			requirePaper = true
			break
		}
	}
	for _, item := range attachments {
		ref := models.AttachmentRef{
			ID:          stringValue(item["id"]),
			Name:        stringValue(item["name"]),
			ContentType: stringValue(item["content_type"]),
			SHA256:      stringValue(item["sha256"]),
			DocumentID:  stringValue(item["document_id"]),
			PageCount:   intValue(item["page_count"]),
			Role:        stringValue(item["role"]),
		}
		if ref.Role == "" {
			ref.Role = "unknown"
		}
		ctx.Attachments = append(ctx.Attachments, ref)
		if ref.DocumentID == "" {
			continue
		}
		ctx.Documents = append(ctx.Documents, models.DocumentRef{
			DocumentID: ref.DocumentID,
			UploadID:   ref.ID,
			PageCount:  ref.PageCount,
			CharCount:  intValue(item["char_count"]),
		})
		if ref.Role != "paper" || ctx.Paper != nil {
			continue
		}
		document, err := documents.Load(uploadspkg.Directory(ownerID, ref.ID))
		if err != nil {
			if requirePaper {
				return nil, fmt.Errorf("paper document %q is unavailable: %w", ref.Name, err)
			}
			continue
		}
		metadata, err := documents.ExtractMetadata(document)
		if err != nil {
			if requirePaper {
				return nil, fmt.Errorf("paper metadata could not be extracted from %q: %w", ref.Name, err)
			}
			continue
		}
		// Provider metadata (arXiv/Crossref) corrects the known PDF extraction
		// gaps: split author names, the arXiv stamp year and missing DOI.
		if providerRaw := stringValue(item["paper_metadata"]); providerRaw != "" {
			var provider models.PaperMetadata
			if json.Unmarshal([]byte(providerRaw), &provider) == nil {
				metadata = documents.ApplyProviderMetadata(metadata, provider)
			}
		}
		if title := extractQuotedPaperTitle(rawIntent); title != "" {
			metadata.Title = title
			metadata.Source = "user"
			metadata.Confidence = 1.0
			metadata.Warnings = nil
		}
		ctx.Paper = &metadata
	}
	if ctx.Paper == nil && len(attachments) == 0 {
		if title := extractQuotedPaperTitle(rawIntent); title != "" {
			ctx.Paper = &models.PaperMetadata{Title: title}
		}
	}
	if ctx.Paper == nil && requirePaper {
		return nil, fmt.Errorf("a paper PDF with extractable metadata is required")
	}
	return ctx, nil
}

func requiresPaperMetadata(intentType, rawIntent string) bool {
	if intentType == "Paper_Reproduction" {
		return true
	}
	if intentType == "General" {
		lower := strings.ToLower(rawIntent)
		return strings.Contains(lower, "论文") || strings.Contains(lower, "paper") || strings.Contains(lower, "文献")
	}
	return false
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

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	default:
		return 0
	}
}

// publicIntentContext removes internal storage paths and excerpts before the
// intent context is returned to the browser.
func publicIntentContext(ctx models.IntentContext) models.IntentContext {
	public := ctx
	public.Entities = map[string]any{}
	for key, value := range ctx.Entities {
		if key != "uploaded_files" {
			public.Entities[key] = value
			continue
		}
		raw, ok := value.([]map[string]any)
		if !ok {
			continue
		}
		files := make([]map[string]any, 0, len(raw))
		for _, file := range raw {
			files = append(files, map[string]any{
				"id":           file["id"],
				"name":         file["name"],
				"content_type": file["content_type"],
				"sha256":       file["sha256"],
				"document_id":  file["document_id"],
				"page_count":   file["page_count"],
				"role":         file["role"],
			})
		}
		public.Entities["uploaded_files"] = files
	}
	return public
}
