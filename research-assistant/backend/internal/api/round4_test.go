package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/webread"

	"github.com/gin-gonic/gin"
)

func setupLiteratureTest(t *testing.T, client *literature.Client, library *literature.LibraryStore) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	literatureClientFactory = func() *literature.Client { return client }
	literatureLibraryFactory = func() *literature.LibraryStore { return library }
	t.Cleanup(func() {
		literatureClientFactory = defaultLiteratureClient
		literatureLibraryFactory = defaultLiteratureLibrary
	})
	engine := gin.New()
	group := engine.Group("/api")
	RegisterLiteratureRoutes(group)
	return engine
}

func doLiteratureRequest(t *testing.T, engine *gin.Engine, method, path, user string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		payload = encoded
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-User-Id", user)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeJSONBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode %q: %v", recorder.Body.String(), err)
	}
	return decoded
}

func TestLiteratureRecordsAPI(t *testing.T) {
	store := literature.NewLibraryStore(t.TempDir())
	record, _, err := store.UpsertResult("alice", literature.Result{
		Source: literature.SourceCrossref, DOI: "10.1234/api", Title: "API Paper",
		Authors: []string{"Ada"}, Year: 2021, Venue: "Journal",
	}, "upload-1")
	if err != nil {
		t.Fatal(err)
	}
	engine := setupLiteratureTest(t, literature.NewClient(literature.Config{CacheDir: t.TempDir()}), store)

	list := doLiteratureRequest(t, engine, http.MethodGet, "/api/literature/records?q=api", "alice", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	body := decodeJSONBody(t, list)
	if body["count"].(float64) != 1 {
		t.Fatalf("list body=%#v", body)
	}

	other := doLiteratureRequest(t, engine, http.MethodGet, "/api/literature/records", "bob", nil)
	if decodeJSONBody(t, other)["count"].(float64) != 0 {
		t.Fatalf("owner isolation failed: %s", other.Body.String())
	}

	get := doLiteratureRequest(t, engine, http.MethodGet, "/api/literature/records/"+record.ID, "alice", nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status=%d body=%s", get.Code, get.Body.String())
	}

	bib := doLiteratureRequest(t, engine, http.MethodGet, "/api/literature/records/"+record.ID+"/bibtex", "alice", nil)
	if bib.Code != http.StatusOK || !bytes.Contains(bib.Body.Bytes(), []byte("title = {API Paper}")) {
		t.Fatalf("bibtex status=%d body=%s", bib.Code, bib.Body.String())
	}
	if bib.Header().Get("Content-Type") == "" {
		t.Fatal("bibtex missing content type")
	}

	attach := doLiteratureRequest(t, engine, http.MethodPost, "/api/literature/records/"+record.ID+"/attach", "alice", map[string]any{"session_id": "sess-1"})
	if attach.Code != http.StatusOK {
		t.Fatalf("attach status=%d body=%s", attach.Code, attach.Body.String())
	}
	attached, found, err := store.Get("alice", record.ID)
	if err != nil || !found {
		t.Fatalf("get attached record: found=%v err=%v", found, err)
	}
	if len(attached.AttachedSessions) != 1 || attached.AttachedSessions[0] != "sess-1" {
		t.Fatalf("attached sessions=%#v", attached.AttachedSessions)
	}

	detach := doLiteratureRequest(t, engine, http.MethodDelete, "/api/literature/records/"+record.ID+"/attach", "alice", map[string]any{"session_id": "sess-1"})
	if detach.Code != http.StatusOK {
		t.Fatalf("detach status=%d body=%s", detach.Code, detach.Body.String())
	}
	detached, found, err := store.Get("alice", record.ID)
	if err != nil || !found {
		t.Fatalf("get detached record: found=%v err=%v", found, err)
	}
	if len(detached.AttachedSessions) != 0 {
		t.Fatalf("detached sessions=%#v", detached.AttachedSessions)
	}

	del := doLiteratureRequest(t, engine, http.MethodDelete, "/api/literature/records/"+record.ID, "alice", nil)
	if del.Code != http.StatusOK {
		t.Fatalf("delete status=%d body=%s", del.Code, del.Body.String())
	}
	missing := doLiteratureRequest(t, engine, http.MethodGet, "/api/literature/records/"+record.ID, "alice", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", missing.Code)
	}
}

func TestLiteratureSearchAPISortAndMetadataImport(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/s2", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"paperId":"p1","title":"Low Cite","year":2020,"citationCount":1},{"paperId":"p2","title":"High Cite","year":2021,"citationCount":99}]}`))
	})
	mux.HandleFunc("/arxiv", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><entry><id>http://arxiv.org/abs/2101.00001v1</id><title>Metadata Paper</title><summary>Abstract text.</summary><published>2021-01-01T00:00:00Z</published><author><name>Ada</name></author></entry></feed>`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := literature.NewClient(literature.Config{CacheDir: t.TempDir()})
	client.WithEndpoint(literature.SourceSemanticScholar, server.URL+"/s2")
	client.WithEndpoint(literature.SourceArxiv, server.URL+"/arxiv")
	store := literature.NewLibraryStore(t.TempDir())
	engine := setupLiteratureTest(t, client, store)

	search := doLiteratureRequest(t, engine, http.MethodGet, "/api/literature/search?q=graph&sources=semanticscholar&sort=citations", "alice", nil)
	if search.Code != http.StatusOK {
		t.Fatalf("search status=%d body=%s", search.Code, search.Body.String())
	}
	var searchBody struct {
		Results []literature.Result `json:"results"`
	}
	if err := json.Unmarshal(search.Body.Bytes(), &searchBody); err != nil {
		t.Fatal(err)
	}
	if len(searchBody.Results) != 2 || searchBody.Results[0].Title != "High Cite" {
		t.Fatalf("sorted results=%#v", searchBody.Results)
	}

	imported := doLiteratureRequest(t, engine, http.MethodPost, "/api/literature/import", "alice", map[string]any{
		"ref": "arxiv:2101.00001", "metadata_only": true,
	})
	if imported.Code != http.StatusOK {
		t.Fatalf("import status=%d body=%s", imported.Code, imported.Body.String())
	}
	records := doLiteratureRequest(t, engine, http.MethodGet, "/api/literature/records", "alice", nil)
	body := decodeJSONBody(t, records)
	if body["count"].(float64) != 1 {
		t.Fatalf("records=%#v", body)
	}
}

func TestWebFetchAPI(t *testing.T) {
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><head><title>Web API</title></head><body><article><p>This is a sufficiently long paragraph for the readability extractor to keep, with commas, words, and a complete sentence.</p><p>Another paragraph with more content to make the article score well above the threshold.</p></article></body></html>`))
	}))
	defer server.Close()
	webFetcherFactory = func() *webread.Fetcher {
		return webread.New(webread.Config{AllowPrivate: true, RespectRobots: false, Timeout: 5 * time.Second})
	}
	t.Cleanup(func() { webFetcherFactory = defaultWebFetcher })
	engine := gin.New()
	group := engine.Group("/api")
	RegisterWebRoutes(group)
	payload, _ := json.Marshal(map[string]string{"url": server.URL})
	request := httptest.NewRequest(http.MethodPost, "/api/web/fetch", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var result webread.Result
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Title != "Web API" || result.Text == "" {
		t.Fatalf("result=%#v", result)
	}
}
