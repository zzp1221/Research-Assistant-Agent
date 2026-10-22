package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
	"strings"
	"testing"
	"time"
)

type failingRunner struct{}

func (r *failingRunner) ExecuteTask(_ context.Context, task *models.Task, _ map[string]interface{}) error {
	task.Status = models.StatusFailed
	task.Error = "model call failed"
	task.Result = "partial analysis"
	task.Code = "print(1)"
	task.StructuredData = `{"metrics":{"accuracy":0.5}}`
	return fmt.Errorf("model call failed")
}

func TestFailedTaskKeepsPartialOutput(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := &models.PlanGraph{
		ID: "plan-partial", UserIntent: "test", IntentType: "Code_Execution", Status: models.StatusPending,
		CreatedAt: time.Now(), UpdatedAt: time.Now(), Artifacts: map[string]models.Artifact{},
		Nodes: []*models.TaskNode{{
			ID: "task-1", Name: "Generate", Type: "generate_code", AssignedTo: "coder_agent",
			Status: models.StatusPending, RetryLimit: 0, TimeoutSeconds: 60,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	executor := NewRoutedTaskExecutor(&failingRunner{}, &failingRunner{}, &failingRunner{})
	runner := NewScheduler(planStore, executor, nil, 1)
	if err := runner.ExecutePlan(context.Background(), plan.ID); err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}
	final, err := planStore.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	node := final.Nodes[0]
	if node.Status != models.StatusFailed {
		t.Fatalf("status=%s", node.Status)
	}
	if node.Result != "partial analysis" || node.Code != "print(1)" {
		t.Fatalf("partial output was dropped: %#v", node)
	}
	var structured map[string]any
	if err := json.Unmarshal([]byte(node.StructuredData), &structured); err != nil {
		t.Fatalf("structured_data=%q", node.StructuredData)
	}
	if node.Error == "" {
		t.Fatal("error should be recorded")
	}
}

func TestArtifactSummaryIsPersisted(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := &models.PlanGraph{
		ID: "plan-artifact", UserIntent: "test", IntentType: "Code_Execution", Status: models.StatusPending,
		CreatedAt: time.Now(), UpdatedAt: time.Now(), Artifacts: map[string]models.Artifact{},
		Nodes: []*models.TaskNode{{
			ID: "task-2", Name: "Generate", Type: "generate_code", AssignedTo: "coder_agent",
			Status: models.StatusPending, RetryLimit: 0, TimeoutSeconds: 60,
			OutputArtifacts: []string{"generated_code"},
			CreatedAt:       time.Now(), UpdatedAt: time.Now(),
		}},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	executor := NewRoutedTaskExecutor(&failingRunner{}, &failingRunner{}, &completingRunner{})
	runner := NewScheduler(planStore, executor, nil, 1)
	if err := runner.ExecutePlan(context.Background(), plan.ID); err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}
	final, err := planStore.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	artifact, ok := final.Artifacts["generated_code"]
	if !ok || artifact.Value != "print(1)" {
		t.Fatalf("artifacts=%#v", final.Artifacts)
	}
	if !strings.Contains(artifact.Metadata["task_name"].(string), "Generate") {
		t.Fatalf("artifact metadata=%#v", artifact.Metadata)
	}
}

type completingRunner struct{}

func (r *completingRunner) ExecuteTask(_ context.Context, task *models.Task, _ map[string]interface{}) error {
	task.Status = models.StatusCompleted
	task.Result = "done"
	task.Code = "print(1)"
	return nil
}
