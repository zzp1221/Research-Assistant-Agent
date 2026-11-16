package webui_test

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"research-assistant-desktop/proxy"
)

// Use the same embed directive as the desktop entry point without starting a
// window or any backend process.
//
//go:embed all:dist
var embeddedFrontend embed.FS

func TestEmbeddedFrontendPDFSecurity(t *testing.T) {
	assets, err := fs.Sub(embeddedFrontend, "dist")
	if err != nil {
		t.Fatal(err)
	}
	manifestData, err := fs.ReadFile(assets, "pdfjs/manifest.json")
	if os.IsNotExist(err) {
		if os.Getenv("REQUIRE_DESKTOP_ASSETS") == "1" {
			t.Fatal("desktop frontend is not staged; copy the complete frontend/dist into desktop/webui/dist")
		}
		t.Skip("desktop frontend is not staged; set REQUIRE_DESKTOP_ASSETS=1 for packaging verification")
	}
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version         string `json:"version"`
		Worker          string `json:"worker"`
		IsEvalSupported bool   `json:"isEvalSupported"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.IsEvalSupported || manifest.Version == "" || manifest.Worker != manifest.Version+"/pdf.worker.min.mjs" {
		t.Fatal("embedded PDF manifest does not declare a versioned worker with eval disabled")
	}
	source := os.DirFS(filepath.Join("..", "..", "research-assistant", "frontend", "dist"))
	embeddedFiles := 0
	if err := fs.WalkDir(assets, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || path == ".gitkeep" {
			return nil
		}
		actual, err := fs.ReadFile(assets, path)
		if err != nil {
			return err
		}
		expected, err := fs.ReadFile(source, path)
		if err != nil {
			return err
		}
		if sha256.Sum256(actual) != sha256.Sum256(expected) {
			t.Errorf("embedded asset differs from frontend/dist: %s", path)
		}
		embeddedFiles++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sourceFiles := 0
	if err := fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			sourceFiles++
			if _, err := fs.Stat(assets, path); err != nil {
				t.Errorf("frontend asset missing from desktop embed: %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if embeddedFiles != sourceFiles {
		t.Fatalf("embedded file count %d differs from frontend/dist %d", embeddedFiles, sourceFiles)
	}
	if _, err := fs.Stat(assets, "pdfjs/pdf.worker.min.js"); !os.IsNotExist(err) {
		t.Fatal("obsolete PDF worker survived desktop staging")
	}
	handler, err := proxy.New(proxy.Config{BackendURL: "http://127.0.0.1:1", StaticFS: assets})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/pdfjs/"+manifest.Worker, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("embedded module worker response: status=%d content-type=%q", response.Code, response.Header().Get("Content-Type"))
	}
	worker, err := fs.ReadFile(source, "pdfjs/"+manifest.Worker)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(response.Body.Bytes()) != sha256.Sum256(worker) {
		t.Fatal("desktop proxy did not serve the embedded matching worker")
	}
	t.Logf("PDF.js %s; %d embedded assets match frontend/dist; worker SHA-256=%x; module MIME=%s", manifest.Version, embeddedFiles, sha256.Sum256(worker), response.Header().Get("Content-Type"))
}
