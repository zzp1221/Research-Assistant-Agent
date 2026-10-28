package api

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"research-assistant-backend/internal/assist"
	"research-assistant-backend/internal/documents"

	"github.com/gin-gonic/gin"
)

var (
	jobStoresMu sync.Mutex
	jobStores   = map[string]*documents.JobStore{}
)

func jobStoreFor(directory string) *documents.JobStore {
	jobStoresMu.Lock()
	defer jobStoresMu.Unlock()
	if store, ok := jobStores[directory]; ok {
		return store
	}
	store := documents.NewJobStore(filepath.Join(directory, "translation"))
	jobStores[directory] = store
	return store
}

// loadUploadDocument returns the persisted document for an owned upload. Older
// uploads created before extraction existed are extracted lazily on first use.
func loadUploadDocument(c *gin.Context, metadata uploadMetadata) (*documents.Document, error) {
	directory := filepath.Dir(metadata.StoredPath)
	document, err := documents.Load(directory)
	if err == nil {
		return document, nil
	}
	if strings.ToLower(filepath.Ext(metadata.StoredPath)) != ".pdf" {
		return nil, fmt.Errorf("only PDF uploads have a document text layer")
	}
	document = documents.ExtractPDF(c.Request.Context(), metadata.ID, metadata.StoredPath)
	if saveErr := documents.Save(directory, document); saveErr != nil {
		return nil, saveErr
	}
	return document, nil
}

func documentSummary(document *documents.Document) map[string]any {
	if document == nil {
		return gin.H{"status": "missing", "page_count": 0, "characters": 0}
	}
	return gin.H{
		"status":       document.Status,
		"page_count":   document.PageCount,
		"total_pages":  document.TotalPages,
		"characters":   document.Characters,
		"extractor":    document.Extractor,
		"error":        document.Error,
		"extracted_at": document.ExtractedAt,
		"ocr_stats":    document.OCRStats,
		"tables":       len(document.Tables),
		"figures":      len(document.Figures),
		"blocks":       len(document.Blocks),
	}
}

func registerDocumentRoutes(apiGroup *gin.RouterGroup) {
	apiGroup.GET("/uploads/:id/document", getUploadDocument)
	apiGroup.GET("/uploads/:id/document/pages/:page", getUploadDocumentPage)
	apiGroup.GET("/uploads/:id/document/pages/:page/layout", getUploadDocumentPageLayout)
	apiGroup.GET("/uploads/:id/document/locate", getUploadDocumentQuoteLocation)
	apiGroup.GET("/uploads/:id/document/quality", getUploadDocumentQuality)
	apiGroup.GET("/uploads/:id/document/tables/:tableId", getUploadDocumentTable)
	apiGroup.GET("/uploads/:id/document/figures/:figureId", getUploadDocumentFigure)
	apiGroup.GET("/uploads/:id/document/search", getUploadDocumentSearch)
	apiGroup.GET("/uploads/:id/document/sections/:section", getUploadDocumentSection)
	apiGroup.POST("/uploads/:id/assist", postUploadAssist)
	apiGroup.GET("/uploads/:id/translation-jobs", listTranslationJobs)
	apiGroup.POST("/uploads/:id/translation-jobs", createTranslationJob)
	apiGroup.GET("/uploads/:id/translation-jobs/:jobId", getTranslationJob)
	apiGroup.GET("/uploads/:id/translation-jobs/:jobId/pages/:page", getTranslationPage)
	apiGroup.POST("/uploads/:id/translation-jobs/:jobId/cancel", cancelTranslationJob)
}

func ownedUpload(c *gin.Context) (uploadMetadata, bool) {
	metadata, err := loadOwnedUpload(resolveUserID(c), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found"})
		return uploadMetadata{}, false
	}
	return metadata, true
}

