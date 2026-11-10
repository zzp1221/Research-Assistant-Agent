package oaichat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

// ToolDefinition is protocol data, independent of registry permissions.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}
type chatTool struct {
	Type     string         `json:"type"`
	Function ToolDefinition `json:"function"`
}

var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type HTTPError struct {
	Status     int
	RetryAfter string
}

func (e *HTTPError) Error() string   { return fmt.Sprintf("model endpoint returned HTTP %d", e.Status) }
func (e *HTTPError) HTTPStatus() int { return e.Status }
func (e *HTTPError) RetryDelay() time.Duration {
	if n, err := strconv.Atoi(e.RetryAfter); err == nil && n >= 0 {
		return time.Duration(n) * time.Second
	}
	if at, err := http.ParseTime(e.RetryAfter); err == nil && time.Until(at) > 0 {
		return time.Until(at)
	}
	return 0
}

// ValidateToolCalls rejects interrupted arguments before any side effect.
func ValidateToolCalls(calls []ToolCall) error {
	seen := map[string]bool{}
	for _, call := range calls {
		var arguments map[string]any
		if call.ID == "" || seen[call.ID] || call.Type != "function" || !toolNamePattern.MatchString(call.Function.Name) || json.Unmarshal([]byte(call.Function.Arguments), &arguments) != nil || arguments == nil {
			return fmt.Errorf("model_tool_calls_invalid")
		}
		seen[call.ID] = true
	}
	return nil
}
