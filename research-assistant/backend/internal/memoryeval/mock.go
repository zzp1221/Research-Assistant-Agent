package memoryeval

import (
	"encoding/json"
	"net/http"
	"strings"

	"research-assistant-backend/internal/contextwindow"
)

// MockHandler tests the pipeline by reading only actual provider input. It
// never reads Task.Expected, so a scope leak or a dropped anchor changes output.
// Its results verify mechanics, not a language model's reasoning quality.
func MockHandler(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if json.NewDecoder(r.Body).Decode(&request) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	content := `{"version":"context.summary/v1","goal":"historical context","constraints":[],"facts":[],"decisions":[],"attempts":[],"hypotheses":[],"open_questions":[],"next_steps":[],"artifact_refs":[]}`
	if len(request.Messages) == 0 || request.Messages[0].Content != contextwindow.SummaryInstruction {
		facts := map[string]Fact{}
		var keys []string
		var walk func(any)
		walk = func(value any) {
			switch value := value.(type) {
			case map[string]any:
				if subject, ok := value["subject"].(string); ok {
					raw, _ := json.Marshal(value)
					var fact Fact
					_ = json.Unmarshal(raw, &fact)
					if fact.SourceID != "" && fact.Status != "" {
						facts[subject] = fact
					}
				}
				if wanted, ok := value["keys"].([]any); ok {
					keys = nil
					for _, key := range wanted {
						if key, ok := key.(string); ok {
							keys = append(keys, key)
						}
					}
				}
				// Sort-sensitive source arrays are visited in original order;
				// metadata object keys carry no chronological meaning.
				for _, key := range []string{"text", "quote", "new_material", "original_critical_anchors"} {
					if nested, ok := value[key]; ok {
						walk(nested)
					}
				}
			case []any:
				for _, nested := range value {
					walk(nested)
				}
			case string:
				for _, line := range strings.Split(value, "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "{") || strings.HasPrefix(line, "[") {
						var nested any
						if json.Unmarshal([]byte(line), &nested) == nil {
							walk(nested)
						}
					}
				}
			}
		}
		for _, message := range request.Messages {
			if message.Role != "system" {
				walk(message.Content)
			}
		}
		answer := make([]Fact, 0, len(keys))
		for _, key := range keys {
			fact, ok := facts[key]
			if !ok {
				fact = Fact{Subject: key, Status: "unknown"}
			}
			fact.Owner, fact.Project, fact.Lifecycle = "", "", ""
			answer = append(answer, fact)
		}
		raw, _ := json.Marshal(map[string]any{"facts": answer})
		content = string(raw)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}, "finish_reason": "stop"}}})
}
