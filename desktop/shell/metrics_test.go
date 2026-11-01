package shell

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParsePrometheusMetrics(t *testing.T) {
	text := "# HELP go_goroutines Number of goroutines\n" +
		"go_goroutines 12\n" +
		"process_resident_memory_bytes 1.5e+08\n" +
		"http_requests_total{method=\"GET\",code=\"200\"} 3\n" +
		"http_requests_total{method=\"POST\",code=\"201\"} 2\n"
	summary := ParsePrometheusMetrics(text, time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC))
	if summary.Goroutines != 12 {
		t.Fatalf("goroutines = %d", summary.Goroutines)
	}
	if summary.ResidentMemoryBytes != 150000000 {
		t.Fatalf("memory = %d", summary.ResidentMemoryBytes)
	}
	if summary.RequestCount != 5 {
		t.Fatalf("requests = %v", summary.RequestCount)
	}
	if summary.CollectedAt != "2026-10-31T00:00:00Z" {
		t.Fatalf("collected_at = %q", summary.CollectedAt)
	}
}

func TestFetchMetrics(t *testing.T) {
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte("go_goroutines 7\nprocess_resident_memory_bytes 1024\n"))
	}))
	defer server.Close()
	shell := &Shell{}
	shell.status.BackendURL = server.URL
	shell.backendTok = "token"
	summary, err := shell.FetchMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if summary.Goroutines != 7 || summary.ResidentMemoryBytes != 1024 {
		t.Fatalf("summary = %#v", summary)
	}
	if authorization != "Bearer token" {
		t.Fatalf("authorization = %q", authorization)
	}
}
