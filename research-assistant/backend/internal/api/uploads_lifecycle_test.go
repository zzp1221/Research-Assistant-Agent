package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func multipartUpload(t *testing.T, name string, content []byte) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(content)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return body, writer.FormDataContentType()
}

func performUpload(t *testing.T, router *gin.Engine, name string, content []byte, user string) *httptest.ResponseRecorder {
	body, contentType := multipartUpload(t, name, content)
	request := httptest.NewRequest(http.MethodPost, "/api/uploads", body)
	request.Header.Set("Content-Type", contentType)
	request.Header.Set(userIDHeaderName, user)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestUploadDeduplicatesIdenticalContent(t *testing.T) {
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUploadRoutes(router.Group("/api"))

	first := performUpload(t, router, "paper-notes.md", []byte("# Same content\n"), "dedupe-user")
	if first.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var firstUpload uploadResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstUpload); err != nil {
		t.Fatal(err)
	}
	second := performUpload(t, router, "paper-notes.md", []byte("# Same content\n"), "dedupe-user")
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	var secondUpload uploadResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondUpload); err != nil {
		t.Fatal(err)
	}
	if !secondUpload.Deduplicated {
		t.Fatal("second upload was not marked as deduplicated")
	}
	if firstUpload.ID != secondUpload.ID {
		t.Fatalf("dedupe returned different ids: %q vs %q", firstUpload.ID, secondUpload.ID)
	}
	if secondUpload.Scan == nil || secondUpload.Scan.Status != "skipped" {
		t.Fatalf("scan result missing for unconfigured scanner: %#v", secondUpload.Scan)
	}
}

func TestUploadDeleteRemovesContentAndIndex(t *testing.T) {
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUploadRoutes(router.Group("/api"))

	created := performUpload(t, router, "notes.md", []byte("# delete me\n"), "delete-user")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var uploaded uploadResponse
	if err := json.Unmarshal(created.Body.Bytes(), &uploaded); err != nil {
		t.Fatal(err)
	}
	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/uploads/"+uploaded.ID, nil)
	deleteRequest.Header.Set(userIDHeaderName, "delete-user")
	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
	fetch := httptest.NewRequest(http.MethodGet, "/api/uploads/"+uploaded.ID+"/content", nil)
	fetch.Header.Set(userIDHeaderName, "delete-user")
	fetchResponse := httptest.NewRecorder()
	router.ServeHTTP(fetchResponse, fetch)
	if fetchResponse.Code != http.StatusNotFound {
		t.Fatalf("deleted upload still readable: status=%d", fetchResponse.Code)
	}
	// Re-uploading the same bytes must create a fresh record, not resurrect it.
	again := performUpload(t, router, "notes.md", []byte("# delete me\n"), "delete-user")
	if again.Code != http.StatusCreated {
		t.Fatalf("re-upload after delete status=%d body=%s", again.Code, again.Body.String())
	}
}

func TestUploadQuota(t *testing.T) {
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	t.Setenv("UPLOAD_MAX_FILES", "1")
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterUploadRoutes(router.Group("/api"))

	if response := performUpload(t, router, "one.md", []byte("one"), "quota-user"); response.Code != http.StatusCreated {
		t.Fatalf("first status=%d body=%s", response.Code, response.Body.String())
	}
	second := performUpload(t, router, "two.md", []byte("two"), "quota-user")
	if second.Code != http.StatusRequestEntityTooLarge || !strings.Contains(second.Body.String(), "quota_exceeded") {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
}

func TestPlanExportMarkdownAndJSON(t *testing.T) {
	_, router := newHardeningRuntime(t)
	created := postPlan(router, `{"intent":"你好，简单介绍一下你能做什么"}`, "export-user", "")
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var payload struct {
		PlanGraph struct {
			ID string `json:"id"`
		} `json:"plan_graph"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	planID := payload.PlanGraph.ID

	markdownRequest := httptest.NewRequest(http.MethodGet, "/api/plans/"+planID+"/export?format=md", nil)
	markdownRequest.Header.Set(userIDHeaderName, "export-user")
	markdownResponse := httptest.NewRecorder()
	router.ServeHTTP(markdownResponse, markdownRequest)
	if markdownResponse.Code != http.StatusOK {
		t.Fatalf("markdown export status=%d body=%s", markdownResponse.Code, markdownResponse.Body.String())
	}
	if !strings.Contains(markdownResponse.Body.String(), "## Nodes") {
		t.Fatalf("markdown export missing node section: %s", markdownResponse.Body.String())
	}

	jsonRequest := httptest.NewRequest(http.MethodGet, "/api/plans/"+planID+"/export?format=json", nil)
	jsonRequest.Header.Set(userIDHeaderName, "export-user")
	jsonResponse := httptest.NewRecorder()
	router.ServeHTTP(jsonResponse, jsonRequest)
	if jsonResponse.Code != http.StatusOK || !strings.Contains(jsonResponse.Body.String(), planID) {
		t.Fatalf("json export status=%d body=%s", jsonResponse.Code, jsonResponse.Body.String())
	}

	zipRequest := httptest.NewRequest(http.MethodGet, "/api/plans/"+planID+"/export?format=zip", nil)
	zipRequest.Header.Set(userIDHeaderName, "export-user")
	zipResponse := httptest.NewRecorder()
	router.ServeHTTP(zipResponse, zipRequest)
	if zipResponse.Code != http.StatusOK {
		t.Fatalf("zip export status=%d body=%s", zipResponse.Code, zipResponse.Body.String())
	}
	if !bytes.HasPrefix(zipResponse.Body.Bytes(), []byte("PK")) {
		t.Fatalf("zip export is not a zip archive: %x", zipResponse.Body.Bytes()[:4])
	}
}

func TestDeleteSessionRemovesPlans(t *testing.T) {
	runtime, router := newHardeningRuntime(t)
	created := postPlan(router, `{"intent":"你好，简单介绍一下你能做什么"}`, "session-user", "")
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d", created.Code)
	}
	var payload struct {
		PlanGraph struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
		} `json:"plan_graph"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	sessionID := payload.PlanGraph.SessionID
	if sessionID == "" {
		t.Fatal("plan did not record a session id")
	}
	request := httptest.NewRequest(http.MethodDelete, "/api/sessions/"+sessionID, nil)
	request.Header.Set(userIDHeaderName, "session-user")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("delete session status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := runtime.store.GetPlan(payload.PlanGraph.ID); err == nil {
		t.Fatal("plan still exists after session delete")
	}
}
