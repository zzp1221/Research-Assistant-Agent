package agentprofile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/tools"
	"strings"
	"testing"
)

type loopFixture struct {
	runner     *ProfileRunner
	task       *models.Task
	ctx        context.Context
	modelCalls int
	answer     func(int, []oaichat.Message) (string, []oaichat.ToolCall, int)
	finish     string
}

func newLoopFixture(t *testing.T) *loopFixture {
	t.Helper()
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	t.Setenv("CONTEXT_STORE_DIR", t.TempDir())
	f := &loopFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var body struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
		}
		if json.NewDecoder(req.Body).Decode(&body) != nil {
			t.Error("invalid model request")
		}
		messages := []oaichat.Message{}
		for _, m := range body.Messages {
			messages = append(messages, oaichat.Message{Role: m.Role, Text: m.Content, ToolCallID: m.ToolCallID})
		}
		f.modelCalls++
		text, calls, status := f.answer(f.modelCalls, messages)
		if status > 0 {
			w.WriteHeader(status)
			return
		}
		finish := "stop"
		if len(calls) > 0 {
			finish = "tool_calls"
		}
		if f.finish != "" {
			finish = f.finish
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": text, "tool_calls": calls}, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 30, "completion_tokens": 10}})
	}))
	t.Cleanup(server.Close)
	registry := newTestRegistry(t)
	_, err := registry.SaveProfile(models.AgentProfile{ID: "profile:loop", OwnerID: "owner", Name: "loop", Enabled: true, ExecutionMode: "tool_loop", MaxSteps: 3, MaxToolCalls: 2, ModelProfileID: "model-loop", ToolAllowlist: []string{"native:echo"}, OutputSchema: `{"type":"object","required":["answer"],"properties":{"answer":{"type":"string"}}}`})
	if err != nil {
		t.Fatal(err)
	}
	toolRegistry := tools.NewRegistry(tools.NewMemoryStore())
	if err := toolRegistry.EnsureBuiltins(); err != nil {
		t.Fatal(err)
	}
	_, err = toolRegistry.SaveGrant(models.ToolGrant{OwnerID: "owner", AgentProfileID: "profile:loop", ToolID: "native:echo"})
	if err != nil {
		t.Fatal(err)
	}
	f.runner = &ProfileRunner{Profiles: registry, Completer: modelresolver.New(nil), Invoker: tools.NewInvoker(toolRegistry), Runs: NewRunStore(t.TempDir())}
	f.task = &models.Task{ID: "task", AssignedTo: "profile:loop", Description: "Use bound evidence then report", Inputs: map[string]any{"evidence": "ORIGINAL_EVIDENCE"}, Metadata: map[string]any{"tenant_id": "owner", "plan_id": "plan", "project_id": "project", "session_id": "session", "plan_revision": 1}}
	call := modelprofile.Resolved{Profile: modelprofile.Profile{ID: "model-loop", OwnerID: "owner", BaseURL: server.URL, Model: "loop-model", Revision: 1, ContextWindowTokens: 16000, MaxOutputTokens: 256, ContextPolicy: modelprofile.ContextPolicy{Capabilities: modelprofile.Capabilities{ToolCalling: "yes"}}}, APIKey: "test"}
	f.ctx = modelresolver.WithResolvedCall(modelresolver.WithBranch(modelresolver.WithOwner(context.Background(), "owner"), "owner", "project", "session", "loop"), call)
	f.answer = func(n int, m []oaichat.Message) (string, []oaichat.ToolCall, int) {
		if n == 1 {
			return "", []oaichat.ToolCall{loopCall("call-1", "native:echo", `{"message":"result from tool"}`)}, 0
		}
		return `{"answer":"done"}`, nil, 0
	}
	return f
}

