package proxy

import (
	"io"
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
