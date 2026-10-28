package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/uploadstore"
)

// executeLiteratureSearch performs a real provider search, imports up to K
// open-access PDFs, records metadata-only failures as missing requirements and
// exposes the structured result to downstream nodes.
func (a *LibrarianAgent) executeLiteratureSearch(ctx context.Context, task *models.Task) error {
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	client := a.Literature
	if client == nil {
		client = literature.NewClient(literature.Config{})
	}
	query := strings.TrimSpace(literatureStringInput(task, "query"))
	if query == "" {
		query = strings.TrimSpace(task.Description)
	}
	if query == "" {
		task.Status = models.StatusFailed
		task.Error = "literature_search requires a query"
		return fmt.Errorf("literature_search requires a query")
	}
	limit := literatureIntInput(task, "limit", 5)
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	sources := literatureStringSliceInput(task, "sources")
	yearFrom := literatureIntInput(task, "min_year", 0)

	searchCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	results, warnings, err := client.SearchWithWarnings(searchCtx, query, literature.SearchOptions{
		Sources: sources, Limit: limit, YearFrom: yearFrom, Sort: "relevance",
	})
	cancel()
	if err != nil {
		task.Status = models.StatusFailed
		task.Error = "literature search failed: " + err.Error()
		task.Metadata = ensureTaskMetadata(task)
		task.Metadata["missing_requirements"] = []models.MissingRequirement{{
			Kind: "literature_search_failed", Name: query, Detail: err.Error(),
			Remediation: "检查网络或 provider 状态后重试；也可以手动上传 PDF。",
		}}
		return err
	}

	ownerID := metadataString(task, "tenant_id")
	selected := make([]map[string]any, 0, len(results))
	missing := make([]models.MissingRequirement, 0)
	importedCount := 0
	for index, result := range results {
		if index >= limit {
			break
		}
		item := map[string]any{
			"title": result.Title, "authors": result.Authors, "year": result.Year,
			"venue": result.Venue, "doi": result.DOI, "arxiv_id": result.ArxivID,
			"url": result.URL, "pdf_url": result.PDFURL, "abstract": result.Abstract,
			"citations": result.Citations, "source": result.Source, "open_access": result.OpenAccess,
			"imported": false,
		}
		uploadID := ""
		ref := literatureReferenceForResult(result)
		if ref != "" {
			importCtx, importCancel := context.WithTimeout(ctx, 120*time.Second)
			imported, importErr := client.Import(importCtx, ref)
			importCancel()
			if importErr != nil {
				item["error"] = importErr.Error()
				missing = append(missing, models.MissingRequirement{
					Kind: "pdf_unavailable", Name: result.Title, Detail: importErr.Error(),
					Remediation: "该文献只有元数据；可稍后重试下载或手动上传 PDF。",
				})
			} else {
				saved, saveErr := uploadstore.SavePDF(ctx, ownerID, "", literatureImportFilename(imported), imported.PDF)
				if saveErr != nil {
					item["error"] = saveErr.Error()
					missing = append(missing, models.MissingRequirement{
						Kind: "pdf_unavailable", Name: result.Title, Detail: saveErr.Error(),
						Remediation: "下载成功但落盘失败；请重试或手动上传 PDF。",
					})
				} else {
					uploadID = saved.Metadata.ID
					item["upload_id"] = saved.Metadata.ID
					item["document_id"] = saved.Metadata.ID
					item["imported"] = true
					if saved.Document != nil {
						item["page_count"] = saved.Document.PageCount
						item["characters"] = saved.Document.Characters
						item["document_status"] = saved.Document.Status
					}
					importedCount++
				}
			}
		} else {
			item["error"] = "no resolvable PDF reference"
			missing = append(missing, models.MissingRequirement{
				Kind: "pdf_unavailable", Name: result.Title, Detail: "provider returned metadata without a PDF reference",
				Remediation: "该来源只提供元数据；请从出版商页面手动上传 PDF。",
			})
		}
		if a.Library != nil {
			if record, _, libraryErr := a.Library.UpsertResult(ownerID, result, uploadID); libraryErr == nil {
				item["record_id"] = record.ID
			}
		}
		selected = append(selected, item)
	}

	structured := map[string]any{
		"query": query, "sources": sources, "limit": limit,
		"warnings": warnings, "results": results,
		"selected_papers":     selected,
		"imported_count":      importedCount,
		"metadata_only_count": len(selected) - importedCount,
	}
	encoded, _ := json.Marshal(structured)
	task.StructuredData = string(encoded)
	task.Result = literatureSearchReport(query, selected, warnings, importedCount)
	task.Metadata = ensureTaskMetadata(task)
	task.Metadata["literature_results"] = structured
	task.Metadata["missing_requirements"] = missing
	task.Metadata["artifact_values"] = map[string]any{
		"literature_results": string(encoded),
		"selected_papers":    selected,
	}
	task.Status = models.StatusCompleted
	logToContext(ctx, "[%s] literature_search query=%q results=%d imported=%d metadata_only=%d",
		a.Name, query, len(selected), importedCount, len(selected)-importedCount)
	return nil
}