func TestProfileLoopRejectsTruncatedFinalAndDeniedApproval(t *testing.T) {
	t.Run("truncated", func(t *testing.T) {
		f := newLoopFixture(t)
		f.finish = "length"
		f.answer = func(int, []oaichat.Message) (string, []oaichat.ToolCall, int) {
			return `{"answer":"looks valid"}`, nil, 0
		}
		if err := f.run(); err == nil || f.task.Status == models.StatusCompleted || f.task.AgentRun.Steps > f.task.AgentRun.MaxSteps {
			t.Fatalf("truncated output accepted: %v %+v", err, f.task.AgentRun)
		}
	})
	for _, state := range []string{"rejected", "expired"} {
		t.Run(state, func(t *testing.T) {
			f := newLoopFixture(t)
			f.runner.Invoker.WorkspaceRoot = t.TempDir()
			_, _ = f.runner.Profiles.UpdateProfile("owner", false, "profile:loop", func(p *models.AgentProfile) error { p.ToolAllowlist = []string{"native:write_file"}; return nil })
			_, _ = f.runner.Invoker.Registry.SaveGrant(models.ToolGrant{OwnerID: "owner", AgentProfileID: "profile:loop", ToolID: "native:write_file"})
			f.answer = func(int, []oaichat.Message) (string, []oaichat.ToolCall, int) {
				return "", []oaichat.ToolCall{loopCall("w", "native:write_file", `{"path":"denied.txt","content":"bad"}`)}, 0
			}
			if err := f.run(); err != nil || f.task.Status != models.StatusAwaitingApproval {
				t.Fatal("approval not requested", err)
			}
			action := *f.task.Metadata["runtime_action"].(*models.RuntimeActionRequest)
			action.Status = models.RuntimeActionRejected
			if state == "expired" {
				action.Status = models.RuntimeActionApproved
				action.Details["expires_at"] = "2000-01-01T00:00:00Z"
			}
			f.ctx = tools.WithApprovals(f.ctx, "owner", "plan", "task", []models.RuntimeActionRequest{action})
			if err := f.run(); err == nil || !strings.Contains(f.task.Error, "tool_approval_"+state) || len(f.task.ToolCalls) != 0 || f.modelCalls != 1 {
				t.Fatalf("approval decision bypassed: %v %+v", err, f.task)
			}
		})
	}
}

type refreshingLoopCompleter struct{ Completer }

func (c refreshingLoopCompleter) ResolveStep(_ context.Context, _, _ string, previous modelprofile.Resolved) (modelprofile.Resolved, error) {
	previous.Profile.Revision++
	previous.Profile.Model = fmt.Sprintf("model-revision-%d", previous.Profile.Revision)
	return previous, nil
}
func TestProfileLoopRefreshesAndRecordsEachModelStep(t *testing.T) {
	f := newLoopFixture(t)
	f.runner.Completer = refreshingLoopCompleter{f.runner.Completer}
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	steps := f.task.AgentRun.ModelSteps
	if len(steps) != 2 || steps[0].ProfileRevision != 2 || steps[1].ProfileRevision != 3 || f.task.Metadata["model_profile_revision"] != 3 {
		t.Fatalf("model revision frozen across steps: %+v", steps)
	}
}
func loopCall(id, tool, args string) oaichat.ToolCall {
	return oaichat.ToolCall{ID: id, Type: "function", Function: oaichat.ToolFunction{Name: toolAlias(tool), Arguments: args}}
}
func (f *loopFixture) run() error {
	return f.runner.ExecuteTask(f.ctx, f.task, map[string]interface{}{"context_managed": true, "plan_context": "PLAN_CONSTRAINT", "core_memory": "REFERENCE_MEMORY"})
}

