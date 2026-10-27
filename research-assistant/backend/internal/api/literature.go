package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/scan"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

var (
	literatureClientOnce    sync.Once
	literatureClient        *literature.Client
	literatureClientFactory = defaultLiteratureClient
)

func defaultLiteratureClient() *literature.Client {
	literatureClientOnce.Do(func() {
		timeoutSec := envIntDefault("LITERATURE_TIMEOUT_SECONDS", 30)
		literatureClient = literature.NewClient(literature.Config{
			CacheDir:       strings.TrimSpace(os.Getenv("LITERATURE_CACHE_DIR")),
			CacheTTL:       time.Duration(envIntDefault("LITERATURE_CACHE_TTL_SECONDS", 900)) * time.Second,
			Timeout:        time.Duration(timeoutSec) * time.Second,
			GitHubToken:    strings.TrimSpace(os.Getenv("GITHUB_TOKEN")),
			OpenAlexMailto: strings.TrimSpace(os.Getenv("OPENALEX_MAILTO")),
		})
	})
	return literatureClient
}

func getLiteratureClient() *literature.Client {
	return literatureClientFactory()
}

// RegisterLiteratureRoutes wires provider search and reference import.
func RegisterLiteratureRoutes(apiGroup *gin.RouterGroup) {
	apiGroup.GET("/literature/search", searchLiterature)
	apiGroup.POST("/literature/import", importLiterature)
}

func searchLiterature(c *gin.Context) {
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q is required", "code": "query_required", "remediation": "提供 q=关键词 后再搜索。"})
		return
	}
	limit := 10
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 && value <= 30 {
			limit = value
		}
	}
	sources := []string{}
	if raw := strings.TrimSpace(c.Query("sources")); raw != "" {
		for _, item := range strings.Split(raw, ",") {
			if item = strings.TrimSpace(item); item != "" {
				sources = append(sources, item)
			}
		}
	}
	yearFrom := 0
	if raw := strings.TrimSpace(c.Query("year_from")); raw != "" {
		yearFrom = literature.SearchYearFromParser(raw)
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(envIntDefault("LITERATURE_TIMEOUT_SECONDS", 30))*time.Second)
	defer cancel()
	results, warnings, err := getLiteratureClient().SearchWithWarnings(ctx, query, literature.SearchOptions{
		Sources:  sources,
		Limit:    limit,
		YearFrom: yearFrom,
	})
	if err != nil {
		status := http.StatusBadGateway
		code := "literature_search_failed"
		if literature.IsCode(err, literature.CodeInvalidQuery) {
			status = http.StatusBadRequest
			code = "invalid_query"
		}
		c.JSON(status, gin.H{"error": err.Error(), "code": code, "remediation": "检查查询词或稍后重试；也可以先上传 PDF 继续。"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"query":    query,
		"results":  results,
		"count":    len(results),
		"warnings": warnings,
		"sources":  sources,
	})
}

