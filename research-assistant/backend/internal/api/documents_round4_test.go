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

	"research-assistant-backend/internal/documents"
	uploadspkg "research-assistant-backend/internal/uploads"

	"github.com/gin-gonic/gin"
)

func setupDocumentAPITest(t *testing.T) (*gin.Engine, string, uploadMetadata) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	t.Setenv("UPLOAD_ROOT", root)
	t.Setenv("ALLOW_LEGACY_USER_HEADER", "true")
	owner := "alice"
	uploadID := "11111111-1111-4111-8111-111111111111"
	directory := uploadspkg.Directory(owner, uploadID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	storedPath := filepath.Join(directory, "content.pdf")
	if err := os.WriteFile(storedPath, []byte("%PDF-1.4 test"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := uploadMetadata{
		ID: uploadID, Name: "fixture.pdf", ContentType: "application/pdf",
		Size: 13, SHA256: "abc", OwnerID: owner, SessionID: "s1",
		StoredPath: storedPath, CreatedAt: time.Now().UTC(),
	}
	if err := writeUploadMetadata(metadata); err != nil {
		t.Fatal(err)
	}
	document := &documents.Document{
		Version: documents.Version, UploadID: uploadID, Status: documents.StatusExtracted,
		PageCount: 1, TotalPages: 1, Characters: 40, Extractor: "pdftotext",
		Pages: []documents.Page{{Number: 1, Text: "First paragraph. The key sentence is here.", Chars: 44, Source: documents.PageSourceText}},
		Blocks: []documents.Block{
			{ID: "p1-b1", Type: documents.BlockParagraph, BBox: [4]float64{0, 0, 100, 20}, Text: "First paragraph. The key sentence is here.", Confidence: 1, Page: 1},
		},
		Tables:   []documents.Table{{ID: "table-p1-1", Page: 1, Caption: "Table 1: Results", Rows: [][]string{{"Model", "Acc"}, {"GNN", "0.9"}}}},
		Figures:  []documents.Figure{{ID: "figure-p1-1", Page: 1, Caption: "Figure 1: Architecture", ImageRef: "figures/fig-p1-000.png"}},
		Quality:  []documents.PageQuality{{Page: 1, Source: documents.PageSourceText, Chars: 44}},
		OCRStats: documents.OCRStats{TotalPages: 1},
	}
	if err := documents.Save(directory, document); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(directory, "figures"), 0o700); err != nil {
		t.Fatal(err)
	}
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(filepath.Join(directory, "figures", "fig-p1-000.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	group := engine.Group("/api")
	registerDocumentRoutes(group)
	return engine, owner, metadata
}

func doDocumentRequest(t *testing.T, engine *gin.Engine, path, user string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-User-Id", user)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func TestDocumentQualityLayoutTableFigureEndpoints(t *testing.T) {
	engine, owner, metadata := setupDocumentAPITest(t)

	quality := doDocumentRequest(t, engine, "/api/uploads/"+metadata.ID+"/document/quality", owner)
	if quality.Code != http.StatusOK {
		t.Fatalf("quality status=%d body=%s", quality.Code, quality.Body.String())
	}
	var qualityBody struct {
		Pages []documents.PageQuality `json:"pages"`
	}
	_ = json.Unmarshal(quality.Body.Bytes(), &qualityBody)
	if len(qualityBody.Pages) != 1 || qualityBody.Pages[0].Source != documents.PageSourceText {
		t.Fatalf("quality=%s", quality.Body.String())
	}

	layout := doDocumentRequest(t, engine, "/api/uploads/"+metadata.ID+"/document/pages/1/layout", owner)
	if layout.Code != http.StatusOK || !bytes.Contains(layout.Body.Bytes(), []byte("p1-b1")) {
		t.Fatalf("layout status=%d body=%s", layout.Code, layout.Body.String())
	}

	table := doDocumentRequest(t, engine, "/api/uploads/"+metadata.ID+"/document/tables/table-p1-1", owner)
	if table.Code != http.StatusOK || !bytes.Contains(table.Body.Bytes(), []byte("GNN")) {
		t.Fatalf("table status=%d body=%s", table.Code, table.Body.String())
	}
	csv := doDocumentRequest(t, engine, "/api/uploads/"+metadata.ID+"/document/tables/table-p1-1?format=csv", owner)
	if csv.Code != http.StatusOK || !bytes.Contains(csv.Body.Bytes(), []byte("Model,Acc")) {
		t.Fatalf("csv status=%d body=%s", csv.Code, csv.Body.String())
	}
	if contentType := csv.Header().Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("csv content-type=%q", contentType)
	}

	figure := doDocumentRequest(t, engine, "/api/uploads/"+metadata.ID+"/document/figures/figure-p1-1", owner)
	if figure.Code != http.StatusOK || figure.Header().Get("Content-Type") == "" {
		t.Fatalf("figure status=%d content-type=%q", figure.Code, figure.Header().Get("Content-Type"))
	}
	figureJSON := doDocumentRequest(t, engine, "/api/uploads/"+metadata.ID+"/document/figures/figure-p1-1?format=json", owner)
	if figureJSON.Code != http.StatusOK || !bytes.Contains(figureJSON.Body.Bytes(), []byte("Architecture")) {
		t.Fatalf("figure json=%d %s", figureJSON.Code, figureJSON.Body.String())
	}

	locate := doDocumentRequest(t, engine, "/api/uploads/"+metadata.ID+"/document/locate?quote=key%20sentence", owner)
	if locate.Code != http.StatusOK || !bytes.Contains(locate.Body.Bytes(), []byte("p1-b1")) {
		t.Fatalf("locate status=%d body=%s", locate.Code, locate.Body.String())
	}
	missing := doDocumentRequest(t, engine, "/api/uploads/"+metadata.ID+"/document/locate?quote=not-present", owner)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing quote status=%d", missing.Code)
	}
}

func TestDocumentEndpointsRespectOwner(t *testing.T) {
	engine, _, metadata := setupDocumentAPITest(t)
	response := doDocumentRequest(t, engine, "/api/uploads/"+metadata.ID+"/document/quality", "bob")
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for other owner, got %d", response.Code)
	}
}
