package api

import (
	"context"
	"fmt"
	"net/http"
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
		"characters":   document.Characters,
		"extractor":    document.Extractor,
		"error":        document.Error,
		"extracted_at": document.ExtractedAt,
	}
}

func registerDocumentRoutes(apiGroup *gin.RouterGroup) {
	apiGroup.GET("/uploads/:id/document", getUploadDocument)
	apiGroup.GET("/uploads/:id/document/pages/:page", getUploadDocumentPage)
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
		pages = append(pages, gin.H{"number": page.Number, "chars": page.Chars})
	}
	c.JSON(http.StatusOK, gin.H{
		"upload_id": metadata.ID,
		"name":      metadata.Name,
		"document":  documentSummary(document),
		"pages":     pages,
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
