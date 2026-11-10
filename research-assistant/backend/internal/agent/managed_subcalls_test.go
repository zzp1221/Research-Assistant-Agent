package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"research-assistant-backend/internal/agent/conversation"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"

	openaiModel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/schema"
)

func managedSubcallFixture(t *testing.T, content func(string) string) (context.Context, modelprofile.Resolved, *atomic.Int32) {
	t.Helper()
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	t.Setenv("CONTEXT_STORE_DIR", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model     string          `json:"model"`
			MaxTokens int             `json:"max_tokens"`
			Messages  json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "pinned-model" || request.MaxTokens != 256 {
			t.Errorf("subcall did not use the pinned model budget: %+v", request)
		}
		if !strings.Contains(string(request.Messages), "frozen-protocol-42") {
			t.Error("subcall omitted current frozen constraints")
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content(string(request.Messages))}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 20, "completion_tokens": 10},
		})
	}))
	t.Cleanup(server.Close)
	previous := modelresolver.Default()
	modelresolver.SetDefault(modelresolver.New(nil))
	t.Cleanup(func() { modelresolver.SetDefault(previous) })
	call := modelprofile.Resolved{
		Profile: modelprofile.Profile{ID: "pinned", OwnerID: "owner", BaseURL: server.URL, Model: "pinned-model", Revision: 7, ContextWindowTokens: 16000, MaxOutputTokens: 256},
		APIKey:  "test-key",
	}
	ctx := modelresolver.WithBranch(modelresolver.WithOwner(context.Background(), "owner"), "owner", "project", "session", "plan/task")
	ctx = modelresolver.WithConstraints(ctx, "frozen-protocol-42")
	return modelresolver.WithResolvedCall(ctx, call), call, &calls
}

func TestResearchCodingRepairSessionsUseManagedPinnedModel(t *testing.T) {
	ctx, _, calls := managedSubcallFixture(t, func(messages string) string {
		switch {
		case strings.Contains(messages, "candidate-route"):
			return `{"status":"stop","reason":"No further improvements"}`
		case strings.Contains(messages, "adapter-route"):
			raw, _ := json.Marshal(benchmarkAdapterRepairResponse{AdapterCode: benchmarkTestAdapterCode("managed-repair"), Reason: "repair"})
			return string(raw)
		default:
			return `{"status":"no_change","diagnosis":"No safe patch"}`
		}
	})
	runtime := newDynamicChatModel(nil)
	agent := NewResearchCodingAgent(&CoderAgent{Runtime: runtime})
	if agent.Runtime != runtime {
		t.Fatal("research coder did not inherit its coder runtime")
	}
	refs := map[string]bool{}
	for _, route := range []string{"candidate", "adapter", "paper"} {
		t.Run(route, func(t *testing.T) {
			session := conversation.NewSession(route+"-session", route+"-route", "original source and task")
			var firstGeneration int
			firstRef := ""
			for round := 0; round < 2; round++ {
				var err error
				switch route {
				case "candidate":
					_, session, err = agent.proposeAutoResearchCandidateWithSession(ctx, models.ResearchSpec{}, []byte(`{}`), models.ResearchTrialLedger{}, nil, "", "trial feedback", session)
				case "adapter":
					_, _, session, err = agent.repairBenchmarkAdapterWithSession(ctx, `{}`, `{}`, "original code", "preflight feedback", session)
				case "paper":
					_, _, session, err = agent.planPaperCodeRepairWithSession(ctx, &models.Task{ID: "task"}, "", "", "runtime feedback", round+1, session)
				}
				if err != nil {
					t.Fatal(err)
				}
				if session.ContextReport.Revision != 7 || session.ContextReport.StateRef == "" || session.ContextReport.Generation == 0 {
					t.Fatalf("repair session lacks managed recovery state: %+v", session.ContextReport)
				}
				if round == 0 {
					firstGeneration = session.ContextReport.Generation
					firstRef = session.ContextReport.StateRef
					prefix := session.PrefixHash()
					session, err = conversation.Restore(session.Snapshot())
					if err != nil || session.PrefixHash() != prefix {
						t.Fatalf("repair source history was not restored intact: %v", err)
					}
					modelresolver.SetDefault(modelresolver.New(nil))
				} else if session.ContextReport.StateRef != firstRef || session.ContextReport.Generation <= firstGeneration || session.Turns() != 2 {
					t.Fatalf("repair did not continue its committed state after restart: %+v, turns=%d", session.ContextReport, session.Turns())
				}
			}
			if refs[session.ContextReport.StateRef] {
				t.Fatal("independent repair sessions shared context state")
			}
			refs[session.ContextReport.StateRef] = true
		})
	}
	if calls.Load() != 6 {
		t.Fatalf("repair rounds executed %d model calls, want 6", calls.Load())
	}
}

