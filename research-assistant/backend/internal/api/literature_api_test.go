package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"research-assistant-backend/internal/literature"

	"github.com/gin-gonic/gin"
)

const apiArxivXML = `<?xml version="1.0"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>http://arxiv.org/abs/1706.03762v7</id>
    <title>Attention Is All You Need</title>
    <summary>Transformer paper.</summary>
    <published>2017-06-12T17:57:34Z</published>
    <author><name>Ashish Vaswani</name></author>
    <link href="PDF" rel="related" title="pdf" type="application/pdf"/>
  </entry>
</feed>`

func newLiteratureAPITest(t *testing.T) (*gin.Engine, *httptest.Server) {
	t.Helper()
	t.Setenv("UPLOAD_ROOT", t.TempDir())
	var pdfURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/arxiv", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write([]byte(strings.ReplaceAll(apiArxivXML, "PDF", pdfURL)))
	})
	mux.HandleFunc("/pdf", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4\nminimal test pdf\n"))
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	pdfURL = server.URL + "/pdf"

	client := literature.NewClient(literature.Config{CacheDir: t.TempDir()})
	client.WithEndpoint(literature.SourceArxiv, server.URL+"/arxiv")
	previous := literatureClientFactory
	literatureClientFactory = func() *literature.Client { return client }
	t.Cleanup(func() { literatureClientFactory = previous })

	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api")
	RegisterUploadRoutes(group)
	RegisterLiteratureRoutes(group)
	return router, server
}

func TestLiteratureSearchEndpoint(t *testing.T) {
	router, _ := newLiteratureAPITest(t)
	request := httptest.NewRequest(http.MethodGet, "/api/literature/search?q=transformer&sources=arxiv", nil)
	request.Header.Set(userIDHeaderName, "lit-user")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Results []literature.Result `json:"results"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Results) != 1 || payload.Results[0].ArxivID != "1706.03762" {
		t.Fatalf("results=%#v", payload.Results)
	}
}

func TestLiteratureImportEndpointAttachesPDF(t *testing.T) {
	router, _ := newLiteratureAPITest(t)
	body := bytes.NewBufferString(`{"ref":"1706.03762"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/literature/import", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(userIDHeaderName, "lit-user")
	request.Header.Set(sessionHeaderName, "lit-session")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Attached bool `json:"attached"`
		Upload   struct {
			ID     string `json:"id"`
			SHA256 string `json:"sha256"`
		} `json:"upload"`
		Metadata literature.Result `json:"metadata"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.Attached || payload.Upload.ID == "" || payload.Metadata.Title != "Attention Is All You Need" {
		t.Fatalf("payload=%#v", payload)
	}
	if payload.Upload.SHA256 == "" {
		t.Fatal("attached upload has no sha256")
	}
}

func TestExtractLiteratureReferenceFormats(t *testing.T) {
	cases := map[string]string{
		"复现论文 https://arxiv.org/abs/1706.03762 的最小实验": "https://arxiv.org/abs/1706.03762",
		"复现 arXiv:1706.03762":     "arxiv:1706.03762",
		"引用 DOI 10.1234/demo 的工作": "10.1234/demo",
		"分析这篇论文":                  "",
	}
	for intent, want := range cases {
		if got := extractLiteratureReference(intent); got != want {
			t.Fatalf("extract(%q)=%q want %q", intent, got, want)
		}
		if want != "" && !looksLikeLiteratureReference(intent) {
			t.Fatalf("intent %q should be detected as a literature reference", intent)
		}
	}
	if looksLikeLiteratureReference("分析这篇论文") {
		t.Fatal("plain analysis intent must not trigger an import")
	}
}

func TestLiteratureImportRejectsInvalidReference(t *testing.T) {
	router, _ := newLiteratureAPITest(t)
	body := bytes.NewBufferString(`{"ref":"not-a-reference"}`)
	request := httptest.NewRequest(http.MethodPost, "/api/literature/import", body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(userIDHeaderName, "lit-user")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_ref") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
