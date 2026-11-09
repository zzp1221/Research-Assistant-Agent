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
	"research-assistant-backend/internal/responses"
	"research-assistant-backend/internal/usage"
)

func TestResponsesToolContinuationPreservesNativeCallAcrossCompaction(t *testing.T) {
	creates, compacts := 0, 0
	const nativeCall = `{"type":"function_call","id":"native-item","call_id":"c1","name":"echo","arguments":"{}","status":"completed","provider_extra":42}`
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		var body responses.Request
		_ = json.NewDecoder(req.Body).Decode(&body)
		switch req.URL.Path {
		case "/responses/input_tokens":
			fmt.Fprint(w, `{"input_tokens":100}`)
		case "/responses/compact":
			compacts++
			for _, item := range body.Input {
				if strings.Contains(string(item), "function_call") {
					t.Error("pending tool call entered compaction source")
				}
			}
			fmt.Fprint(w, `{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":"opaque"}]}`)
		case "/responses":
			creates++
			if len(body.Tools) != 1 || body.Tools[0].Name != "echo" {
				t.Error("native tool definitions missing")
			}
			if creates == 1 {
				fmt.Fprintf(w, `{"status":"completed","output":[%s]}`, nativeCall)
				return
			}
			if len(body.Input) != 3 || string(body.Input[1]) != nativeCall || !strings.Contains(string(body.Input[2]), `"call_id":"c1"`) || !strings.Contains(string(body.Input[2]), "function_call_output") {
				t.Errorf("native correlation lost: %s", body.Input)
			}
			nativeAnswer(w, "done")
		}
	})
	call.Profile.Capabilities.ToolCalling = "yes"
	opts := oaichat.Options{Tools: []oaichat.ToolDefinition{{Name: "echo", Parameters: json.RawMessage(`{"type":"object"}`)}}}
	input := []oaichat.Message{{Role: "user", Text: strings.Repeat("研", 4300)}}
	first, err := r.CompleteManaged(ctx, call, input, opts)
	if err != nil || len(first.ToolCalls) != 1 || first.Content != "" {
		t.Fatalf("tool-only decision rejected: %#v %v", first, err)
	}
	input = append(input, oaichat.Message{Role: "assistant", ToolCalls: first.ToolCalls}, oaichat.Message{Role: "tool", ToolCallID: "c1", Text: strings.Repeat("果", 100)})
	final, err := New(nil).CompleteManaged(ctx, call, input, opts)
	if err != nil || final.Content != "done" || creates != 2 || compacts != 1 {
		t.Fatalf("continuation failed: %#v %v creates=%d compacts=%d", final, err, creates, compacts)
	}
}

func TestGenerationChecksCumulativeTokenAndUnknownCostAllowance(t *testing.T) {
	for _, format := range []string{modelprofile.APIChat, modelprofile.APIResponses} {
		t.Run(format, func(t *testing.T) {
			requests := 0
			r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
				requests++
				if format == modelprofile.APIChat {
					fmt.Fprint(w, `{"choices":[{"message":{"content":"answer"},"finish_reason":"stop"}]}`)
				} else {
					nativeAnswer(w, "answer")
				}
			})
			call.Profile.APIFormat, call.Profile.CompactionMode = format, modelprofile.CompactionOff
			recorder := usage.NewRecorder()
			recorder.RecordLLMUnknownCost("prior-step", 300, 100, true)
			ctx = usage.WithRecorder(ctx, recorder)
			baseCtx := ctx
			ctx = contextwindow.WithLimits(ctx, contextwindow.Limits{TokensConfigured: true, Tokens: 500})
			input := []oaichat.Message{{Role: "user", Text: "continue"}}
			if _, err := r.CompleteManaged(ctx, call, input, oaichat.Options{}); err == nil || !strings.Contains(err.Error(), "budget_exhausted") || requests != 0 {
				t.Fatalf("cumulative budget bypassed: %v requests=%d", err, requests)
			}
			ctx = contextwindow.WithLimits(baseCtx, contextwindow.Limits{CostConfigured: true, CostUSD: .01})
			if _, err := r.CompleteManaged(ctx, call, input, oaichat.Options{}); err == nil || !strings.Contains(err.Error(), "cost_unknown") || requests != 0 {
				t.Fatalf("unknown cost spent finite allowance: %v requests=%d", err, requests)
			}
		})
	}
}