func getUploadDocument(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	document, err := loadUploadDocument(c, metadata)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	pages := make([]gin.H, 0, len(document.Pages))
	for _, page := range document.Pages {
		pages = append(pages, gin.H{"number": page.Number, "chars": page.Chars, "source": page.Source})
	}
	tables := make([]gin.H, 0, len(document.Tables))
	for _, table := range document.Tables {
		tables = append(tables, gin.H{
			"id": table.ID, "page": table.Page, "caption": table.Caption,
			"rows": len(table.Rows), "confidence": table.Confidence, "csv_ref": table.CSVRef,
		})
	}
	figures := make([]gin.H, 0, len(document.Figures))
	for _, figure := range document.Figures {
		figures = append(figures, gin.H{
			"id": figure.ID, "page": figure.Page, "caption": figure.Caption,
			"image_ref": figure.ImageRef, "warning": figure.Warning,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"upload_id": metadata.ID,
		"name":      metadata.Name,
		"document":  documentSummary(document),
		"pages":     pages,
		"tables":    tables,
		"figures":   figures,
		"quality":   document.Quality,
		"ocr_stats": document.OCRStats,
	})
}

func getUploadDocumentPage(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	page, err := strconv.Atoi(c.Param("page"))
	if err != nil || page < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page must be a positive integer"})
		return
	}
	document, err := loadUploadDocument(c, metadata)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	if page > len(document.Pages) {
		c.JSON(http.StatusNotFound, gin.H{"error": "page not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"upload_id": metadata.ID, "page": document.Pages[page-1]})
}

func getUploadDocumentPageLayout(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	page, err := strconv.Atoi(c.Param("page"))
	if err != nil || page < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page must be a positive integer"})
		return
	}
	document, err := loadUploadDocument(c, metadata)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	if page > len(document.Pages) {
		c.JSON(http.StatusNotFound, gin.H{"error": "page not found"})
		return
	}
	blocks := make([]documents.Block, 0)
	for _, block := range document.Blocks {
		if block.Page == page {
			blocks = append(blocks, block)
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"upload_id": metadata.ID,
		"page":      page,
		"blocks":    blocks,
		"quality":   document.PageQualityFor(page),
	})
}

func getUploadDocumentQuoteLocation(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	quote := strings.TrimSpace(c.Query("quote"))
	if quote == "" || len([]rune(quote)) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "quote is required and must be at most 2000 characters"})
		return
	}
	document, err := loadUploadDocument(c, metadata)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	location := documents.LocateQuoteLocation(document, quote)
	if !location.Found {
		c.JSON(http.StatusNotFound, gin.H{"error": "quote not found", "code": "quote_not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"upload_id": metadata.ID, "location": location})
}

func getUploadDocumentQuality(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	document, err := loadUploadDocument(c, metadata)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"upload_id":   metadata.ID,
		"status":      document.Status,
		"total_pages": document.TotalPages,
		"ocr_stats":   document.OCRStats,
		"pages":       document.Quality,
	})
}

func findDocumentTable(document *documents.Document, tableID string) *documents.Table {
	if document == nil {
		return nil
	}
	for index := range document.Tables {
		if document.Tables[index].ID == tableID {
			return &document.Tables[index]
		}
	}
	return nil
}

func getUploadDocumentTable(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	document, err := loadUploadDocument(c, metadata)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	table := findDocumentTable(document, c.Param("tableId"))
	if table == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "table not found", "code": "table_not_found"})
		return
	}
	if strings.EqualFold(strings.TrimSpace(c.Query("format")), "csv") {
		var buffer strings.Builder
		writer := csv.NewWriter(&buffer)
		if err := writer.WriteAll(table.Rows); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot encode table csv"})
			return
		}
		writer.Flush()
		filename := sanitizeDownloadName(table.ID) + ".csv"
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
		c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(buffer.String()))
		return
	}
	c.JSON(http.StatusOK, gin.H{"upload_id": metadata.ID, "table": table})
}

func getUploadDocumentFigure(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	document, err := loadUploadDocument(c, metadata)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	var figure *documents.Figure
	for index := range document.Figures {
		if document.Figures[index].ID == c.Param("figureId") {
			figure = &document.Figures[index]
			break
		}
	}
	if figure == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "figure not found", "code": "figure_not_found"})
		return
	}
	if strings.EqualFold(strings.TrimSpace(c.Query("format")), "json") || figure.ImageRef == "" {
		c.JSON(http.StatusOK, gin.H{"upload_id": metadata.ID, "figure": figure})
		return
	}
	relative := filepath.Clean(filepath.FromSlash(figure.ImageRef))
	if relative != "figures" && !strings.HasPrefix(relative, "figures"+string(os.PathSeparator)) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid figure reference"})
		return
	}
	path := filepath.Join(filepath.Dir(metadata.StoredPath), relative)
	if info, statErr := os.Stat(path); statErr != nil || info.IsDir() {
		c.JSON(http.StatusNotFound, gin.H{"error": "figure image is unavailable", "code": "figure_image_missing"})
		return
	}
	c.Header("Cache-Control", "private, max-age=3600")
	c.File(path)
}

func sanitizeDownloadName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "table"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	cleaned := strings.Trim(builder.String(), "-")
	if cleaned == "" {
		return "table"
	}
	return cleaned
}

func getUploadDocumentSearch(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	query := strings.TrimSpace(c.Query("q"))
	if len([]rune(query)) > 500 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "query is too long"})
		return
	}
	topK := 8
	if raw := strings.TrimSpace(c.Query("top_k")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 20 {
			topK = parsed
		}
	}
	store := documents.NewStore("")
	results, err := store.Search(c.Request.Context(), resolveUserID(c), metadata.ID, query, topK)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"upload_id": metadata.ID, "query": query, "results": results})
}