func ensureTaskMetadata(task *models.Task) map[string]any {
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	return task.Metadata
}

func literatureReferenceForResult(result literature.Result) string {
	// Prefer arXiv when both identifiers exist: arXiv PDFs are reliably
	// downloadable, while a DOI may only resolve to a paywalled publisher page.
	if arxivID := strings.TrimSpace(result.ArxivID); arxivID != "" {
		return "arxiv:" + arxivID
	}
	if doi := strings.TrimSpace(result.DOI); doi != "" {
		return doi
	}
	if pdf := strings.TrimSpace(result.PDFURL); pdf != "" {
		return pdf
	}
	return ""
}

func literatureImportFilename(imported *literature.ImportResult) string {
	title := strings.TrimSpace(imported.Title)
	if title == "" {
		title = "imported-paper"
	}
	if imported.ArxivID != "" {
		return imported.ArxivID + " " + title + ".pdf"
	}
	if imported.DOI != "" {
		return strings.ReplaceAll(imported.DOI, "/", "_") + " " + title + ".pdf"
	}
	return title + ".pdf"
}

func literatureSearchReport(query string, papers []map[string]any, warnings []literature.Warning, importedCount int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "文献检索：%s\n\n", query)
	fmt.Fprintf(&builder, "共 %d 篇候选，已导入 %d 篇 PDF。\n", len(papers), importedCount)
	for index, paper := range papers {
		title := strings.TrimSpace(fmt.Sprint(paper["title"]))
		status := "仅元数据"
		if imported, _ := paper["imported"].(bool); imported {
			status = "已导入 PDF"
		}
		fmt.Fprintf(&builder, "%d. %s（%s）\n", index+1, title, status)
	}
	if len(warnings) > 0 {
		builder.WriteString("\nProvider 警告：\n")
		for _, warning := range warnings {
			fmt.Fprintf(&builder, "- %s: %s\n", warning.Source, warning.Message)
		}
	}
	return builder.String()
}

func literatureStringInput(task *models.Task, key string) string {
	if task == nil || task.Inputs == nil {
		return ""
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func literatureIntInput(task *models.Task, key string, fallback int) int {
	if task == nil || task.Inputs == nil {
		return fallback
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &parsed); err == nil {
			return parsed
		}
	}
	return fallback
}

func literatureStringSliceInput(task *models.Task, key string) []string {
	if task == nil || task.Inputs == nil {
		return nil
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		items := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text := strings.TrimSpace(fmt.Sprint(entry)); text != "" {
				items = append(items, text)
			}
		}
		return items
	case string:
		items := make([]string, 0, 4)
		for _, entry := range strings.Split(typed, ",") {
			if text := strings.TrimSpace(entry); text != "" {
				items = append(items, text)
			}
		}
		return items
	}
	return nil
}
