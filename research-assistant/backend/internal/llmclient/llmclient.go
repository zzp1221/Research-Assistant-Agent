// Package llmclient centralizes HTTP retry, exponential backoff and per-attempt
// timeouts for all production LLM calls.
package llmclient

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/openai"
)

// Config controls retry and timeout behavior for one LLM HTTP client.
type Config struct {
	MaxAttempts    int
	BaseDelay      time.Duration
	MaxDelay       time.Duration
	AttemptTimeout time.Duration
}

// DefaultConfig reads optional environment overrides and otherwise returns
// conservative production defaults: 3 attempts, 500ms exponential backoff,
// 90s per-attempt timeout.
func DefaultConfig() Config {
	return normalizeConfig(Config{
		MaxAttempts:    envInt("LLM_MAX_ATTEMPTS", 3, 1, 8),
		BaseDelay:      time.Duration(envInt("LLM_RETRY_BASE_MS", 500, 10, 60000)) * time.Millisecond,
		MaxDelay:       time.Duration(envInt("LLM_RETRY_MAX_MS", 8000, 10, 120000)) * time.Millisecond,
		AttemptTimeout: time.Duration(envInt("LLM_ATTEMPT_TIMEOUT_SECONDS", 180, 5, 600)) * time.Second,
	})
}

func normalizeConfig(cfg Config) Config {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 500 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 8 * time.Second
	}
	if cfg.MaxDelay < cfg.BaseDelay {
		cfg.MaxDelay = cfg.BaseDelay
	}
	if cfg.AttemptTimeout <= 0 {
		cfg.AttemptTimeout = 90 * time.Second
	}
	return cfg
}

// NewChatModel creates an Eino OpenAI-compatible chat model with the shared
// retry transport unless the caller explicitly provides an HTTP client.
func NewChatModel(ctx context.Context, cfg *openai.ChatModelConfig) (*openai.ChatModel, error) {
	if cfg == nil {
		return nil, fmt.Errorf("chat model config is nil")
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = NewHTTPClient(DefaultConfig())
	}
	return openai.NewChatModel(ctx, cfg)
}

// NewHTTPClient returns an HTTP client whose transport retries transient
// failures (429, 408, 425, 500, 502, 503, 504 and network errors) with
// exponential backoff and a per-attempt timeout.
func NewHTTPClient(cfg Config) *http.Client {
	cfg = normalizeConfig(cfg)
	return &http.Client{
		Transport: &retryTransport{base: http.DefaultTransport, cfg: cfg},
	}
}

type retryTransport struct {
	base http.RoundTripper
	cfg  Config
}

func (t *retryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("request is nil")
	}
	body, err := readAndReplaceBody(req)
	if err != nil {
		return nil, err
	}

	var lastErr error
	lastStatus := 0
	for attempt := 1; attempt <= t.cfg.MaxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(req.Context(), t.cfg.AttemptTimeout)
		attemptReq := req.Clone(attemptCtx)
		if body != nil {
			attemptReq.Body = io.NopCloser(bytes.NewReader(body))
			attemptReq.ContentLength = int64(len(body))
			attemptReq.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(body)), nil
			}
		}

		resp, roundErr := t.base.RoundTrip(attemptReq)
		if roundErr == nil && !retryableStatus(resp.StatusCode) {
			resp.Body = &cancelReadCloser{ReadCloser: resp.Body, cancel: cancel}
			return resp, nil
		}
		if resp != nil {
			lastStatus = resp.StatusCode
			_, _ = io.Copy(io.Discard, resp.Body)
			_ = resp.Body.Close()
		}
		if roundErr != nil {
			lastErr = roundErr
		}
		cancel()

		if req.Context().Err() != nil {
			return nil, req.Context().Err()
		}
		if attempt == t.cfg.MaxAttempts {
			break
		}
		delay := backoffDelay(t.cfg, attempt)
		if resp != nil {
			if retryAfter := parseRetryAfter(resp.Header.Get("Retry-After")); retryAfter > delay {
				delay = retryAfter
			}
		}
		log.Printf("[llmclient] retry %d/%d after %s (status=%d, error=%v)", attempt+1, t.cfg.MaxAttempts, delay, lastStatus, lastErr)
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-req.Context().Done():
			timer.Stop()
			return nil, req.Context().Err()
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("LLM request failed after %d attempt(s): %w", t.cfg.MaxAttempts, lastErr)
	}
	return nil, fmt.Errorf("LLM request failed after %d attempt(s): HTTP %d", t.cfg.MaxAttempts, lastStatus)
}

func readAndReplaceBody(req *http.Request) ([]byte, error) {
	if req.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("read LLM request body: %w", err)
	}
	_ = req.Body.Close()
	req.Body = io.NopCloser(bytes.NewReader(body))
	return body, nil
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func retryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func backoffDelay(cfg Config, attempt int) time.Duration {
	delay := cfg.BaseDelay
	for i := 1; i < attempt; i++ {
		delay *= 2
		if delay >= cfg.MaxDelay {
			return cfg.MaxDelay
		}
	}
	if delay > cfg.MaxDelay {
		return cfg.MaxDelay
	}
	return delay
}

func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(raw); err == nil {
		if wait := time.Until(when); wait > 0 {
			return wait
		}
	}
	return 0
}

func envInt(key string, fallback, minimum, maximum int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return fallback
	}
	return value
}
