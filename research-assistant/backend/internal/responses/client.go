// Package responses preserves native protocol items separately from visible text.
package responses

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/recovery"
	"strings"
)

type Item = json.RawMessage
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}
type Result struct {
	ID     string          `json:"id"`
	Object string          `json:"object"`
	Status string          `json:"status"`
	Output []Item          `json:"output"`
	Usage  *Usage          `json:"usage,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}
type Request struct {
	Model             string           `json:"model"`
	Input             []Item           `json:"input"`
	Instructions      string           `json:"instructions,omitempty"`
	MaxOutputTokens   int              `json:"max_output_tokens,omitempty"`
	Store             bool             `json:"store"`
	Stream            bool             `json:"stream,omitempty"`
	Include           []string         `json:"include,omitempty"`
	ContextManagement []Management     `json:"context_management,omitempty"`
	Tools             []ToolDefinition `json:"tools,omitempty"`
}
type ToolDefinition struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}
type Management struct {
	Type             string `json:"type"`
	CompactThreshold int    `json:"compact_threshold"`
}
type Client struct{ HTTP *http.Client }

func (c Client) post(ctx context.Context, base, key, path string, payload any) (*http.Response, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	if err := recovery.HTTPAttempt(ctx, false); err != nil {
		return nil, err
	}
	res, err := c.HTTP.Do(req) // Exactly one attempt, including compact and count.
	if err != nil {
		return nil, fmt.Errorf("responses_transport_unknown: request outcome unknown")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		res.Body.Close()
		return nil, &oaichat.HTTPError{Status: res.StatusCode, RetryAfter: res.Header.Get("Retry-After")} // Never echo provider input/ciphertext.
	}
	return res, nil
}

func (c Client) Create(ctx context.Context, base, key string, request Request) (*Result, error) {
	res, err := c.post(ctx, base, key, "/responses", request)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var result Result
	if strings.Contains(res.Header.Get("Content-Type"), "text/event-stream") {
		result, err = readStream(res.Body)
	} else {
		err = json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&result)
	}
	if err != nil {
		return nil, fmt.Errorf("responses_incomplete: invalid or interrupted response")
	}
	if result.Status != "completed" || (len(result.Error) > 0 && string(result.Error) != "null") {
		return nil, fmt.Errorf("responses_incomplete: completion not committed")
	}
	if err := ValidateItems(result.Output); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c Client) Compact(ctx context.Context, base, key, model, instructions string, input []Item) (*Result, error) {
	res, err := c.post(ctx, base, key, "/responses/compact", struct {
		Model        string `json:"model"`
		Input        []Item `json:"input"`
		Instructions string `json:"instructions,omitempty"`
	}{model, input, instructions})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var result Result
	if json.NewDecoder(io.LimitReader(res.Body, 32<<20)).Decode(&result) != nil || result.Object != "response.compaction" {
		return nil, fmt.Errorf("compaction_invalid: incomplete compaction envelope")
	}
	if err := ValidateItems(result.Output); err != nil {
		return nil, err
	}
	if !HasOpaque(result.Output) {
		return nil, fmt.Errorf("compaction_invalid: missing compaction item")
	}
	return &result, nil
}

func (c Client) Count(ctx context.Context, base, key string, request Request) (int, error) {
	// Only used when the deployment explicitly confirms this optional endpoint.
	res, err := c.post(ctx, base, key, "/responses/input_tokens", struct {
		Model        string           `json:"model"`
		Input        []Item           `json:"input"`
		Instructions string           `json:"instructions,omitempty"`
		Tools        []ToolDefinition `json:"tools,omitempty"`
	}{request.Model, request.Input, request.Instructions, request.Tools})
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	var count struct {
		Tokens *int `json:"input_tokens"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&count) != nil || count.Tokens == nil || *count.Tokens < 0 {
		return 0, fmt.Errorf("context_size_unknown")
	}
	return *count.Tokens, nil
}

func ValidateItems(items []Item) error {
	if len(items) == 0 {
		return fmt.Errorf("responses_invalid: empty output")
	}
	for _, item := range items {
		var value struct {
			Type      string          `json:"type"`
			Encrypted string          `json:"encrypted_content"`
			Content   json.RawMessage `json:"content"`
		}
		if json.Unmarshal(item, &value) != nil || value.Type == "" {
			return fmt.Errorf("responses_invalid: item type missing")
		}
		if value.Type == "compaction" && value.Encrypted == "" {
			return fmt.Errorf("responses_invalid: empty compaction state")
		}
		if value.Type == "message" && (len(value.Content) == 0 || string(value.Content) == "null") {
			return fmt.Errorf("responses_invalid: missing message content")
		}
	}
	return nil
}

func HasOpaque(items []Item) bool {
	for _, item := range items {
		var value struct {
			Type      string `json:"type"`
			Encrypted string `json:"encrypted_content"`
		}
		_ = json.Unmarshal(item, &value)
		if value.Type == "compaction" || value.Encrypted != "" {
			return true
		}
	}
	return false
}

func Text(items []Item) string {
	var text strings.Builder
	for _, item := range items {
		var value struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(item, &value) != nil || value.Type != "message" {
			continue
		}
		for _, part := range value.Content {
			if part.Type == "output_text" {
				text.WriteString(part.Text)
			}
		}
	}
	return text.String()
}

// Only the final completed envelope is authoritative. Partial deltas never
// become a committed window; unknown event fields remain in its native items.
func readStream(reader io.Reader) (Result, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 64<<20))
	scanner.Buffer(make([]byte, 4096), 32<<20)
	var data strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			data.WriteByte('\n')
			continue
		}
		if line != "" || data.Len() == 0 {
			continue
		}
		var event struct {
			Type     string `json:"type"`
			Response Result `json:"response"`
		}
		err := json.Unmarshal([]byte(data.String()), &event)
		data.Reset()
		if err != nil {
			return Result{}, err
		}
		if event.Type == "response.completed" {
			return event.Response, nil
		}
		if event.Type == "response.failed" || event.Type == "error" || event.Type == "response.incomplete" {
			return Result{}, fmt.Errorf("responses_incomplete")
		}
	}
	return Result{}, fmt.Errorf("responses_incomplete: missing completion event")
}
