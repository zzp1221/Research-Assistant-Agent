// Package oaichat is a minimal OpenAI-compatible chat-completions client used
// by model probes and the multimodal visual verifier. It deliberately supports
// only what those call sites need: text + image_url content parts, strict JSON
// responses and provider token usage.
package oaichat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// ImagePart is one inline image content part.
type ImagePart struct {
	MIMEType string
	Base64   string
}

// Message is one chat message. A user message may carry images.
type Message struct {
	Role   string
	Text   string
	Images []ImagePart
}

// Options tune one request.
type Options struct {
	Temperature        float64
	MaxOutputTokens    int
	Timeout            time.Duration
	ResponseFormatJSON bool
}

// Response is a normalized chat completion.
type Response struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	EstimatedTokens  bool
	Latency          time.Duration
}

// Client performs chat completions with an SSRF-safe transport.
type Client struct {
	httpClient *http.Client
}

// NewClient builds a client. When allowPrivate is false the dialer refuses
// loopback/private/link-local destinations, so a user-supplied base_url cannot
// turn into an internal-network probe.
func NewClient(allowPrivate bool) *Client {
	if allowPrivate {
		return &Client{httpClient: &http.Client{Transport: http.DefaultTransport}}
	}
	return &Client{httpClient: &http.Client{Transport: &guardedTransport{
		base:    http.DefaultTransport.(*http.Transport).Clone(),
		timeout: 30 * time.Second,
	}}}
}

type guardedTransport struct {
	base    *http.Transport
	timeout time.Duration
}

func (t *guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, fmt.Errorf("request is nil")
	}
	dialer := &net.Dialer{Timeout: t.timeout, KeepAlive: 30 * time.Second}
	transport := t.base.Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("invalid model endpoint address: %w", err)
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("cannot resolve model endpoint")
		}
		for _, candidate := range addresses {
			if blockedIP(candidate.IP) {
				return nil, fmt.Errorf("model endpoint resolves to a private or local address")
			}
		}
		var lastErr error
		for _, candidate := range addresses {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, fmt.Errorf("connect to model endpoint: %w", lastErr)
	}
	return transport.RoundTrip(req)
}

func blockedIP(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()
}

// AllowPrivateFromEnv reports whether the deployment opted into private model
// endpoints (only meant for local/container test harnesses).
func AllowPrivateFromEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_PRIVATE_MODEL_ENDPOINTS")), "true")
}

// ValidateEndpoint enforces the base_url policy: https by default, no
// credentials in the URL, no private hosts unless explicitly allowed.
func ValidateEndpoint(ctx context.Context, raw string, allowPrivate bool) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid base_url")
	}
	if parsed.User != nil {
		return nil, fmt.Errorf("base_url must not contain credentials")
	}
	allowHTTP := allowPrivate
	if !allowHTTP {
		allowHTTP = strings.EqualFold(strings.TrimSpace(os.Getenv("ALLOW_HTTP_LOCALHOST")), "true") &&
			(parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && allowHTTP) {
		return nil, fmt.Errorf("base_url must use https")
	}
	if !allowPrivate {
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
		if err != nil || len(addresses) == 0 {
			return nil, fmt.Errorf("cannot resolve base_url host")
		}
		for _, candidate := range addresses {
			if blockedIP(candidate.IP) {
				return nil, fmt.Errorf("base_url resolves to a private or local address")
			}
		}
	}
	return parsed, nil
}

type chatCompletionRequest struct {
	Model          string              `json:"model"`
	Messages       []completionMessage `json:"messages"`
	Temperature    *float64            `json:"temperature,omitempty"`
	MaxTokens      int                 `json:"max_tokens,omitempty"`
	ResponseFormat map[string]string   `json:"response_format,omitempty"`
}

type completionMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type contentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *imageURLPart `json:"image_url,omitempty"`
}

type imageURLPart struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}

type chatCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Complete calls {baseURL}/chat/completions.
func (c *Client) Complete(ctx context.Context, baseURL, apiKey, model string, messages []Message, opts Options) (*Response, error) {
	if c == nil {
		return nil, fmt.Errorf("chat client is nil")
	}
	endpoint := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/chat/completions"
	payload := chatCompletionRequest{Model: model}
	if opts.MaxOutputTokens > 0 {
		payload.MaxTokens = opts.MaxOutputTokens
	}
	if opts.Temperature > 0 {
		temperature := opts.Temperature
		payload.Temperature = &temperature
	}
	if opts.ResponseFormatJSON {
		payload.ResponseFormat = map[string]string{"type": "json_object"}
	}
	for _, message := range messages {
		if len(message.Images) == 0 {
			payload.Messages = append(payload.Messages, completionMessage{Role: message.Role, Content: message.Text})
			continue
		}
		parts := make([]contentPart, 0, len(message.Images)+1)
		if strings.TrimSpace(message.Text) != "" {
			parts = append(parts, contentPart{Type: "text", Text: message.Text})
		}
		for _, image := range message.Images {
			mime := image.MIMEType
			if mime == "" {
				mime = "image/png"
			}
			parts = append(parts, contentPart{Type: "image_url", ImageURL: &imageURLPart{
				URL:    "data:" + mime + ";base64," + image.Base64,
				Detail: "auto",
			}})
		}
		payload.Messages = append(payload.Messages, completionMessage{Role: message.Role, Content: parts})
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	}
	started := time.Now()
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if readErr != nil {
		return nil, readErr
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		trimmed := strings.TrimSpace(string(body))
		if len(trimmed) > 500 {
			trimmed = trimmed[:500]
		}
		return nil, fmt.Errorf("model endpoint returned HTTP %d: %s", resp.StatusCode, trimmed)
	}
	var parsed chatCompletionResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode model response: %w", err)
	}
	if parsed.Error != nil && strings.TrimSpace(parsed.Error.Message) != "" {
		return nil, fmt.Errorf("model endpoint error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("model response has no choices")
	}
	result := &Response{Content: parsed.Choices[0].Message.Content, Latency: time.Since(started)}
	if parsed.Usage != nil {
		result.PromptTokens = parsed.Usage.PromptTokens
		result.CompletionTokens = parsed.Usage.CompletionTokens
	} else {
		result.EstimatedTokens = true
		result.PromptTokens = estimateTokens(messages)
		result.CompletionTokens = estimateTokensText(result.Content)
	}
	return result, nil
}

func estimateTokens(messages []Message) int {
	total := 0
	for _, message := range messages {
		total += estimateTokensText(message.Text)
		// A downscaled 1280px image costs roughly 1000 tokens; use a fixed
		// estimate so cost reporting never silently drops image tokens.
		total += 1000 * len(message.Images)
	}
	return total
}

func estimateTokensText(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	runes := len([]rune(trimmed))
	if runes <= 4 {
		return 1
	}
	return runes / 4
}
