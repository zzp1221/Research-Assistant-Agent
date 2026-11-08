package modelresolver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/schema"
	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/responses"
	"research-assistant-backend/internal/usage"
)

func runtimeFixture(t *testing.T, handler http.HandlerFunc) (*Resolver, modelprofile.Resolved, context.Context) {
	t.Helper()
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	t.Setenv("CONTEXT_STORE_DIR", t.TempDir())
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	p := modelprofile.Profile{ID: "profile", OwnerID: "owner", BaseURL: server.URL, Model: "model-v1", Revision: 1, ContextWindowTokens: 4000, MaxOutputTokens: 256, ContextPolicy: modelprofile.ContextPolicy{APIFormat: modelprofile.APIResponses, CompactionMode: modelprofile.CompactionStandalone, Capabilities: modelprofile.Capabilities{Responses: "yes", StandaloneCompaction: "yes", RenderedInputCount: "yes"}}}
	ctx := WithBranch(WithOwner(context.Background(), "owner"), "owner", "project", "session", "branch")
	return New(nil), modelprofile.Resolved{Profile: p, APIKey: "key"}, ctx
}

func nativeAnswer(w http.ResponseWriter, text string) {
	raw, _ := json.Marshal(map[string]any{"status": "completed", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": text}}}}})
	w.Write(raw)
}

func TestStandalonePersistsCanonicalWindowBeforeFailedGenerationAndRestart(t *testing.T) {
	compactCalls, createCalls, countCalls := 0, 0, 0
	firstAnswer := strings.Repeat("答", 2400)
	canonical := []responses.Item{json.RawMessage(`{"type":"message","role":"user","content":"retained","extra":123}`), json.RawMessage(`{"type":"compaction","encrypted_content":"opaque","future":{"v":1}}`)}
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/responses/input_tokens":
			countCalls++
			fmt.Fprint(w, `{"input_tokens":100}`)
		case "/responses/compact":
			compactCalls++
			json.NewEncoder(w).Encode(map[string]any{"object": "response.compaction", "output": canonical})
		case "/responses":
			createCalls++
			var request responses.Request
			_ = json.NewDecoder(req.Body).Decode(&request)
			if request.Store || request.Model != "model-v1" || request.MaxOutputTokens != 256 {
				t.Errorf("wrong resolved call: %#v", request)
			}
			if createCalls == 1 {
				nativeAnswer(w, firstAnswer)
				return
			}
			if len(request.Input) != 3 {
				t.Errorf("canonical prefix lost: %s", request.Input)
			} else {
				for i := range canonical {
					var a, b any
					_ = json.Unmarshal(canonical[i], &a)
					_ = json.Unmarshal(request.Input[i], &b)
					aa, _ := json.Marshal(a)
					bb, _ := json.Marshal(b)
					if string(aa) != string(bb) {
						t.Errorf("canonical item %d rewritten", i)
					}
				}
			}
			if createCalls == 2 {
				http.Error(w, "unavailable", 503)
				return
			}
			nativeAnswer(w, "recovered")
		default:
			t.Errorf("unexpected endpoint %s", req.URL.Path)
		}
	})
	input := []oaichat.Message{{Role: "system", Text: "immutable constraints"}, {Role: "user", Text: strings.Repeat("研", 2250)}}
	first, err := r.CompleteManaged(ctx, call, input, oaichat.Options{})
	if err != nil {
		t.Fatal(err)
	}
	input = append(input, oaichat.Message{Role: "assistant", Text: first.Content}, oaichat.Message{Role: "user", Text: "continue"})
	if _, err = r.CompleteManaged(ctx, call, input, oaichat.Options{}); err == nil {
		t.Fatal("expected generation failure")
	}
	if compactCalls != 1 || createCalls != 2 {
		t.Fatalf("calls compact=%d create=%d", compactCalls, createCalls)
	}
	restarted := New(nil)
	result, err := restarted.CompleteManaged(ctx, call, input, oaichat.Options{})
	if err != nil || result.Content != "recovered" || compactCalls != 1 || createCalls != 3 || countCalls != 2 {
		t.Fatalf("restart result %#v, %v; calls=%d/%d/%d", result, err, compactCalls, createCalls, countCalls)
	}
}

