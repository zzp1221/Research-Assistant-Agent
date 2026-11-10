package modelresolver

import (
	"encoding/json"
	"fmt"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/responses"
)

func responseToolCalls(items []responses.Item) ([]oaichat.ToolCall, error) {
	var calls []oaichat.ToolCall
	for _, item := range items {
		var value struct {
			Type      string `json:"type"`
			Status    string `json:"status"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(item, &value) != nil {
			return nil, fmt.Errorf("model_tool_calls_invalid")
		}
		if value.Type != "function_call" {
			continue
		}
		if value.Status != "" && value.Status != "completed" {
			return nil, fmt.Errorf("model_tool_calls_incomplete")
		}
		calls = append(calls, oaichat.ToolCall{ID: value.CallID, Type: "function", Function: oaichat.ToolFunction{Name: value.Name, Arguments: value.Arguments}})
	}
	return calls, oaichat.ValidateToolCalls(calls)
}