func TestRepairFailureRetainsTaskSnapshot(t *testing.T) {
	ctx, call, calls := managedSubcallFixture(t, func(string) string { return `{"status":"stop"}` })
	call.Profile.ContextWindowTokens = 400
	ctx = modelresolver.WithResolvedCall(ctx, call)
	for _, route := range []string{"benchmark", "paper"} {
		t.Run(route, func(t *testing.T) {
			agent := NewResearchCodingAgent(&CoderAgent{Runtime: newDynamicChatModel(nil)})
			task := &models.Task{ID: "task", Type: "benchmark_adapter_preflight"}
			var err error
			if route == "benchmark" {
				workspace, manifest, spec, code := benchmarkHarnessFixture(t)
				manifestJSON, _ := json.Marshal(manifest)
				specJSON, _ := json.Marshal(spec)
				agent.Sandbox = &scriptedBenchmarkSandbox{workspace: workspace, failFirst: true}
				task.Inputs = map[string]any{
					"workspace_path": workspace, "dataset_manifest": string(manifestJSON),
					"benchmark_adapter_spec": string(specJSON), "benchmark_generated_code": code,
					"prepared_runtime": "dk-fixture", "benchmark_max_preflight_attempts": 2,
				}
				err = agent.ExecuteTask(ctx, task, nil)
			} else {
				_, _, _, err = agent.planPaperCodeRepairWithSession(ctx, task, "", "", "failure", 1,
					conversation.NewSession("failed-paper", "system", strings.Repeat("材料", 2000)))
			}
			if err == nil {
				t.Fatal("expected repair model capacity error")
			}
			snapshot, ok := task.Metadata["repair_session"].(map[string]any)
			if !ok {
				t.Fatal("failed repair lost its latest session snapshot")
			}
			restored, restoreErr := conversation.Restore(snapshot)
			if restoreErr != nil || len(restored.Messages) < 2 || restored.ContextReport.Revision != 7 {
				t.Fatalf("failed repair snapshot is incomplete: %v, %+v", restoreErr, restored)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("capacity rejection reached provider %d times", calls.Load())
	}
}

func TestResearchCodingRepairRejectsOversizedPinnedContextBeforeHTTP(t *testing.T) {
	ctx, call, calls := managedSubcallFixture(t, func(string) string { return `{"status":"stop"}` })
	call.Profile.ContextWindowTokens = 400
	ctx = modelresolver.WithResolvedCall(ctx, call)
	agent := NewResearchCodingAgent(&CoderAgent{Runtime: newDynamicChatModel(nil)})
	_, _, err := agent.proposeAutoResearchCandidateWithSession(ctx, models.ResearchSpec{}, []byte(`{}`), models.ResearchTrialLedger{}, nil, "", "feedback", conversation.NewSession("candidate", "system", strings.Repeat("材料", 2000)))
	if err == nil || !strings.Contains(err.Error(), "context_required_items_too_large") {
		t.Fatalf("oversized context was not rejected by model budget: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("oversized request reached provider %d times", calls.Load())
	}
}

func TestIndependentManagedSubcallsHaveSeparateReplayState(t *testing.T) {
	var answer atomic.Int32
	ctx, _, calls := managedSubcallFixture(t, func(string) string { return fmt.Sprintf("answer-%d", answer.Add(1)) })
	runtime := newDynamicChatModel(nil)
	messages := []*schema.Message{{Role: schema.User, Content: "identical stage input"}}
	first, err := generateRuntimeModel(ctx, runtime, nil, "strategy", messages)
	if err != nil {
		t.Fatal(err)
	}
	second, err := generateRuntimeModel(ctx, runtime, nil, "verification", messages)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := generateRuntimeModel(ctx, runtime, nil, "strategy", messages)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || first.Content == second.Content || replayed.Content != first.Content {
		t.Fatalf("independent stages or replay crossed: %q / %q / %q, calls=%d", first.Content, second.Content, replayed.Content, calls.Load())
	}
}

func TestResearchCodingInjectedFixedModelStillWorksWithoutRuntime(t *testing.T) {
	previous := modelresolver.Default()
	modelresolver.SetDefault(nil)
	t.Cleanup(func() { modelresolver.SetDefault(previous) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"{\"status\":\"stop\"}"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(server.Close)
	fixed, err := openaiModel.NewChatModel(context.Background(), &openaiModel.ChatModelConfig{BaseURL: server.URL, Model: "fixed", APIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	agent := &ResearchCodingAgent{ChatModel: fixed}
	response, _, err := agent.proposeAutoResearchCandidateWithSession(context.Background(), models.ResearchSpec{}, []byte(`{}`), models.ResearchTrialLedger{}, nil, "", "", conversation.NewSession("fixed", "system", "source"))
	if err != nil || response.Status != "stop" {
		t.Fatalf("fixed injected model fallback failed: %+v, %v", response, err)
	}
}