func TestServerModeKeepsItemsAfterLatestCompactionAndNeverCallsCompact(t *testing.T) {
	creates := 0
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/responses/input_tokens" {
			fmt.Fprint(w, `{"input_tokens":123}`)
			return
		}
		if req.URL.Path != "/responses" {
			t.Errorf("server mode called %s", req.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		creates++
		var request responses.Request
		_ = json.NewDecoder(req.Body).Decode(&request)
		if len(request.ContextManagement) != 1 || request.ContextManagement[0].CompactThreshold != 2835 {
			t.Errorf("wrong derived threshold: %#v", request.ContextManagement)
		}
		if creates == 2 {
			if len(request.Input) != 4 || !strings.Contains(string(request.Input[0]), "latest") || !strings.Contains(string(request.Input[1]), "keep-after") {
				t.Errorf("server prefix trimming lost later items: %s", request.Input)
			}
		}
		fmt.Fprint(w, `{"status":"completed","output":[{"type":"compaction","encrypted_content":"old"},{"type":"compaction","encrypted_content":"latest"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"keep-after"}]},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`)
	})
	call.Profile.CompactionMode = modelprofile.CompactionServer
	call.Profile.Capabilities.ServerCompaction = "yes"
	recorder := usage.NewRecorder()
	ctx = usage.WithRecorder(ctx, recorder)
	var report contextwindow.Report
	ctx = contextwindow.WithReport(ctx, &report)
	input := []oaichat.Message{{Role: "user", Text: "first"}}
	first, err := r.CompleteManaged(ctx, call, input, oaichat.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !first.CostUnknown || report.LastCompaction == nil || report.LastCompaction.Status != "completed" || report.LastCompaction.Attempts != 0 || report.Requests.Generation != 1 || report.Requests.Compaction != 0 {
		t.Fatalf("server operation or request overhead was misreported: %#v", report)
	}
	input = append(input, oaichat.Message{Role: "assistant", Text: first.Content}, oaichat.Message{Role: "user", Text: "next"})
	if _, err := r.CompleteManaged(ctx, call, input, oaichat.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestServerCompactionIsNotRequestedUnderFiniteCostBudget(t *testing.T) {
	creates := 0
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/responses" {
			t.Errorf("unexpected request: %s", req.URL.Path)
		}
		creates++
		var request responses.Request
		_ = json.NewDecoder(req.Body).Decode(&request)
		if len(request.ContextManagement) != 0 {
			t.Error("server compaction requested with unknown rate under finite cost cap")
		}
		nativeAnswer(w, "answer")
	})
	call.Profile.CompactionMode = modelprofile.CompactionServer
	call.Profile.Capabilities.ServerCompaction = "yes"
	ctx = contextwindow.WithLimits(ctx, contextwindow.Limits{CostConfigured: true, CostUSD: .01})
	var report contextwindow.Report
	ctx = contextwindow.WithReport(ctx, &report)
	response, err := r.CompleteManaged(ctx, call, []oaichat.Message{{Role: "user", Text: "continue"}}, oaichat.Options{})
	if err != nil || response.CostUnknown || creates != 1 || report.LastCompaction == nil || report.LastCompaction.Status != "budget_blocked" {
		t.Fatalf("budget-safe generation failed: response=%#v report=%#v err=%v", response, report, err)
	}
}

func TestServerCompactionCombinedUsageDoesNotUseGenerationPrices(t *testing.T) {
	t.Setenv("LLM_PRICE_PROMPT_PER_1K_USD", "100")
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `{"status":"completed","output":[{"type":"compaction","encrypted_content":"opaque"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}],"usage":{"input_tokens":100,"output_tokens":30}}`)
	})
	call.Profile.CompactionMode = modelprofile.CompactionServer
	call.Profile.Capabilities.ServerCompaction = "yes"
	call.Profile.PromptPricePer1K = 100
	ctx = WithResolvedCall(ctx, call)
	recorder := usage.NewRecorder()
	ctx = usage.WithRecorder(ctx, recorder)
	input := []*schema.Message{{Role: schema.User, Content: "continue"}}
	response, err := r.GenerateSchema(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	llmclient.RecordUsage(ctx, input, response, 0)
	got := recorder.Snapshot()
	if got.LLMCalls != 1 || got.PromptTokens != 100 || got.CompletionTokens != 30 || got.Compaction.Calls != 0 || got.Compaction.CostSource != "unknown" || got.CostSource != "unknown" || got.CostUSD != 0 {
		t.Fatalf("combined usage inferred a compaction price or extra call: %#v", got)
	}
}

