package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/documents"

	"github.com/gin-gonic/gin"
)

func seedDocumentUpload(t *testing.T, owner string, pageTexts []string) uploadMetadata {
	t.Helper()
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	metadata := uploadMetadata{
		ID: "6b0f0d3e-2f7a-4a3c-9c62-1a1f6f1a0b11", Name: "paper.pdf", ContentType: "application/pdf",
		OwnerID: owner, SessionID: "session", SHA256: "sha-test", CreatedAt: time.Now().UTC(),
	}
	directory := uploadDirectory(metadata.OwnerID, metadata.ID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata.StoredPath = filepath.Join(directory, "content.pdf")
	if err := os.WriteFile(metadata.StoredPath, []byte("%PDF-1.4"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata.Size = 8
	if err := writeUploadMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	page := make([]documents.Page, 0, len(pageTexts))
	chars := 0
	for index, text := range pageTexts {
		page = append(page, documents.Page{Number: index + 1, Text: text, Chars: len([]rune(text))})
		chars += len([]rune(text))
	}
	document := &documents.Document{Version: documents.Version, UploadID: metadata.ID, Status: documents.StatusExtracted, PageCount: len(pageTexts), Characters: chars, Pages: page, ExtractedAt: time.Now().UTC()}
	if err := documents.Save(directory, document); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func TestUploadDocumentEndpointsExposePages(t *testing.T) {
	metadata := seedDocumentUpload(t, "doc-owner", []string{"first page text", "second page text"})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUploadRoutes(router.Group("/api"))

	listRequest := httptest.NewRequest(http.MethodGet, "/api/uploads/"+metadata.ID+"/document", nil)
	listRequest.Header.Set(userIDHeaderName, "doc-owner")
	listResponse := httptest.NewRecorder()
	router.ServeHTTP(listResponse, listRequest)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", listResponse.Code, listResponse.Body.String())
	}
	var payload struct {
		Document struct {
			Status    string `json:"status"`
			PageCount int    `json:"page_count"`
		} `json:"document"`
		Pages []struct {
			Number int `json:"number"`
			Chars  int `json:"chars"`
		} `json:"pages"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Document.Status != "extracted" || payload.Document.PageCount != 2 || len(payload.Pages) != 2 {
		t.Fatalf("payload=%s", listResponse.Body.String())
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/api/uploads/"+metadata.ID+"/document/pages/2", nil)
	pageRequest.Header.Set(userIDHeaderName, "doc-owner")
	pageResponse := httptest.NewRecorder()
	router.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), "second page text") {
		t.Fatalf("status=%d body=%s", pageResponse.Code, pageResponse.Body.String())
	}

	foreignRequest := httptest.NewRequest(http.MethodGet, "/api/uploads/"+metadata.ID+"/document", nil)
	foreignRequest.Header.Set(userIDHeaderName, "other-owner")
	foreignResponse := httptest.NewRecorder()
	router.ServeHTTP(foreignResponse, foreignRequest)
	if foreignResponse.Code != http.StatusNotFound {
		t.Fatalf("foreign status=%d body=%s", foreignResponse.Code, foreignResponse.Body.String())
	}
}

func TestTranslationJobLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		translation := "{\"result\":\"translated page\"}"
		response := map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": translation}}}}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("OPENAI_BASE_URL", server.URL)
	t.Setenv("OPENAI_MODEL_NAME", "test-model")
	appconfig.ResetLLMConfigForTest()

	metadata := seedDocumentUpload(t, "job-owner", []string{"alpha page", "beta page"})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUploadRoutes(router.Group("/api"))

	createRequest := httptest.NewRequest(http.MethodPost, "/api/uploads/"+metadata.ID+"/translation-jobs", bytes.NewBufferString(`{"start_page":1,"end_page":2}`))
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set(userIDHeaderName, "job-owner")
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created struct {
		Job documents.TranslationJob `json:"job"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Job.ID == "" || created.Job.TotalPages != 2 {
		t.Fatalf("created=%s", createResponse.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	status := ""
	for time.Now().Before(deadline) {
		statusRequest := httptest.NewRequest(http.MethodGet, "/api/uploads/"+metadata.ID+"/translation-jobs/"+created.Job.ID, nil)
		statusRequest.Header.Set(userIDHeaderName, "job-owner")
		statusResponse := httptest.NewRecorder()
		router.ServeHTTP(statusResponse, statusRequest)
		var current struct {
			Job documents.TranslationJob `json:"job"`
		}
		if err := json.Unmarshal(statusResponse.Body.Bytes(), &current); err == nil {
			status = current.Job.Status
			if status == documents.JobCompleted {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if status != documents.JobCompleted {
		t.Fatalf("job status=%q, want completed", status)
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/api/uploads/"+metadata.ID+"/translation-jobs/"+created.Job.ID+"/pages/1", nil)
	pageRequest.Header.Set(userIDHeaderName, "job-owner")
	pageResponse := httptest.NewRecorder()
	router.ServeHTTP(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK || !strings.Contains(pageResponse.Body.String(), "translated page") {
		t.Fatalf("page status=%d body=%s", pageResponse.Code, pageResponse.Body.String())
	}
}