func getUploadDocumentSection(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	section := strings.TrimSpace(c.Param("section"))
	if section == "" || len([]rune(section)) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "section is required and must be short"})
		return
	}
	store := documents.NewStore("")
	text, err := store.ReadSection(c.Request.Context(), resolveUserID(c), metadata.ID, section)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"upload_id": metadata.ID, "section": section, "text": text})
}

type assistPayload struct {
	Mode         string `json:"mode" binding:"required"`
	Text         string `json:"text" binding:"required"`
	Page         int    `json:"page"`
	ContextPages int    `json:"context_pages"`
}

func postUploadAssist(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var payload assistPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	document, docErr := loadUploadDocument(c, metadata)
	if docErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": docErr.Error()})
		return
	}
	radius := payload.ContextPages
	if radius <= 0 {
		radius = 1
	}
	if radius > 3 {
		radius = 3
	}
	contextText := ""
	if payload.Page > 0 {
		contextText = documents.ContextForPage(document, payload.Page, radius)
	}
	result, err := assist.TranslateOrExplain(c.Request.Context(), filepath.Join(filepath.Dir(metadata.StoredPath), "assist-cache"), metadata.SHA256, assist.Request{
		Mode: payload.Mode, Text: payload.Text, Page: payload.Page, Context: contextText,
	})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"mode": result.Mode, "page": result.Page, "result": result.Result,
		"bullets": result.Bullets, "terms": result.Terms, "model": result.Model, "cached": result.Cached,
	})
}

type translationJobPayload struct {
	StartPage  int    `json:"start_page"`
	EndPage    int    `json:"end_page"`
	TargetLang string `json:"target_lang"`
}

func createTranslationJob(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	var payload translationJobPayload
	if err := c.ShouldBindJSON(&payload); err != nil && err.Error() != "EOF" {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	document, docErr := loadUploadDocument(c, metadata)
	if docErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": docErr.Error()})
		return
	}
	if !document.HasText() {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "this PDF has no extractable text layer", "document": documentSummary(document)})
		return
	}
	if payload.TargetLang == "" {
		payload.TargetLang = "zh"
	}
	// Bound one request so a single click cannot translate an entire long paper
	// (and burn the model budget). The client can page through the document.
	const maxTranslationPages = 40
	if payload.StartPage <= 0 {
		payload.StartPage = 1
	}
	if payload.EndPage <= 0 || payload.EndPage > document.PageCount {
		payload.EndPage = document.PageCount
	}
	if payload.EndPage-payload.StartPage+1 > maxTranslationPages {
		payload.EndPage = payload.StartPage + maxTranslationPages - 1
	}
	store := jobStoreFor(filepath.Dir(metadata.StoredPath))
	job, err := store.Create(metadata.ID, resolveUserID(c), payload.TargetLang, document, payload.StartPage, payload.EndPage)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	runner := &documents.Runner{Store: store, CacheDir: filepath.Join(filepath.Dir(metadata.StoredPath), "assist-cache")}
	// The job outlives this HTTP response, so it must not inherit the request context.
	runner.Start(context.Background(), job, document)
	current, _ := store.Get(job.ID, resolveUserID(c))
	c.JSON(http.StatusAccepted, gin.H{"job": current})
}

func listTranslationJobs(c *gin.Context) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return
	}
	store := jobStoreFor(filepath.Dir(metadata.StoredPath))
	jobs := store.List(metadata.ID, resolveUserID(c))
	if jobs == nil {
		jobs = []*documents.TranslationJob{}
	}
	c.JSON(http.StatusOK, gin.H{"jobs": jobs})
}

func translationJobFromPath(c *gin.Context) (*documents.JobStore, uploadMetadata, *documents.TranslationJob, bool) {
	metadata, ok := ownedUpload(c)
	if !ok {
		return nil, uploadMetadata{}, nil, false
	}
	store := jobStoreFor(filepath.Dir(metadata.StoredPath))
	job, ok := store.Get(c.Param("jobId"), resolveUserID(c))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "translation job not found"})
		return nil, uploadMetadata{}, nil, false
	}
	return store, metadata, job, true
}

func getTranslationJob(c *gin.Context) {
	_, _, job, ok := translationJobFromPath(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": job})
}

func getTranslationPage(c *gin.Context) {
	page, err := strconv.Atoi(c.Param("page"))
	if err != nil || page < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "page must be a positive integer"})
		return
	}
	store, _, job, ok := translationJobFromPath(c)
	if !ok {
		return
	}
	text, _, found := store.PageText(job.ID, resolveUserID(c), page)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "page translation not ready"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job_id": job.ID, "page": page, "text": text})
}

func cancelTranslationJob(c *gin.Context) {
	store, _, job, ok := translationJobFromPath(c)
	if !ok {
		return
	}
	canceled, ok := store.Cancel(job.ID, resolveUserID(c))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "translation job not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": canceled})
}