func TestOpaqueUnknownNeverUsesCiphertextLength(t *testing.T) {
	compactCalls, creates := 0, 0
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/responses/compact" {
			compactCalls++
			fmt.Fprintf(w, `{"object":"response.compaction","output":[{"type":"compaction","encrypted_content":%q}]}`, strings.Repeat("cipher", 10000))
			return
		}
		creates++
		nativeAnswer(w, strings.Repeat("答", 2400))
	})
	call.Profile.Capabilities.RenderedInputCount = "unknown"
	input := []oaichat.Message{{Role: "user", Text: strings.Repeat("研", 2250)}}
	first, err := r.CompleteManaged(ctx, call, input, oaichat.Options{})
	if err != nil {
		t.Fatal(err)
	}
	input = append(input, oaichat.Message{Role: "assistant", Text: first.Content}, oaichat.Message{Role: "user", Text: "continue"})
	for i := 0; i < 2; i++ {
		_, err = New(nil).CompleteManaged(ctx, call, input, oaichat.Options{})
		if err == nil || !strings.Contains(err.Error(), "context_size_unknown") {
			t.Fatalf("opaque token count fabricated: %v", err)
		}
	}
	if compactCalls != 1 || creates != 1 {
		t.Fatalf("repeated compaction or invalid generation: %d/%d", compactCalls, creates)
	}
}

func TestConcurrentIdenticalTurnIsSingleflightAndOversizedNewInputDoesNotCompact(t *testing.T) {
	var calls atomic.Int32
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) { calls.Add(1); nativeAnswer(w, "answer") })
	input := []oaichat.Message{{Role: "user", Text: "question"}}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.CompleteManaged(ctx, call, input, oaichat.Options{}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("duplicate paid requests: %d", calls.Load())
	}
	input = append(input, oaichat.Message{Role: "assistant", Text: "answer"}, oaichat.Message{Role: "user", Text: strings.Repeat("研", 7500)})
	if _, err := r.CompleteManaged(ctx, call, input, oaichat.Options{}); err == nil || !strings.Contains(err.Error(), "context_required_items_too_large") {
		t.Fatalf("oversized current input accepted: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("oversized request called provider")
	}
}

func TestModelChangeRebuildsFromVisibleHistoryWithoutOpaqueForwarding(t *testing.T) {
	creates := 0
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		creates++
		var request responses.Request
		_ = json.NewDecoder(req.Body).Decode(&request)
		if creates == 1 {
			fmt.Fprint(w, `{"status":"completed","output":[{"type":"compaction","encrypted_content":"private"},{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]}]}`)
			return
		}
		if responses.HasOpaque(request.Input) || request.Model != "model-v2" {
			t.Error("incompatible opaque state forwarded")
		}
		nativeAnswer(w, "new")
	})
	input := []oaichat.Message{{Role: "user", Text: "question"}}
	first, err := r.CompleteManaged(ctx, call, input, oaichat.Options{})
	if err != nil {
		t.Fatal(err)
	}
	input = append(input, oaichat.Message{Role: "assistant", Text: first.Content}, oaichat.Message{Role: "user", Text: "continue"})
	call.Profile.Model = "model-v2"
	call.Profile.Revision++
	if _, err := r.CompleteManaged(ctx, call, input, oaichat.Options{}); err != nil {
		t.Fatal(err)
	}
}

