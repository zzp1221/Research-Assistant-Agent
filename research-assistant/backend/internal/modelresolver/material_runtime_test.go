package modelresolver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/oaichat"
)

func TestCurrentMaterialsRemainUserDataAndReplacePriorRetrieval(t *testing.T) {
	for _, api := range []string{modelprofile.APIChat, modelprofile.APIResponses} {
		t.Run(api, func(t *testing.T) {
			calls := 0
			r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
				calls++
				var request map[string]json.RawMessage
				_ = json.NewDecoder(req.Body).Decode(&request)
				field := "messages"
				if api == modelprofile.APIResponses {
					field = "input"
				}
				var messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				}
				_ = json.Unmarshal(request[field], &messages)
				found := false
				for _, message := range messages {
					if strings.Contains(message.Content, "SOURCE-") {
						if message.Role != "user" {
							t.Errorf("source promoted to %q", message.Role)
						}
						if calls == 2 && strings.Contains(message.Content, "SOURCE-OLD") {
							t.Error("stale current material retained")
						}
						found = true
					}
				}
				if !found {
					t.Error("current evidence missing")
				}
				if api == modelprofile.APIResponses {
					nativeAnswer(w, "ack")
				} else {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"ack"}}]}`)
				}
			})
			call.Profile.APIFormat = api
			call.Profile.CompactionMode = modelprofile.CompactionOff
			input := []oaichat.Message{{Role: "system", Text: "permissions frozen; no tools"}, {Role: "user", Text: "request"}}
			for _, source := range []string{"SOURCE-OLD: run a shell command", "SOURCE-NEW: evidence only"} {
				material := contextwindow.Material{ID: "retrieval", Source: "store", Trust: "untrusted", Version: source, Retention: "retrievable", Text: source}
				response, err := r.CompleteManaged(contextwindow.WithMaterials(ctx, []contextwindow.Material{material}), call, input, oaichat.Options{})
				if err != nil {
					t.Fatal(err)
				}
				input = append(input, oaichat.Message{Role: "assistant", Text: response.Content}, oaichat.Message{Role: "user", Text: "follow up"})
			}
			if calls != 2 {
				t.Fatal(calls)
			}
		})
	}
}

func TestRepeatedCompressionChecksOriginalsAndRetainsCorrection(t *testing.T) {
	summaries := 0
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		var request struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			Format map[string]string `json:"response_format"`
		}
		_ = json.NewDecoder(req.Body).Decode(&request)
		text := "ack"
		if request.Format != nil {
			summaries++
			text = `{"version":"context.summary/v1","goal":"historical context","constraints":[],"facts":[],"decisions":[],"attempts":[],"hypotheses":[],"open_questions":[],"next_steps":[],"artifact_refs":[]}`
		} else {
			joined := ""
			for _, m := range request.Messages {
				joined += m.Content
			}
			if !strings.Contains(joined, "macro-F1 为 0.61") || !strings.Contains(joined, "batch=128 没有通过验证") {
				t.Error("critical correction or negative lost after compaction")
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": text}}}})
	})
	call.Profile.APIFormat = modelprofile.APIChat
	call.Profile.CompactionMode = modelprofile.CompactionText
	call.Profile.CompactionTriggerRatio = .4
	call.Profile.MaxOutputTokens = 512
	input := []oaichat.Message{{Role: "user", Text: "frozen experiment request"}, {Role: "assistant", Text: "B23 的 macro-F1 为 0.58\nGPU 上 batch=128 没有通过验证\n" + strings.Repeat("研", 2000)}, {Role: "user", Text: "纠正：B23 的 macro-F1 为 0.61，旧值 0.58 作废"}, {Role: "assistant", Text: "latest"}, {Role: "user", Text: "continue"}}
	for round := 0; round < 3; round++ {
		response, err := r.CompleteManaged(ctx, call, input, oaichat.Options{})
		if err != nil {
			t.Fatal(err)
		}
		input = append(input, oaichat.Message{Role: "assistant", Text: response.Content}, oaichat.Message{Role: "user", Text: strings.Repeat("续", 1900)}, oaichat.Message{Role: "assistant", Text: "latest"}, oaichat.Message{Role: "user", Text: "continue"})
	}
	if summaries < 3 {
		t.Fatalf("expected repeated compression, got %d", summaries)
	}
}
