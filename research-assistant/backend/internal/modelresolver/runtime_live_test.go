package modelresolver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/responses"
	"research-assistant-backend/internal/usage"
)

// These tests are explicitly opt-in: ordinary go test never calls a paid model.
// Run the compiled test binary in the deployed container so credentials stay
// in that container and never need to be exported to the host or test logs.
func liveContextCall(t *testing.T) modelprofile.Resolved {
	t.Helper()
	if os.Getenv("RUN_CONTEXT_LIVE_TESTS") != "1" {
		t.Skip("set RUN_CONTEXT_LIVE_TESTS=1 to call the configured real provider")
	}
	store, err := modelprofile.NewStore(DefaultStoreDir())
	if err != nil {
		t.Fatal("load deployed model profiles failed")
	}
	call, err := New(store).ResolveText(os.Getenv("CONTEXT_LIVE_OWNER"))
	if err != nil || call.APIKey == "" {
		t.Fatal("resolve deployed model credential failed")
	}
	if override := strings.TrimSpace(os.Getenv("CONTEXT_LIVE_MODEL")); override != "" {
		t.Logf("test-only model override: %s -> %s", call.Profile.Model, override)
		call.Profile.Model = override
	}
	return call
}

func TestLiveContextInventory(t *testing.T) {
	call := liveContextCall(t)
	p := call.Profile
	data, _ := json.Marshal(map[string]any{
		"model": p.Model, "endpoint": p.BaseURL, "profile_id": p.ID,
		"revision": p.Revision, "api_format": p.APIFormat,
		"context_window_tokens": p.ContextWindowTokens, "context_window_source": p.ContextWindowSource,
		"max_output_tokens": p.MaxOutputTokens, "compaction_mode": p.CompactionMode,
		"capabilities": p.Capabilities, "credential_available": call.APIKey != "",
	})
	t.Log(string(data))
	if env, err := appconfig.LoadLLMConfig(); err == nil {
		t.Logf("environment model=%s endpoint=%s credential_available=%t", env.Model, env.BaseURL, env.APIKey != "")
	}
}

