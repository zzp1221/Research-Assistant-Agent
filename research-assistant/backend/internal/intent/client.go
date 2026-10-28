package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Classification is the response returned by the Python intent service.
type Classification struct {
	IntentType string         `json:"intent_type"`
	Confidence float64        `json:"confidence"`
	Entities   map[string]any `json:"entities"`
	Reasoning  string         `json:"reasoning,omitempty"`
	// Optional complexity fields returned by newer intent services. Missing
	// fields fall back to the Go-side rule router.
	Complexity           string   `json:"complexity,omitempty"`
	Capabilities         []string `json:"capabilities,omitempty"`
	RouteHint            string   `json:"route_hint,omitempty"`
	ComplexityConfidence float64  `json:"complexity_confidence,omitempty"`
	ComplexityReason     string   `json:"complexity_reason,omitempty"`
	Model                string   `json:"model"`
	LatencyMS            int      `json:"latency_ms"`
	Attempts             int      `json:"attempts"`
}

// Classifier is the small seam used by the resolver and by tests.
type Classifier interface {
	Classify(ctx context.Context, text string) (*Classification, error)
}

// HTTPClassifier calls the optional Python intent-recognition service.
type HTTPClassifier struct {
	baseURL string
	client  *http.Client
}

func NewHTTPClassifier(baseURL string, timeout time.Duration) *HTTPClassifier {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &HTTPClassifier{
		baseURL: baseURL,
		client:  &http.Client{Timeout: timeout},
	}
}

// NewHTTPClassifierFromEnv builds a client from INTENT_SERVICE_URL and
// INTENT_CLIENT_TIMEOUT_SECONDS. An empty URL disables the model classifier.
func NewHTTPClassifierFromEnv() *HTTPClassifier {
	baseURL := strings.TrimSpace(os.Getenv("INTENT_SERVICE_URL"))
	timeout := 15 * time.Second
	if raw := strings.TrimSpace(os.Getenv("INTENT_CLIENT_TIMEOUT_SECONDS")); raw != "" {
		if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds > 0 {
			timeout = time.Duration(seconds * float64(time.Second))
		}
	}
	return NewHTTPClassifier(baseURL, timeout)
}

func (c *HTTPClassifier) Classify(ctx context.Context, text string) (*Classification, error) {
	if c == nil {
		return nil, fmt.Errorf("intent classifier is not configured")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("intent text is empty")
	}

	body, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return nil, fmt.Errorf("marshal intent request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/predict", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create intent request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("intent service request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("intent service status %d: %s", resp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var result Classification
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode intent response: %w", err)
	}
	if strings.TrimSpace(result.IntentType) == "" {
		return nil, fmt.Errorf("intent service returned an empty intent_type")
	}
	if result.Entities == nil {
		result.Entities = map[string]any{}
	}
	return &result, nil
}
