package proxy

import (
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestProxyForwardsAPIAndServesSPA(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"ok\":true}"))
	}))
	defer backend.Close()
	handler, err := New(Config{BackendURL: backend.URL, StaticFS: fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>app</html>")},
		"app.js":     &fstest.MapFile{Data: []byte("console.log(1)")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/health", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "\"ok\":true") {
		t.Fatalf("api response = %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1/unknown/route", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "app") {
		t.Fatalf("spa response = %d %s", rec.Code, rec.Body.String())
	}
}

func TestProxyRejectsCrossOriginAndForeignHost(t *testing.T) {
	handler, err := New(Config{BackendURL: "http://127.0.0.1:1", StaticFS: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("app")}}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/x", strings.NewReader("{}"))
	req.Header.Set("Origin", "http://evil.example")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "http://evil.example/", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign host status = %d", rec.Code)
	}
}

func TestProxyForwardsSSEChunks(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: one\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: two\n\n")
		flusher.Flush()
	}))
	defer backend.Close()
	handler, _ := New(Config{BackendURL: backend.URL, StaticFS: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("app")}}})
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/stream", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "data: one") || !strings.Contains(rec.Body.String(), "data: two") {
		t.Fatalf("sse body = %q", rec.Body.String())
	}
}

func TestProxyServesExecutableAssetsWithoutHostMIMEAssociations(t *testing.T) {
	// Reproduce hosts whose registry maps modern web assets to plain text.
	// These tests must exercise explicit response headers, not Go's MIME table.
	for _, extension := range []string{".js", ".mjs", ".wasm"} {
		original := mime.TypeByExtension(extension)
		if original == "" {
			original = "application/octet-stream"
		}
		t.Cleanup(func() { _ = mime.AddExtensionType(extension, original) })
		if err := mime.AddExtensionType(extension, "text/plain; charset=utf-8"); err != nil {
			t.Fatal(err)
		}
	}
	assets := fstest.MapFS{
		"index.html":            {Data: []byte("<html>app</html>")},
		"assets/app.js":         {Data: []byte("export const ready = true;")},
		"pdfjs/worker.mjs":      {Data: []byte("export const worker = true;")},
		"pdfjs/wasm/pdfjs.wasm": {Data: []byte("\x00asm\x01\x00\x00\x00")},
	}
	handler, err := New(Config{BackendURL: "http://127.0.0.1:1", StaticFS: assets})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ path, contentType string }{
		{"assets/app.js", "text/javascript; charset=utf-8"},
		{"pdfjs/worker.mjs", "text/javascript; charset=utf-8"},
		{"pdfjs/wasm/pdfjs.wasm", "application/wasm"},
	} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			request := httptest.NewRequest(method, "http://127.0.0.1/"+tc.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || response.Header().Get("Content-Type") != tc.contentType {
				t.Errorf("%s %s: status=%d content-type=%q", method, tc.path, response.Code, response.Header().Get("Content-Type"))
			}
			if method == http.MethodGet && response.Body.String() != string(assets[tc.path].Data) {
				t.Errorf("served asset bytes changed for %s", tc.path)
			}
			if method == http.MethodHead && response.Body.Len() != 0 {
				t.Errorf("HEAD response contained an asset body for %s", tc.path)
			}
		}
	}
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/pdfjs/worker.mjs", nil)
	request.Header.Set("Range", "bytes=0-5")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || response.Body.String() != "export" {
		t.Fatalf("ranged worker response: status=%d type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}
