package agentprofile

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestTaskEvaluatorUsesRealProfileRunnerAndKeepsReferencePrivate(t *testing.T) {
	runner, _, completer := profileContextFixture(t, "model-small", "Compute {{task}}")
	e := Evaluator{Registry: runner.Profiles, TaskRunner: runner}
	profile, _ := runner.Profiles.GetProfileForOwner("owner", "profile:context")
	// The fake provider emits the real runner's schema-independent text output;
	// the frozen grader checks the independently chosen JSON reference.
	_ = completer
	c := models.PromptEvalCase{Name: "reject-success-keyword", Variables: map[string]string{"task": "statistics"}, Task: &models.PromptEvalTask{Description: "Compute statistics", Inputs: map[string]any{"values": []int{1, 2, 3}}}, Grader: &models.TaskEvalGrader{ExpectedJSON: map[string]any{"/mean": "HIDDEN_REFERENCE"}}}
	result, err := e.EvaluateMode(context.Background(), "owner", profile.PromptVersionID, []models.PromptEvalCase{c}, "task", "", profile.ID)
	if err != nil || result.Failed != 1 || result.Passed != 0 {
		t.Fatalf("task falsely passed: %+v %v", result, err)
	}
	if len(completer.messages) == 0 {
		t.Fatal("task runner was not called")
	}
	for _, m := range completer.messages {
		if strings.Contains(m.Text, "HIDDEN_REFERENCE") {
			t.Fatal("grader reference leaked into model inputs")
		}
	}
}

type taskEvalRunnerFunc func(context.Context, *models.Task, map[string]interface{}) error

func (f taskEvalRunnerFunc) ExecuteTask(ctx context.Context, task *models.Task, shared map[string]interface{}) error {
	return f(ctx, task, shared)
}

func TestTaskEvaluatorInfrastructureAndGraderStates(t *testing.T) {
	runner, _, _ := profileContextFixture(t, "model-small", "Report")
	p, _ := runner.Profiles.GetProfileForOwner("owner", "profile:context")
	c := models.PromptEvalCase{Task: &models.PromptEvalTask{Description: "Report"}, Grader: &models.TaskEvalGrader{ExpectedJSON: map[string]any{"/mean": 2}}}
	e := Evaluator{Registry: runner.Profiles}
	result, err := e.EvaluateMode(context.Background(), "owner", p.PromptVersionID, []models.PromptEvalCase{c}, "task", "", p.ID)
	if err != nil || result.Skipped != 1 || result.Passed != 0 || result.ScoredRate != nil {
		t.Fatalf("missing task runner: %+v %v", result, err)
	}
	e.TaskRunner = taskEvalRunnerFunc(func(_ context.Context, task *models.Task, _ map[string]interface{}) error {
		task.Status = models.StatusCompleted
		task.Result = `{"mean":2}`
		return nil
	})
	result, err = e.EvaluateMode(context.Background(), "owner", p.PromptVersionID, []models.PromptEvalCase{c}, "task", "", p.ID)
	if err != nil || result.Passed != 1 {
		t.Fatalf("reference task failed: %+v %v", result, err)
	}
	c.Grader = nil
	c.ExpectedContains = "success"
	result, err = e.EvaluateMode(context.Background(), "owner", p.PromptVersionID, []models.PromptEvalCase{c}, "task", "", p.ID)
	if err != nil || result.Errors != 1 || result.Attempted != 0 {
		t.Fatalf("substring-only task grader accepted: %+v %v", result, err)
	}
}

func TestFrozenTaskQualityBaseline(t *testing.T) {
	raw, err := os.ReadFile("testdata/task-quality-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var dataset struct {
		Version string `json:"version"`
		Cases   []struct {
			ID     string                `json:"id"`
			Task   models.Task           `json:"task"`
			Grader models.TaskEvalGrader `json:"grader"`
			Pass   bool                  `json:"pass"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &dataset); err != nil {
		t.Fatal(err)
	}
	if dataset.Version != "task-quality/v1" || len(dataset.Cases) < 6 {
		t.Fatal("frozen baseline is incomplete")
	}
	for _, c := range dataset.Cases {
		t.Run(c.ID, func(t *testing.T) {
			if err := validateTaskGrader(&c.Grader); err != nil {
				t.Fatal(err)
			}
			err := gradeTask(&c.Task, &c.Grader)
			if (err == nil) != c.Pass {
				t.Fatalf("expected pass=%v: %v", c.Pass, err)
			}
		})
	}
}

func TestTaskGraderRequiresRealToolEvidence(t *testing.T) {
	task := &models.Task{ID: "task", Status: models.StatusCompleted, Result: `{"answer":2}`, Metadata: map[string]any{"plan_id": "plan", "agent_profile_id": "profile"}}
	call := models.ToolCallRecord{ID: "call", PlanID: "plan", TaskID: "task", ToolID: "native:echo", AgentProfileID: "profile", Status: models.ToolCallCompleted, Arguments: map[string]any{"text": "2"}, ResultHash: "sha256:result"}
	call.ArgumentsHash = models.HashArguments(call.Arguments)
	task.ToolCalls = []models.ToolCallRecord{call}
	task.Artifacts = []models.Artifact{{Key: "result", ProducerTaskID: "task", Value: "2", Metadata: map[string]any{"tool_call_id": "call", "result_hash": "sha256:result"}}}
	g := &models.TaskEvalGrader{RequiredTools: []string{"native:echo"}}
	if err := gradeTask(task, g); err != nil {
		t.Fatal(err)
	}
	task.ToolCalls[0].DryRun = true
	if err := gradeTask(task, g); err == nil {
		t.Fatal("dry-run passed as executed tool")
	}
	task.ToolCalls[0].DryRun = false
	task.Artifacts[0].Metadata["tool_call_id"] = "another-call"
	if err := gradeTask(task, g); err == nil {
		t.Fatal("unrelated artifact passed as evidence")
	}
	task.Artifacts[0].Metadata["tool_call_id"] = "call"
	task.ToolCalls[0].Arguments["text"] = "tampered"
	if err := gradeTask(task, g); err == nil {
		t.Fatal("tampered invocation passed")
	}
}
