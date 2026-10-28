package webread

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const articleHTML = `<html><head><title>Test Article</title></head><body>
<nav>Navigation links only</nav>
<script>console.log("ignore")</script>
<article>
<h1>Main Heading</h1>
<p>This is the first paragraph of a real article, with enough words to score as readable content, including commas, clauses, and detail.</p>
<p>Second paragraph continues the argument and includes a <a href="/related">related link</a> plus more text for the extractor to keep.</p>
<ul><li>First bullet</li><li>Second bullet</li></ul>
</article>
<footer>Footer noise</footer>
</body></html>`

func TestExtractHTMLReadability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(articleHTML))
	}))
	defer server.Close()
	fetcher := New(Config{AllowPrivate: true, RespectRobots: false, Timeout: 5 * time.Second})
	result, err := fetcher.Fetch(context.Background(), server.URL+"/article")
	if err != nil {
		t.Fatal(err)
	}
	if result.Title != "Test Article" {
		t.Fatalf("title=%q", result.Title)
	}
	if !strings.Contains(result.Text, "first paragraph") || !strings.Contains(result.Text, "Second paragraph") {
		t.Fatalf("text=%q", result.Text)
	}
	if strings.Contains(result.Text, "Navigation links only") || strings.Contains(result.Text, "Footer noise") {
		t.Fatalf("boilerplate leaked into text: %q", result.Text)
	}
	if result.WordCount < 20 || result.Excerpt == "" {
		t.Fatalf("word_count=%d excerpt=%q", result.WordCount, result.Excerpt)
	}
	found := false
	for _, link := range result.Links {
		if link == server.URL+"/related" {
			found = true
		}
	}
	if !found {
		t.Fatalf("links=%#v", result.Links)
	}
}

func TestRobotsDisallow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /private\nAllow: /private/public\n"))
		case "/private":
			_, _ = w.Write([]byte("secret"))
		case "/private/public":
			_, _ = w.Write([]byte("public"))
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer server.Close()
	fetcher := New(Config{AllowPrivate: true, RespectRobots: true})
	_, err := fetcher.Fetch(context.Background(), server.URL+"/private")
	if err == nil || !IsCode(err, CodeRobotsDenied) {
		t.Fatalf("expected robots_denied, got %v", err)
	}
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/private/public"); err != nil {
		t.Fatalf("allow rule should win: %v", err)
	}
}

func TestUnsupportedContentTypeAndSizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/binary" {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("binary"))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("x", 500)))
	}))
	defer server.Close()
	fetcher := New(Config{AllowPrivate: true, RespectRobots: false, MaxBytes: 100})
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/binary"); err == nil || !IsCode(err, CodeUnsupportedContentType) {
		t.Fatalf("expected unsupported_content_type, got %v", err)
	}
	if _, err := fetcher.Fetch(context.Background(), server.URL+"/big"); err == nil || !IsCode(err, CodeContentTooLarge) {
		t.Fatalf("expected content_too_large, got %v", err)
	}
}

func TestSSRFBlocksPrivateAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	fetcher := New(Config{AllowPrivate: false, RespectRobots: false})
	_, err := fetcher.Fetch(context.Background(), server.URL)
	if err == nil || !IsCode(err, CodeSSRFBlocked) {
		t.Fatalf("expected ssrf_blocked, got %v", err)
	}
	if _, err := fetcher.Fetch(context.Background(), "ftp://example.com/x"); err == nil || !IsCode(err, CodeInvalidURL) {
		t.Fatalf("expected invalid_url, got %v", err)
	}
}

func TestFetchCache(t *testing.T) {
	t.Setenv("WEB_FETCH_RESPECT_ROBOTS", "false")
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("cached body"))
	}))
	defer server.Close()
	fetcher := New(Config{AllowPrivate: true, RespectRobots: false, CacheTTL: time.Minute})
	first, err := fetcher.Fetch(context.Background(), server.URL+"/x")
	if err != nil {
		t.Fatal(err)
	}
	second, err := fetcher.Fetch(context.Background(), server.URL+"/x")
	if err != nil {
		t.Fatal(err)
	}
	if first.Cached || !second.Cached || atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("first=%v second=%v hits=%d", first.Cached, second.Cached, hits)
	}
}