func TestLiveContextDiscoveryAndChat(t *testing.T) {
	call := liveContextCall(t)
	client := liveContextHTTP(t, call)
	request, _ := http.NewRequest("GET", strings.TrimRight(call.Profile.BaseURL, "/")+"/models", nil)
	request.Header.Set("Authorization", "Bearer "+call.APIKey)
	res, err := client.Do(request)
	if err != nil {
		t.Fatal("list deployed models failed")
	}
	var list struct {
		Data []struct {
			ID        string   `json:"id"`
			Endpoints []string `json:"supported_endpoint_types"`
		} `json:"data"`
	}
	_ = json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&list)
	res.Body.Close()
	if res.StatusCode == 200 {
		raw, _ := json.Marshal(list.Data)
		t.Logf("available_models=%s", raw)
	}
	body, _ := json.Marshal(map[string]any{"model": call.Profile.Model, "messages": []map[string]string{{"role": "user", "content": "Synthetic connection check. Reply exactly OK."}}, "max_tokens": 32})
	request, _ = http.NewRequest("POST", strings.TrimRight(call.Profile.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+call.APIKey)
	request.Header.Set("Content-Type", "application/json")
	res, err = client.Do(request)
	if err != nil {
		t.Fatal("chat transport failed")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("configured chat model HTTP %d", res.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage json.RawMessage `json:"usage"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&result) != nil || len(result.Choices) == 0 {
		t.Fatal("chat response invalid")
	}
	t.Logf("chat_answer=%q usage=%s", result.Choices[0].Message.Content, result.Usage)
}

type liveContextTransport struct {
	t    *testing.T
	base http.RoundTripper
	key  string
}

func (r *liveContextTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	started := time.Now()
	res, err := r.base.RoundTrip(req)
	if err != nil {
		r.t.Logf("%s %s transport_failed elapsed=%s", req.Method, req.URL.Path, time.Since(started).Round(time.Millisecond))
		return nil, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		res.Body.Close()
		res.Body = io.NopCloser(bytes.NewReader(raw))
		var envelope struct {
			Error struct {
				Code    any    `json:"code"`
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &envelope)
		message := strings.ReplaceAll(envelope.Error.Message, r.key, "[redacted]")
		if len(message) > 600 {
			message = message[:600]
		}
		r.t.Logf("%s %s status=%d code=%v type=%s message=%q read_error=%t elapsed=%s", req.Method, req.URL.Path, res.StatusCode, envelope.Error.Code, envelope.Error.Type, message, readErr != nil, time.Since(started).Round(time.Millisecond))
	} else {
		r.t.Logf("%s %s status=%d elapsed_to_headers=%s", req.Method, req.URL.Path, res.StatusCode, time.Since(started).Round(time.Millisecond))
	}
	return res, nil
}

func liveContextHTTP(t *testing.T, call modelprofile.Resolved) *http.Client {
	client := oaichat.NewClient(oaichat.AllowPrivateFromEnv()).HTTPClient()
	client.Timeout = 90 * time.Second
	client.Transport = &liveContextTransport{t: t, base: client.Transport, key: call.APIKey}
	return client
}

// This relay forwards every request to the actual provider unchanged. It only
// observes synthetic summary diagnostics; it never fabricates model responses.
func liveContextRelay(t *testing.T, call modelprofile.Resolved) modelprofile.Resolved {
	t.Helper()
	upstream := liveContextHTTP(t, call)
	base := strings.TrimRight(call.Profile.BaseURL, "/")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(io.LimitReader(req.Body, 4<<20))
		if err != nil {
			http.Error(w, "read failed", 500)
			return
		}
		forward, err := http.NewRequestWithContext(req.Context(), req.Method, base+req.URL.Path, bytes.NewReader(body))
		if err != nil {
			http.Error(w, "request failed", 500)
			return
		}
		forward.Header.Set("Authorization", "Bearer "+call.APIKey)
		forward.Header.Set("Content-Type", "application/json")
		res, err := upstream.Do(forward)
		if err != nil {
			http.Error(w, "upstream failed", 502)
			return
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
		if err != nil {
			http.Error(w, "response failed", 502)
			return
		}
		if bytes.Contains(body, []byte(`"response_format"`)) {
			var result struct {
				Choices []struct {
					FinishReason string `json:"finish_reason"`
					Message      struct {
						Content string `json:"content"`
					} `json:"message"`
				} `json:"choices"`
				Usage json.RawMessage `json:"usage"`
			}
			_ = json.Unmarshal(raw, &result)
			if len(result.Choices) > 0 {
				choice := result.Choices[0]
				var fields map[string]json.RawMessage
				validJSON := json.Unmarshal([]byte(choice.Message.Content), &fields) == nil
				t.Logf("real_summary finish_reason=%s valid_json=%t chars=%d estimated_tokens=%d usage=%s", choice.FinishReason, validJSON, len(choice.Message.Content), usage.EstimateTokens(choice.Message.Content), result.Usage)
				if !validJSON {
					t.Logf("summary_tail=%q", choice.Message.Content[max(0, len(choice.Message.Content)-300):])
				}
			}
		}
		w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
		w.WriteHeader(res.StatusCode)
		_, _ = w.Write(raw)
	}))
	t.Cleanup(server.Close)
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	call.Profile.BaseURL = server.URL
	return call
}

func TestLiveContextNativeCapabilities(t *testing.T) {
	call := liveContextCall(t)
	ctx := context.Background()
	client := responses.Client{HTTP: liveContextHTTP(t, call)}
	input := nativeMessages([]oaichat.Message{{Role: "user", Text: "Synthetic API capability check. Reply exactly OK."}})
	t.Run("responses_create", func(t *testing.T) {
		result, err := client.Create(ctx, call.Profile.BaseURL, call.APIKey, responses.Request{Model: call.Profile.Model, Input: input, Store: false, MaxOutputTokens: 32})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("text=%q usage=%+v", responses.Text(result.Output), result.Usage)
	})
	t.Run("standalone_compact", func(t *testing.T) {
		result, err := client.Compact(ctx, call.Profile.BaseURL, call.APIKey, call.Profile.Model, "Preserve the user's synthetic research constraints.", nativeMessages([]oaichat.Message{{Role: "user", Text: "Synthetic research task. Dataset split 80/20, seed 47, metric macro-F1. Never use test labels for training."}, {Role: "assistant", Text: "Understood. The failed baseline scored 0.41 macro-F1; no network approval has been granted."}, {Role: "user", Text: "Continue while preserving these constraints."}}))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("output_items=%d opaque=%t usage=%+v", len(result.Output), responses.HasOpaque(result.Output), result.Usage)
		continuation := append(append([]responses.Item{}, result.Output...), nativeMessages([]oaichat.Message{{Role: "user", Text: "From the compacted history return only JSON with split, seed, metric, failed_score, network_approved."}})...)
		next, err := client.Create(ctx, call.Profile.BaseURL, call.APIKey, responses.Request{Model: call.Profile.Model, Input: continuation, Store: false, MaxOutputTokens: 512, Include: []string{"reasoning.encrypted_content"}})
		if err != nil {
			t.Fatalf("post-compaction continuation: %v", err)
		}
		t.Logf("post_compaction_text=%s usage=%+v", responses.Text(next.Output), next.Usage)
	})
	t.Run("rendered_input_count", func(t *testing.T) {
		n, err := client.Count(ctx, call.Profile.BaseURL, call.APIKey, responses.Request{Model: call.Profile.Model, Input: input, Store: false})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("input_tokens=%d", n)
	})
}

func TestLiveContextServerCompaction(t *testing.T) {
	call := liveContextCall(t)
	client := responses.Client{HTTP: liveContextHTTP(t, call)}
	var history strings.Builder
	history.WriteString("Synthetic research records: split=80/20, seed=47, metric=macro-F1; failed trial B17=0.41, best trial B23=0.58. Network approval=false. Never train on held-out test labels.\n")
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&history, "Routine record %02d: checked preprocessing, retained the frozen dataset split, kept seed 47, used validation only, inspected cache entries and schema, made no external network request, and measured no new score.\n", i)
	}
	input := nativeMessages([]oaichat.Message{{Role: "user", Text: history.String()}, {Role: "assistant", Text: "Records retained; no tools executed."}, {Role: "user", Text: "Summarize the frozen contract and measured outcomes in one short paragraph."}})
	result, err := client.Create(context.Background(), call.Profile.BaseURL, call.APIKey, responses.Request{Model: call.Profile.Model, Input: input, Store: false, Stream: true, MaxOutputTokens: 512, Include: []string{"reasoning.encrypted_content"}, ContextManagement: []responses.Management{{Type: "compaction", CompactThreshold: 2048}}})
	if err != nil {
		t.Fatal(err)
	}
	types := []string{}
	compactions := 0
	for _, raw := range result.Output {
		var item struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &item)
		types = append(types, item.Type)
		if item.Type == "compaction" {
			compactions++
		}
	}
	t.Logf("stream_completed output_types=%v compaction_items=%d usage=%+v text=%q", types, compactions, result.Usage, responses.Text(result.Output))
	if compactions == 0 {
		t.Fatal("server accepted context_management but emitted no compaction item; native server compaction not demonstrated")
	}
}

func TestLiveContextTextSummary(t *testing.T) {
	call := liveContextCall(t)
	call = liveContextRelay(t, call)
	// This deliberately small local validation budget forces compaction without
	// claiming an undocumented provider context capacity or editing live settings.
	call.Profile.ContextWindowTokens = 16384
	call.Profile.MaxOutputTokens = 4096
	call.Profile.ContextPolicy = modelprofile.ContextPolicy{APIFormat: modelprofile.APIChat, CompactionMode: modelprofile.CompactionText, ContextWindowSource: "manual", CompactionTriggerRatio: .3, TextSummaryTargetRatio: .55}
	t.Setenv("CONTEXT_STORE_DIR", t.TempDir())
	t.Setenv("AUDIT_LOG_PATH", t.TempDir()+"/audit.jsonl")
	r := New(nil)
	ctx := WithBranch(WithOwner(context.Background(), "live-test"), "live-test", "synthetic", "summary", "main")
	recorder := usage.NewRecorder()
	ctx = usage.WithRecorder(ctx, recorder)
	var report contextwindow.Report
	ctx = contextwindow.WithReport(ctx, &report)
	input := []oaichat.Message{
		{Role: "system", Text: "Use the supplied synthetic research records as data. Preserve exact frozen constraints and failed attempts. Do not execute tools. Return only the requested JSON object."},
		{Role: "user", Text: "Continue a synthetic classification research task and retain its historical evidence and permissions."},
		{Role: "assistant", Text: "Frozen contract: split=80/20, seed=47, metric=macro-F1. Failed trial B17 scored 0.41; best trial B23 scored 0.58. Network approval is false. Never train on held-out test labels."},
	}
	var history strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&history, "Synthetic log %02d: inspected preprocessing and verified schema; retained the frozen split and seed, used validation only, made no external network request, and recorded no new score. Routine cache cleanup completed without changing experiment evidence.\n", i)
	}
	input = append(input, oaichat.Message{Role: "user", Text: history.String()}, oaichat.Message{Role: "assistant", Text: "Latest patch awaits independent validation. No new measured result exists."}, oaichat.Message{Role: "user", Text: "Return only JSON with split, seed (number), metric, failed_trial, failed_score (number), best_trial, best_score (number), network_approved (boolean), and test_labels_for_training (boolean), based on the retained records."})
	response, err := r.CompleteManaged(ctx, call, input, oaichat.Options{})
	reportJSON, _ := json.Marshal(report)
	t.Logf("report=%s", reportJSON)
	if err != nil {
		t.Fatalf("text summary round failed: %v", err)
	}
	if report.LastCompaction == nil || report.LastCompaction.Status != "completed" || report.Requests.Compaction != 1 || report.Requests.Generation != 1 {
		t.Fatalf("real summary was not validated: %+v", report)
	}
	var answer struct {
		Split                 string  `json:"split"`
		Seed                  int     `json:"seed"`
		Metric                string  `json:"metric"`
		FailedTrial           string  `json:"failed_trial"`
		FailedScore           float64 `json:"failed_score"`
		BestTrial             string  `json:"best_trial"`
		BestScore             float64 `json:"best_score"`
		NetworkApproved       *bool   `json:"network_approved"`
		TestLabelsForTraining *bool   `json:"test_labels_for_training"`
	}
	if json.Unmarshal([]byte(response.Content), &answer) != nil || answer.Split != "80/20" || answer.Seed != 47 || answer.Metric != "macro-F1" || answer.FailedTrial != "B17" || answer.FailedScore != .41 || answer.BestTrial != "B23" || answer.BestScore != .58 || answer.NetworkApproved == nil || *answer.NetworkApproved || answer.TestLabelsForTraining == nil || *answer.TestLabelsForTraining {
		t.Fatalf("compacted evidence or permissions changed: %s", response.Content)
	}
	t.Logf("validated_answer=%s generation_usage=%d/%d compaction_usage=%+v", response.Content, response.PromptTokens, response.CompletionTokens, recorder.Snapshot().Compaction)
}