func TestProfileNativeToolLoopAndConfirmedReplay(t *testing.T) {
	f := newLoopFixture(t)
	f.answer = func(n int, m []oaichat.Message) (string, []oaichat.ToolCall, int) {
		if n == 1 {
			joined := fmt.Sprint(m)
			for _, marker := range []string{"ORIGINAL_EVIDENCE", "PLAN_CONSTRAINT", "REFERENCE_MEMORY"} {
				if !strings.Contains(joined, marker) {
					t.Error("missing shared context " + marker)
				}
			}
			return "", []oaichat.ToolCall{loopCall("call-1", "native:echo", `{"message":"result from tool"}`)}, 0
		}
		if m[len(m)-1].Role != "tool" || m[len(m)-1].ToolCallID != "call-1" || !strings.Contains(m[len(m)-1].Text, "result from tool") {
			t.Error("native result correlation lost")
		}
		return `{"answer":"done"}`, nil, 0
	}
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	if f.task.Status != models.StatusCompleted || len(f.task.ToolCalls) != 1 || len(f.task.Artifacts) == 0 || f.task.AgentRun.Steps != 2 {
		t.Fatalf("unverified loop result %+v", f.task)
	}
	if f.task.Artifacts[0].Metadata["tool_call_id"] != f.task.ToolCalls[0].ID {
		t.Fatal("tool artifact provenance missing")
	}
	f.runner.Runs = NewRunStore(f.runner.Runs.Dir)
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	if f.modelCalls != 2 {
		t.Fatal("restart replayed confirmed work")
	}
}
func TestProfileToolLoopBoundaries(t *testing.T) {
	for _, scenario := range []string{"steps", "tools", "empty_allowlist", "incomplete", "unauthorized", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			f := newLoopFixture(t)
			if scenario == "steps" {
				_, _ = f.runner.Profiles.UpdateProfile("owner", false, "profile:loop", func(p *models.AgentProfile) error { p.MaxSteps = 1; return nil })
			}
			if scenario == "tools" {
				_, _ = f.runner.Profiles.UpdateProfile("owner", false, "profile:loop", func(p *models.AgentProfile) error { p.MaxToolCalls = 1; return nil })
				f.answer = func(int, []oaichat.Message) (string, []oaichat.ToolCall, int) {
					return "", []oaichat.ToolCall{loopCall("a", "native:echo", `{"message":"a"}`), loopCall("b", "native:echo", `{"message":"b"}`)}, 0
				}
			}
			if scenario == "empty_allowlist" {
				f.task.Contract.AllowedTools = []string{}
			}
			if scenario == "incomplete" {
				f.answer = func(int, []oaichat.Message) (string, []oaichat.ToolCall, int) {
					return "", []oaichat.ToolCall{loopCall("a", "native:echo", `{"message":`)}, 0
				}
			}
			if scenario == "unauthorized" {
				f.answer = func(int, []oaichat.Message) (string, []oaichat.ToolCall, int) {
					return "", []oaichat.ToolCall{loopCall("a", "native:write_file", `{"path":"bad","content":"x"}`)}, 0
				}
			}
			if scenario == "cancel" {
				ctx, cancel := context.WithCancel(f.ctx)
				cancel()
				f.ctx = ctx
			}
			if err := f.run(); err == nil || f.task.Status == models.StatusCompleted {
				t.Fatalf("boundary accepted %s", scenario)
			}
			if scenario == "tools" && len(f.task.ToolCalls) != 1 {
				t.Fatal("batch bypassed tool allowance")
			}
			if (scenario == "empty_allowlist" || scenario == "unauthorized" || scenario == "incomplete") && len(f.task.ToolCalls) > 0 {
				t.Fatal("invalid action executed")
			}
		})
	}
}
func TestProfileApprovalBoundToPendingAction(t *testing.T) {
	f := newLoopFixture(t)
	root := t.TempDir()
	f.runner.Invoker.WorkspaceRoot = root
	_, _ = f.runner.Profiles.UpdateProfile("owner", false, "profile:loop", func(p *models.AgentProfile) error { p.ToolAllowlist = []string{"native:write_file"}; return nil })
	_, _ = f.runner.Invoker.Registry.SaveGrant(models.ToolGrant{OwnerID: "owner", AgentProfileID: "profile:loop", ToolID: "native:write_file"})
	f.answer = func(n int, _ []oaichat.Message) (string, []oaichat.ToolCall, int) {
		if n == 1 {
			return "", []oaichat.ToolCall{loopCall("write", "native:write_file", `{"path":"approved.txt","content":"written"}`)}, 0
		}
		return `{"answer":"done"}`, nil, 0
	}
	f.task.Metadata["approval_id"] = "forged"
	if err := f.run(); err != nil || f.task.Status != models.StatusAwaitingApproval {
		t.Fatalf("approval state %v %+v", err, f.task)
	}
	if _, err := os.Stat(filepath.Join(root, "approved.txt")); !os.IsNotExist(err) {
		t.Fatal("unapproved side effect")
	}
	action := *f.task.Metadata["runtime_action"].(*models.RuntimeActionRequest)
	action.Status = models.RuntimeActionApproved
	f.ctx = tools.WithApprovals(f.ctx, "owner", "plan", "task", []models.RuntimeActionRequest{action})
	if err := f.run(); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(root, "approved.txt")); err != nil || string(raw) != "written" {
		t.Fatal("approved action did not execute")
	}
	if f.modelCalls != 2 {
		t.Fatal("approval resume repeated generation")
	}
}
func TestProfileUnknownToolOutcomeIsNeverReplayed(t *testing.T) {
	f := newLoopFixture(t)
	calls := 0
	f.runner.Invoker.Native["echo"] = func(context.Context, tools.NativeRequest) (*tools.NativeResult, error) {
		calls++
		panic("crash after side effect")
	}
	// Built-in native implementation key is looked up from its pinned definition.
	definition, _ := f.runner.Invoker.Registry.GetToolForOwner("owner", "native:echo")
	f.runner.Invoker.Native[definition.Native] = func(context.Context, tools.NativeRequest) (*tools.NativeResult, error) {
		calls++
		panic("simulated process crash")
	}
	func() { defer func() { _ = recover() }(); _ = f.run() }()
	f.runner.Runs = NewRunStore(f.runner.Runs.Dir)
	if err := f.run(); err != nil || f.task.Status != models.StatusBlocked || calls != 1 {
		t.Fatalf("unknown external outcome replayed: %v calls=%d status=%s", err, calls, f.task.Status)
	}
}
