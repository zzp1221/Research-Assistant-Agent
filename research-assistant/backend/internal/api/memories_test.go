package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"research-assistant-backend/internal/memory"

	"github.com/gin-gonic/gin"
)

func TestMemoryAPICRUDSearchAndOwnerIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	store := memory.NewMemoryStore()
	settings, err := memory.NewSettingsStore("")
	if err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	RegisterMemoryRoutes(router.Group("/api"), store, settings)

	createBody := []byte(`{"kind":"fact","title":"Attention BLEU","content":"WMT14 EN-DE BLEU 27.3","tags":["attention","transformer"]}`)
	createRequest := httptest.NewRequest(http.MethodPost, "/api/memories", bytes.NewReader(createBody))
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set(userIDHeaderName, "u1")
	createResponse := httptest.NewRecorder()
	router.ServeHTTP(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", createResponse.Code, createResponse.Body.String())
	}
	var created struct {
		Memory memory.Record `json:"memory"`
	}
	if err := json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Memory.ID == "" || created.Memory.Trust != memory.TrustUserExplicit {
		t.Fatalf("unexpected created memory: %#v", created.Memory)
	}

	searchBody := []byte(`{"query":"attention BLEU"}`)
	searchRequest := httptest.NewRequest(http.MethodPost, "/api/memories/search", bytes.NewReader(searchBody))
	searchRequest.Header.Set("Content-Type", "application/json")
	searchRequest.Header.Set(userIDHeaderName, "u1")
	searchResponse := httptest.NewRecorder()
	router.ServeHTTP(searchResponse, searchRequest)
	if searchResponse.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", searchResponse.Code, searchResponse.Body.String())
	}
	var searched struct {
		Results []memory.ScoredRecord `json:"results"`
	}
	if err := json.Unmarshal(searchResponse.Body.Bytes(), &searched); err != nil {
		t.Fatal(err)
	}
	if len(searched.Results) == 0 {
		t.Fatal("expected search hit for owner")
	}

	otherRequest := httptest.NewRequest(http.MethodPost, "/api/memories/search", bytes.NewReader(searchBody))
	otherRequest.Header.Set("Content-Type", "application/json")
	otherRequest.Header.Set(userIDHeaderName, "u2")
	otherResponse := httptest.NewRecorder()
	router.ServeHTTP(otherResponse, otherRequest)
	var other struct {
		Results []memory.ScoredRecord `json:"results"`
	}
	if err := json.Unmarshal(otherResponse.Body.Bytes(), &other); err != nil {
		t.Fatal(err)
	}
	if len(other.Results) != 0 {
		t.Fatalf("cross-owner search leaked %d records", len(other.Results))
	}

	getRequest := httptest.NewRequest(http.MethodGet, "/api/memories/"+created.Memory.ID, nil)
	getRequest.Header.Set(userIDHeaderName, "u2")
	getResponse := httptest.NewRecorder()
	router.ServeHTTP(getResponse, getRequest)
	if getResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-owner get status=%d", getResponse.Code)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/memories/"+created.Memory.ID, nil)
	deleteRequest.Header.Set(userIDHeaderName, "u1")
	deleteResponse := httptest.NewRecorder()
	router.ServeHTTP(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestMemoryAPIRejectsHiddenAndSecretRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	store := memory.NewMemoryStore()
	settings, _ := memory.NewSettingsStore("")
	router := gin.New()
	RegisterMemoryRoutes(router.Group("/api"), store, settings)

	for _, body := range []string{
		`{"kind":"fact","title":"hidden","content":"x","source":{"type":"document","ref":"/app/private-benchmarks/a.json"}}`,
		`{"kind":"fact","title":"secret","content":"sk-abcdefghijklmnopqrstuvwxyz"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/memories", bytes.NewReader([]byte(body)))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(userIDHeaderName, "u1")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %s, got %d body=%s", body, response.Code, response.Body.String())
		}
	}
}
