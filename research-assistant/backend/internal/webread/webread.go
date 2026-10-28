// Package webread fetches public web pages and extracts readable article text
// with SSRF, robots.txt, content-type, size and timeout guards.
package webread

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Error codes returned by the package.
const (
	CodeInvalidURL             = "invalid_url"
	CodeSSRFBlocked            = "ssrf_blocked"
	CodeRobotsDenied           = "robots_denied"
	CodeUnsupportedContentType = "unsupported_content_type"
	CodeContentTooLarge        = "content_too_large"
	CodeFetchTimeout           = "fetch_timeout"
	CodeUpstream               = "upstream_error"
)

// Error is the structured error returned by Fetch.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// IsCode reports whether err is an *Error with code.
func IsCode(err error, code string) bool {
	var target *Error
	return errors.As(err, &target) && target.Code == code
}

// Result is one extracted web page.
type Result struct {
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	Text        string    `json:"text"`
	Excerpt     string    `json:"excerpt"`
	Links       []string  `json:"links"`
	FetchedAt   time.Time `json:"fetched_at"`
	ContentType string    `json:"content_type"`
	WordCount   int       `json:"word_count"`
	Cached      bool      `json:"cached"`
}

// Config controls a Fetcher. Zero values select conservative defaults.
type Config struct {
	MaxBytes      int64
	Timeout       time.Duration
	RespectRobots bool
	UserAgent     string
	AllowPrivate  bool
	CacheTTL      time.Duration
}

// Fetcher is a concurrency-safe web page fetcher with an in-memory cache.
type Fetcher struct {
	config Config
	client *http.Client

	mu     sync.Mutex
	cache  map[string]cacheEntry
	robots map[string]robotsEntry
}

type cacheEntry struct {
	result  *Result
	expires time.Time
}

type robotsEntry struct {
	rules   []robotsRule
	expires time.Time
}

// New builds a Fetcher from config, filling defaults from environment.
func New(cfg Config) *Fetcher {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = int64(envInt("WEB_FETCH_MAX_MB", 2)) * 1024 * 1024
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = time.Duration(envInt("WEB_FETCH_TIMEOUT_SECONDS", 15)) * time.Second
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = time.Duration(envInt("WEB_FETCH_CACHE_TTL_SECONDS", 3600)) * time.Second
	}
	if strings.TrimSpace(cfg.UserAgent) == "" {
		cfg.UserAgent = "research-assistant-webread/1.0"
	}
	if !cfg.RespectRobots && !envBool("WEB_FETCH_RESPECT_ROBOTS", true) {
		cfg.RespectRobots = false
	} else if envBool("WEB_FETCH_RESPECT_ROBOTS", true) {
		cfg.RespectRobots = true
	}
	if envBool("ALLOW_PRIVATE_WEB_FETCH", false) {
		cfg.AllowPrivate = true
	}
	fetcher := &Fetcher{
		config: cfg,
		cache:  map[string]cacheEntry{},
		robots: map[string]robotsEntry{},
	}
	fetcher.client = &http.Client{
		Timeout:   cfg.Timeout,
		Transport: safeTransport(cfg.AllowPrivate),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return &Error{Code: CodeUpstream, Message: "too many redirects"}
			}
			_, err := validateURL(req.Context(), req.URL.String(), cfg.AllowPrivate)
			return err
		},
	}
	return fetcher
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch raw {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return fallback
	}
}

// Fetch downloads and extracts one public web page.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (*Result, error) {
	parsed, err := validateURL(ctx, rawURL, f.config.AllowPrivate)
	if err != nil {
		return nil, err
	}
	key := parsed.String()
	now := time.Now()
	f.mu.Lock()
	if entry, ok := f.cache[key]; ok && now.Before(entry.expires) {
		copyResult := *entry.result
		copyResult.Cached = true
		f.mu.Unlock()
		return &copyResult, nil
	}
	f.mu.Unlock()

	if f.config.RespectRobots {
		allowed, robotsErr := f.allowedByRobots(ctx, parsed)
		if robotsErr == nil && !allowed {
			return nil, &Error{Code: CodeRobotsDenied, Message: "robots.txt disallows this path"}
		}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, key, nil)
	if err != nil {
		return nil, &Error{Code: CodeInvalidURL, Message: "cannot build request"}
	}
	request.Header.Set("User-Agent", f.config.UserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.1")
	response, err := f.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || strings.Contains(strings.ToLower(err.Error()), "timeout") {
			return nil, &Error{Code: CodeFetchTimeout, Message: "fetch timed out"}
		}
		var webErr *Error
		if errors.As(err, &webErr) {
			return nil, webErr
		}
		return nil, &Error{Code: CodeUpstream, Message: "fetch failed: " + err.Error()}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &Error{Code: CodeUpstream, Message: fmt.Sprintf("upstream returned HTTP %d", response.StatusCode)}
	}
	contentType := strings.ToLower(strings.TrimSpace(response.Header.Get("Content-Type")))
	mediaType, _, _ := mime.ParseMediaType(contentType)
	switch mediaType {
	case "text/html", "application/xhtml+xml", "text/plain":
	case "":
		mediaType = "text/html"
	default:
		return nil, &Error{Code: CodeUnsupportedContentType, Message: "unsupported content type: " + contentType}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, f.config.MaxBytes+1))
	if err != nil {
		return nil, &Error{Code: CodeUpstream, Message: "cannot read response body"}
	}
	if int64(len(body)) > f.config.MaxBytes {
		return nil, &Error{Code: CodeContentTooLarge, Message: "page exceeds the configured size limit"}
	}

	result := &Result{
		URL:         key,
		FetchedAt:   now.UTC(),
		ContentType: mediaType,
		Links:       []string{},
	}
	if mediaType == "text/plain" {
		result.Text = strings.TrimSpace(string(body))
		result.Title = firstLine(result.Text)
	} else {
		extracted := ExtractHTML(body, parsed)
		result.Title = extracted.Title
		result.Text = extracted.Text
		result.Links = extracted.Links
	}
	result.Excerpt = excerpt(result.Text, 300)
	result.WordCount = wordCount(result.Text)

	f.mu.Lock()
	f.cache[key] = cacheEntry{result: result, expires: now.Add(f.config.CacheTTL)}
	f.mu.Unlock()
	return result, nil
}

func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if len([]rune(trimmed)) > 120 {
				return string([]rune(trimmed)[:120])
			}
			return trimmed
		}
	}
	return ""
}

func excerpt(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "..."
}

func wordCount(text string) int {
	fields := strings.Fields(text)
	if len(fields) > 0 {
		return len(fields)
	}
	return 0
}

// validateURL enforces http(s), no userinfo and (unless allowPrivate) a
// public IP resolution.
func validateURL(ctx context.Context, raw string, allowPrivate bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return nil, &Error{Code: CodeInvalidURL, Message: "invalid URL"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, &Error{Code: CodeInvalidURL, Message: "URL must use http or https"}
	}
	if parsed.User != nil {
		return nil, &Error{Code: CodeInvalidURL, Message: "URL must not contain user information"}
	}
	if !allowPrivate {
		if _, err := resolvePublicIPs(ctx, parsed.Hostname()); err != nil {
			return nil, &Error{Code: CodeSSRFBlocked, Message: err.Error()}
		}
	}
	return parsed, nil
}