func TestPinnedCallSurvivesProfileEdit(t *testing.T) {
	store := newTestStore(t)
	p, err := store.Create("a", modelprofile.Input{Name: "p", BaseURL: "https://example.com/v1", Model: "before", APIKey: "key", Roles: []string{"main"}, ContextWindowTokens: 32000}, 10)
	if err != nil {
		t.Fatal(err)
	}
	r := New(store)
	call, err := r.ResolveText("a")
	if err != nil {
		t.Fatal(err)
	}
	ctx := WithResolvedCall(WithOwner(context.Background(), "a"), call)
	if _, err := store.Update("a", p.ID, modelprofile.Input{Model: "after", ContextWindowTokens: 200000}); err != nil {
		t.Fatal(err)
	}
	pinned, _ := r.ResolveCall(ctx)
	next, _ := r.ResolveCall(WithOwner(context.Background(), "a"))
	b, _ := contextwindow.ResolveBudget(pinned.Profile, 0)
	nextBudget, _ := contextwindow.ResolveBudget(next.Profile, 0)
	if pinned.Profile.Revision != 1 || pinned.Profile.Model != "before" || b.ContextWindow != 32000 || next.Profile.Revision != 2 || nextBudget.ContextWindow != 200000 {
		t.Fatal("budget and call revisions diverged")
	}
}

func TestTextSummaryValidatedOnceAndKeepsFrozenRequestAndLatestFeedback(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(fmt.Sprint(valid), func(t *testing.T) {
			summaries, generations := 0, 0
			r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path != "/chat/completions" {
					t.Errorf("text mode changed protocol: %s", req.URL.Path)
				}
				var request struct {
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
					ResponseFormat map[string]string `json:"response_format"`
				}
				_ = json.NewDecoder(req.Body).Decode(&request)
				text := "answer"
				if request.ResponseFormat != nil {
					summaries++
					var material struct {
						Items []struct {
							ID string `json:"id"`
						} `json:"new_material"`
					}
					_ = json.Unmarshal([]byte(request.Messages[1].Content), &material)
					id := "invented"
					if valid && len(material.Items) > 0 {
						id = material.Items[0].ID
					}
					text = fmt.Sprintf(`{"version":"context.summary/v1","goal":"continue","constraints":[],"facts":[],"decisions":[],"attempts":[{"action":"failed repair","outcome":"test failed","source_ids":[%q]}],"hypotheses":[],"open_questions":[],"next_steps":[],"artifact_refs":[]}`, id)
				} else {
					generations++
					joined := ""
					for _, m := range request.Messages {
						joined += m.Content
					}
					if !strings.Contains(joined, "FROZEN split=80/20") || !strings.Contains(joined, "LATEST runnable patch") || !strings.Contains(joined, "CURRENT request") {
						t.Error("required current state lost")
					}
					if valid && !strings.Contains(joined, "context.summary/v1") {
						t.Error("summary not injected")
					}
					if !valid && !strings.Contains(joined, "OLD attempt") {
						t.Error("invalid summary discarded original evidence")
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": text}}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20}})
			})
			call.Profile.APIFormat = modelprofile.APIChat
			call.Profile.CompactionMode = modelprofile.CompactionText
			input := []oaichat.Message{{Role: "system", Text: "authoritative instructions"}, {Role: "user", Text: "FROZEN split=80/20"}, {Role: "assistant", Text: "OLD attempt " + strings.Repeat("研", 2250)}, {Role: "user", Text: "OLD feedback " + strings.Repeat("研", 2250)}, {Role: "assistant", Text: "LATEST runnable patch"}, {Role: "user", Text: "CURRENT request"}}
			response, err := r.CompleteManaged(ctx, call, input, oaichat.Options{})
			if err != nil || response.Content != "answer" || summaries != 1 || generations != 1 {
				t.Fatalf("summary flow: %#v %v %d/%d", response, err, summaries, generations)
			}
		})
	}
}

func TestCompactionCannotSpendUnknownCostUnderFiniteBudget(t *testing.T) {
	compacts, creates := 0, 0
	r, call, ctx := runtimeFixture(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/responses/compact" {
			compacts++
			http.Error(w, "must not call", 500)
			return
		}
		creates++
		nativeAnswer(w, "answer")
	})
	ctx = contextwindow.WithLimits(ctx, contextwindow.Limits{CostConfigured: true, CostUSD: .01})
	input := []oaichat.Message{{Role: "user", Text: strings.Repeat("研", 2250)}, {Role: "assistant", Text: strings.Repeat("答", 2400)}, {Role: "user", Text: "continue"}}
	if _, err := r.CompleteManaged(ctx, call, input, oaichat.Options{}); err != nil {
		t.Fatal(err)
	}
	if compacts != 0 || creates != 1 {
		t.Fatalf("budget gate invoked compact: %d/%d", compacts, creates)
	}
}
