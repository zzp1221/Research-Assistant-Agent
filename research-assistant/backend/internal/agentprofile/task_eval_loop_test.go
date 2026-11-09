package agentprofile

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/tools"
)

type taskLoopCompleter struct {
	calls       int
	model       modelprofile.Resolved
	sawResult   bool
	wrongAnswer bool
}

type taskControlCompleter struct {
	*taskLoopCompleter
	requestedTool string
	arguments     string
}

func (f *taskControlCompleter) CompleteWithResolved(context.Context, modelprofile.Resolved, []oaichat.Message, oaichat.Options) (*oaichat.Response, error) {
	f.calls++
	return &oaichat.Response{FinishReason: "tool_calls", ToolCalls: []oaichat.ToolCall{{ID: "untrusted-request", Type: "function", Function: oaichat.ToolFunction{Name: toolAlias(f.requestedTool), Arguments: f.arguments}}}}, nil
}

func TestTaskEvaluatorGradesRealPermissionsApprovalAndStepBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name, tool, args string
		steps            int
		emptyAllowlist   bool
		status           models.TaskStatus
		reason           string
	}{
		{"empty-allowlist", "native:echo", `{"message":"forbidden"}`, 2, true, models.StatusFailed, "tool_forbidden: model requested an unexposed tool"},
		{"step-limit", "native:echo", `{"message":"first action"}`, 1, false, models.StatusFailed, "agent_max_steps"},
		{"approval-wait", "native:write_file", `{"path":"must-not-exist.txt","content":"forbidden side effect"}`, 2, false, models.StatusAwaitingApproval, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			registry := newTestRegistry(t)
			prompt, _ := registry.CreatePrompt("owner", "control-eval", "Complete the task within granted permissions.", "owner")
			profile, err := registry.SaveProfile(models.AgentProfile{ID: "profile:control-eval", OwnerID: "owner", Name: "Control evaluator", Enabled: true, PromptVersionID: prompt.ID, ModelProfileID: "model", ExecutionMode: "tool_loop", MaxSteps: scenario.steps, MaxToolCalls: 2, ToolAllowlist: []string{scenario.tool}})
			if err != nil {
				t.Fatal(err)
			}
			toolRegistry := tools.NewRegistry(tools.NewMemoryStore())
			if err := toolRegistry.EnsureBuiltins(); err != nil {
				t.Fatal(err)
			}
			if _, err := toolRegistry.SaveGrant(models.ToolGrant{OwnerID: "owner", AgentProfileID: profile.ID, ToolID: scenario.tool}); err != nil {
				t.Fatal(err)
			}
			model := modelprofile.Resolved{Profile: modelprofile.Profile{ID: "model", Model: "offline-fixture", Revision: 1, ContextWindowTokens: 32000, MaxOutputTokens: 2048, ContextPolicy: modelprofile.ContextPolicy{Capabilities: modelprofile.Capabilities{ToolCalling: modelprofile.VisionYes}}}}
			completer := &taskControlCompleter{taskLoopCompleter: &taskLoopCompleter{model: model}, requestedTool: scenario.tool, arguments: scenario.args}
			invoker := tools.NewInvoker(toolRegistry)
			invoker.WorkspaceRoot = t.TempDir()
			runner := &ProfileRunner{Profiles: registry, Completer: completer, Invoker: invoker, Runs: NewRunStore(t.TempDir())}
			allowed := []string{scenario.tool}
			if scenario.emptyAllowlist {
				allowed = []string{}
			}
			grader := &models.TaskEvalGrader{ExpectedStatus: scenario.status, MaxModelSteps: scenario.steps, MaxToolCalls: 1, ExpectedStopReason: scenario.reason}
			if scenario.name == "step-limit" {
				grader.RequiredTools = []string{scenario.tool}
			} else {
				grader.ForbiddenTools = []string{scenario.tool}
			}
			c := models.PromptEvalCase{Name: scenario.name, Task: &models.PromptEvalTask{Description: "Observe the tool boundary", Contract: models.TaskContract{AllowedTools: allowed}}, Grader: grader}
			e := Evaluator{Registry: registry, TaskRunner: runner}
			result, err := e.EvaluateMode(context.Background(), "owner", prompt.ID, []models.PromptEvalCase{c}, "task", "", profile.ID)
			if err != nil || result.Passed != 1 || completer.calls != 1 {
				t.Fatalf("control case=%+v calls=%d err=%v", result, completer.calls, err)
			}
			if _, err := os.Stat(filepath.Join(invoker.WorkspaceRoot, "must-not-exist.txt")); !os.IsNotExist(err) {
				t.Fatal("unapproved tool produced a side effect")
			}
		})
	}
}

