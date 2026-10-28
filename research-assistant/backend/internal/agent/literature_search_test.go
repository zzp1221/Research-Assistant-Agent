package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/models"
)

func TestLibrarianLiteratureSearchImportsPDFAndRecordsMetadataOnly(t *testing.T) {
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	mux := http.NewServeMux()
	mux.HandleFunc("/s2", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":[{"paperId":"p1","title":"Open Paper","year":2021,"citationCount":5,"externalIds":{"ArXiv":"2101.00001"},"openAccessPdf":{"url":"http://%s/pdf"},"authors":[{"name":"Ada"}]},{"paperId":"p2","title":"Metadata Only","year":2020,"citationCount":2,"externalIds":{"DOI":"10.1234/meta"},"authors":[{"name":"Bob"}]}]}`, r.Host)
	})
	mux.HandleFunc("/arxiv", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom"><entry><id>http://arxiv.org/abs/2101.00001v1</id><title>Open Paper</title><summary>Abstract.</summary><published>2021-01-01T00:00:00Z</published><author><name>Ada</name></author></entry></feed>`))
	})
	mux.HandleFunc("/pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4 test"))
	})
	mux.HandleFunc("/crossref/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"message":{"DOI":"10.1234/meta","title":["Metadata Only"],"URL":"https://doi.org/10.1234/meta","issued":{"date-parts":[[2020]]}}}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := literature.NewClient(literature.Config{CacheDir: t.TempDir()})
	client.WithEndpoint(literature.SourceSemanticScholar, server.URL+"/s2")
	client.WithEndpoint(literature.SourceArxiv, server.URL+"/arxiv")
	client.WithEndpoint(literature.SourceCrossref, server.URL+"/crossref")
	store := literature.NewLibraryStore(t.TempDir())
	librarian := &LibrarianAgent{Name: "librarian_agent", Literature: client, Library: store}
	task := &models.Task{
		ID: "t1", Type: "literature_search", Name: "Search Literature",
		Inputs:   map[string]any{"query": "graph neural network", "limit": 2},
		Metadata: map[string]any{"tenant_id": "alice"},
	}
	if err := librarian.ExecuteTask(context.Background(), task, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if task.Status != models.StatusCompleted {
		t.Fatalf("status=%s error=%s", task.Status, task.Error)
	}
	if !strings.Contains(task.StructuredData, "selected_papers") || !strings.Contains(task.StructuredData, "imported_count") {
		t.Fatalf("structured=%s", task.StructuredData)
	}
	missing, ok := task.Metadata["missing_requirements"].([]models.MissingRequirement)
	if !ok || len(missing) == 0 {
		t.Fatalf("missing=%#v", task.Metadata["missing_requirements"])
	}
	if missing[0].Kind != "pdf_unavailable" {
		t.Fatalf("missing kind=%q", missing[0].Kind)
	}
	if records, err := store.List("alice", "", "", 10); err != nil || len(records) != 2 {
		t.Fatalf("records=%#v err=%v", records, err)
	}
	if !strings.Contains(task.Result, "已导入") {
		t.Fatalf("result=%s", task.Result)
	}
}