func importLiterature(c *gin.Context) {
	var payload struct {
		Ref    string `json:"ref" binding:"required"`
		Attach *bool  `json:"attach,omitempty"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ref is required", "code": "ref_required"})
		return
	}
	userID := resolveUserID(c)
	sessionID := resolveSessionID(c)
	attach := true
	if payload.Attach != nil {
		attach = *payload.Attach
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), time.Duration(envIntDefault("LITERATURE_IMPORT_TIMEOUT_SECONDS", 120))*time.Second)
	defer cancel()
	imported, err := getLiteratureClient().Import(ctx, payload.Ref)
	if err != nil {
		status := http.StatusBadGateway
		code := "literature_import_failed"
		switch {
		case literature.IsCode(err, literature.CodeInvalidRef):
			status = http.StatusBadRequest
			code = "invalid_ref"
		case literature.IsCode(err, literature.CodePDFUnavailable):
			status = http.StatusUnprocessableEntity
			code = "pdf_unavailable"
		case literature.IsCode(err, literature.CodePDFTooLarge):
			status = http.StatusRequestEntityTooLarge
			code = "pdf_too_large"
		}
		c.JSON(status, gin.H{"error": err.Error(), "code": code, "remediation": "确认 arXiv/DOI 可访问；若只有受限全文，请手动上传 PDF。"})
		return
	}
	response := gin.H{
		"metadata": imported.Result,
		"source":   imported.Source,
		"attached": false,
	}
	if attach {
		upload, uploadErr := persistImportedPDF(ctx, userID, sessionID, imported)
		if uploadErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": uploadErr.Error(), "code": "import_store_failed", "remediation": "重试导入；若持续失败，请手动上传 PDF。"})
			return
		}
		response["upload"] = upload
		response["attached"] = true
	}
	recordAuditEvent("literature_imported", userID, payload.Ref, map[string]any{"source": imported.Source, "title": imported.Title})
	c.JSON(http.StatusOK, response)
}

// persistImportedPDF stores the imported PDF with the same SHA-256 dedupe,
// quota and scan rules as an interactive upload.
func persistImportedPDF(ctx context.Context, ownerID, sessionID string, imported *literature.ImportResult) (uploadResponse, error) {
	if imported == nil || len(imported.PDF) == 0 {
		return uploadResponse{}, fmt.Errorf("imported PDF is empty")
	}
	name := sanitizeUploadName(importedFilename(imported))
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	sum := sha256.Sum256(imported.PDF)
	sha := hex.EncodeToString(sum[:])
	if existing, ok := findUploadByHash(ownerID, sha); ok {
		response := uploadResponseFromMetadata(existing)
		response.Deduplicated = true
		if document, loadErr := documents.Load(filepath.Dir(existing.StoredPath)); loadErr == nil {
			response.Document = documentSummary(document)
		}
		return response, nil
	}
	if err := ensureUploadQuota(ownerID, int64(len(imported.PDF))); err != nil {
		return uploadResponse{}, err
	}
	uploadID := uuid.NewString()
	directory := uploadDirectory(ownerID, uploadID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return uploadResponse{}, err
	}
	storedPath := filepath.Join(directory, "content.pdf")
	if err := os.WriteFile(storedPath, imported.PDF, 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return uploadResponse{}, err
	}
	metadata := uploadMetadata{
		ID:          uploadID,
		Name:        name,
		ContentType: "application/pdf",
		Size:        int64(len(imported.PDF)),
		SHA256:      sha,
		OwnerID:     ownerID,
		SessionID:   sessionID,
		StoredPath:  storedPath,
		CreatedAt:   time.Now().UTC(),
	}
	scanResult := scan.Result{Status: "skipped", Detail: "CLAMAV_ADDR is not configured"}
	if scan.Configured() {
		result, scanErr := scan.ScanBytes(ctx, imported.PDF)
		if scanErr != nil {
			_ = os.RemoveAll(directory)
			return uploadResponse{}, scanErr
		}
		scanResult = result
		if result.Status == "infected" {
			_ = os.RemoveAll(directory)
			return uploadResponse{}, fmt.Errorf("imported PDF failed malware scan: %s", result.Signature)
		}
	}
	metadata.ScanStatus = scanResult.Status
	metadata.ScanDetail = scanResult.Detail
	if err := writeUploadMetadata(metadata); err != nil {
		_ = os.RemoveAll(directory)
		return uploadResponse{}, err
	}
	if err := writeUploadIndex(ownerID, sha, uploadID); err != nil {
		logUploadError("cannot persist imported upload index", err)
	}
	document := documents.ExtractPDF(ctx, uploadID, storedPath)
	if err := documents.Save(directory, document); err != nil {
		_ = os.RemoveAll(directory)
		return uploadResponse{}, err
	}
	response := uploadResponseFromMetadata(metadata)
	response.Scan = &scanResult
	response.Document = documentSummary(document)
	return response, nil
}

func importedFilename(imported *literature.ImportResult) string {
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

var (
	arxivReferencePattern = regexp.MustCompile("(?i)arxiv\\s*:\\s*([0-9]{4}\\.[0-9]{4,5}(?:v[0-9]+)?)")
	doiReferencePattern   = regexp.MustCompile("(?i)(10\\.[0-9]{4,9}/[^\\s<>]+)")
)

// looksLikeLiteratureReference reports whether an intent contains an arXiv ID,
// DOI or explicit PDF URL that can be imported automatically.
func looksLikeLiteratureReference(intent string) bool {
	lower := strings.ToLower(intent)
	if strings.Contains(lower, "arxiv.org/abs/") || strings.Contains(lower, "arxiv.org/pdf/") {
		return true
	}
	if strings.Contains(lower, "doi.org/10.") || strings.Contains(lower, "doi:10.") {
		return true
	}
	if arxivReferencePattern.MatchString(intent) {
		return true
	}
	return doiReferencePattern.MatchString(intent)
}

// extractLiteratureReference pulls the first URL / DOI-like token out of the
// intent so it can be imported.
func extractLiteratureReference(intent string) string {
	fields := strings.Fields(intent)
	for _, field := range fields {
		trimmed := strings.Trim(field, "()[]<>\u300a\u300b,\u3002;\uFF1B")
		lower := strings.ToLower(trimmed)
		switch {
		case strings.Contains(lower, "arxiv.org/abs/"), strings.Contains(lower, "arxiv.org/pdf/"):
			return trimmed
		case strings.Contains(lower, "doi.org/10."), strings.HasPrefix(lower, "doi:10."):
			return trimmed
		}
	}
	if match := arxivReferencePattern.FindStringSubmatch(intent); len(match) > 1 {
		return "arxiv:" + match[1]
	}
	if match := doiReferencePattern.FindStringSubmatch(intent); len(match) > 1 {
		return match[1]
	}
	return ""
}

// importPlanReference attaches an automatically imported paper when the intent
// contains an arXiv URL or DOI. Failures are non-fatal: the planner can still
// work from the title/id in the intent.
func (r *planRuntime) importPlanReference(ctx context.Context, ownerID, sessionID, intent string) (map[string]any, *models.PaperMetadata, bool) {
	if !looksLikeLiteratureReference(intent) {
		return nil, nil, false
	}
	reference := extractLiteratureReference(intent)
	if reference == "" {
		return nil, nil, false
	}
	// 1) Metadata is fast and sufficient for planning.
	metadataCtx, metadataCancel := context.WithTimeout(ctx, time.Duration(envIntDefault("LITERATURE_METADATA_TIMEOUT_SECONDS", 25))*time.Second)
	result, metadataErr := getLiteratureClient().Metadata(metadataCtx, reference)
	metadataCancel()
	if metadataErr != nil {
		recordAuditEvent("literature_auto_import_failed", ownerID, reference, map[string]any{"error": metadataErr.Error()})
		return nil, nil, false
	}
	paper := &models.PaperMetadata{
		Title:      result.Title,
		Authors:    result.Authors,
		Year:       result.Year,
		Venue:      result.Venue,
		ArxivID:    result.ArxivID,
		Abstract:   result.Abstract,
		Source:     result.Source,
		Confidence: 0.9,
	}
	// 2) PDF download is best-effort under a short budget; the plan still gets
	// provider metadata when the PDF is slow or unavailable.
	downloadCtx, downloadCancel := context.WithTimeout(ctx, time.Duration(envIntDefault("LITERATURE_AUTO_IMPORT_TIMEOUT_SECONDS", 60))*time.Second)
	imported, err := getLiteratureClient().Import(downloadCtx, reference)
	downloadCancel()
	if err != nil {
		recordAuditEvent("literature_auto_pdf_unavailable", ownerID, reference, map[string]any{"error": err.Error()})
		return nil, paper, true
	}
	upload, err := persistImportedPDF(ctx, ownerID, sessionID, imported)
	if err != nil {
		recordAuditEvent("literature_auto_import_failed", ownerID, reference, map[string]any{"error": err.Error()})
		return nil, paper, true
	}
	item := map[string]any{
		"id":           upload.ID,
		"name":         upload.Name,
		"content_type": "application/pdf",
		"size":         upload.Size,
		"sha256":       upload.SHA256,
		"storage_path": filepath.Join(uploadDirectory(ownerID, upload.ID), "content.pdf"),
		"role":         "paper",
		"document_id":  upload.ID,
		"source":       imported.Source,
		"title":        imported.Title,
		"url":          imported.URL,
	}
	if document, loadErr := documents.Load(filepath.Dir(item["storage_path"].(string))); loadErr == nil && document.HasText() {
		item["text_excerpt"] = documents.Excerpt(document)
		item["page_count"] = document.PageCount
		item["char_count"] = document.Characters
	}
	encoded, _ := json.Marshal(result)
	item["paper_metadata"] = string(encoded)
	recordAuditEvent("literature_auto_imported", ownerID, reference, map[string]any{"title": imported.Title, "source": imported.Source})
	return item, paper, true
}