func (f *taskLoopCompleter) ResolveProfile(string, string) (modelprofile.Resolved, error) {
	return f.model, nil
}
func (f *taskLoopCompleter) Complete(context.Context, string, []oaichat.Message, oaichat.Options) (*oaichat.Response, modelprofile.Resolved, error) {
	panic("task eval must use resolved model")
}
func (f *taskLoopCompleter) CompleteWithResolved(_ context.Context, _ modelprofile.Resolved, messages []oaichat.Message, opts oaichat.Options) (*oaichat.Response, error) {
	f.calls++
	if f.calls == 1 {
		return &oaichat.Response{FinishReason: "tool_calls", ToolCalls: []oaichat.ToolCall{{ID: "provider-call", Type: "function", Function: oaichat.ToolFunction{Name: opts.Tools[0].Name, Arguments: `{"message":"two"}`}}}}, nil
	}
	for _, m := range messages {
		if m.Role == "tool" && m.ToolCallID == "provider-call" {
			f.sawResult = true
		}
	}
	output := `{"answer":2}`
	if f.wrongAnswer {
		output = `{"answer":200,"status":"success"}`
	}
	return &oaichat.Response{Content: output, FinishReason: "stop"}, nil
}

func TestTaskEvaluatorRunsProfileToolLoopAndHardGrader(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(map[bool]string{false: "correct-reference", true: "success-keyword-wrong-reference"}[wrong], func(t *testing.T) {
			registry := newTestRegistry(t)
			prompt, err := registry.CreatePrompt("owner", "tool-eval", "Use the authorized echo tool then answer JSON.", "owner")
			if err != nil {
				t.Fatal(err)
			}
			profile, err := registry.SaveProfile(models.AgentProfile{ID: "profile:tool-eval", OwnerID: "owner", Name: "Tool evaluator", Enabled: true, PromptVersionID: prompt.ID, ModelProfileID: "model", ExecutionMode: "tool_loop", MaxSteps: 3, MaxToolCalls: 2, ToolAllowlist: []string{"native:echo"}, OutputSchema: `{"type":"object","required":["answer"],"properties":{"answer":{"type":"number"}}}`})
			if err != nil {
				t.Fatal(err)
			}
			toolRegistry := tools.NewRegistry(tools.NewMemoryStore())
			if err := toolRegistry.EnsureBuiltins(); err != nil {
				t.Fatal(err)
			}
			if _, err := toolRegistry.SaveGrant(models.ToolGrant{OwnerID: "owner", AgentProfileID: profile.ID, ToolID: "native:echo", Approval: models.ToolApprovalNever}); err != nil {
				t.Fatal(err)
			}
			model := modelprofile.Resolved{Profile: modelprofile.Profile{ID: "model", Model: "offline-fixture", Revision: 1, ContextWindowTokens: 32000, MaxOutputTokens: 2048, ContextPolicy: modelprofile.ContextPolicy{Capabilities: modelprofile.Capabilities{ToolCalling: modelprofile.VisionYes}}}}
			completer := &taskLoopCompleter{model: model, wrongAnswer: wrong}
			runner := &ProfileRunner{Profiles: registry, Completer: completer, Invoker: tools.NewInvoker(toolRegistry), Runs: NewRunStore(t.TempDir())}
			e := Evaluator{Registry: registry, TaskRunner: runner}
			c := models.PromptEvalCase{Name: "tool-contract", Task: &models.PromptEvalTask{Description: "Call echo with message two then report answer 2", Contract: models.TaskContract{AllowedTools: []string{"native:echo"}}}, Grader: &models.TaskEvalGrader{OutputSchema: profile.OutputSchema, ExpectedJSON: map[string]any{"/answer": 2}, RequiredTools: []string{"native:echo"}}}
			result, err := e.EvaluateMode(context.Background(), "owner", prompt.ID, []models.PromptEvalCase{c}, "task", "", profile.ID)
			if err != nil {
				t.Fatal(err)
			}
			if (result.Passed == 1) == wrong || result.Errors != 0 || completer.calls != 2 || !completer.sawResult {
				t.Fatalf("tool task result=%+v calls=%d sawResult=%v", result, completer.calls, completer.sawResult)
			}
			if !wrong && len(result.Cases[0].EvidenceRefs) < 2 {
				t.Fatal("passed task lost tool and artifact evidence")
			}
		})
	}
}
