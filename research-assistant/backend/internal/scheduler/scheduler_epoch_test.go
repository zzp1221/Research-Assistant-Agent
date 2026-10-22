package scheduler

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

func TestExecutionMatchesRequiresMatchingEpoch(t *testing.T) {
	now := time.Now()
	lease := now.Add(time.Minute)
	task := &models.TaskNode{
		Status:         models.StatusInProgress,
		ExecutionID:    "exec-1",
		ExecutionEpoch: 3,
		LeaseOwner:     "coder_agent",
		AssignedTo:     "coder_agent",
		LeaseExpiresAt: &lease,
	}
	if executionMatches(task, "exec-1", "coder_agent", 2) {
		t.Fatal("stale execution epoch must not match")
	}
	if !executionMatches(task, "exec-1", "coder_agent", 3) {
		t.Fatal("current execution epoch must match")
	}
}

func TestMarkTaskCompletedRejectsArtifactFromAnotherProducer(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("artifact-producer-plan", 2)
	plan.Artifacts["shared"] = models.Artifact{Key: "shared", ProducerTaskID: plan.Nodes[0].ID}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, &DefaultTaskExecutor{}, &NoopEventPublisher{}, 1)

	executionID, leaseOwner, epoch, err := runner.markTaskStarted(plan.ID, plan.Nodes[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	err = runner.markTaskCompleted(plan.ID, plan.Nodes[1].ID, executionID, leaseOwner, epoch, &models.TaskExecutionResult{
		Status: models.StatusCompleted,
		Artifacts: []models.Artifact{{
			Key:            "shared",
			Value:          "second",
			ProducerTaskID: plan.Nodes[1].ID,
		}},
	})
	if err == nil {
		t.Fatal("expected a second producer of the same artifact to be rejected")
	}
}

func TestExecuteTaskPersistsCompletedResultAndArtifact(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("single-task-plan", 1)
	plan.Nodes[0].OutputArtifacts = []string{"result"}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		return &models.TaskExecutionResult{
			Status: models.StatusCompleted,
			Result: "manual result",
			Artifacts: []models.Artifact{{
				Key:            "result",
				Value:          "manual result",
				ProducerTaskID: plan.Nodes[0].ID,
			}},
		}, nil
	}), &NoopEventPublisher{}, 1)

	result, err := runner.ExecuteTask(context.Background(), plan.ID, plan.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || result.Status != models.StatusCompleted {
		t.Fatalf("unexpected execution result: %#v", result)
	}
	persisted, err := planStore.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Nodes[0].Status != models.StatusCompleted || persisted.Nodes[0].Result != "manual result" {
		t.Fatalf("manual execution was not persisted: %#v", persisted.Nodes[0])
	}
	if persisted.Artifacts["result"].Value != "manual result" {
		t.Fatalf("manual execution artifact was not persisted: %#v", persisted.Artifacts)
	}
}

func TestExecuteTaskFailureIsTerminalForManualRun(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("single-failure-plan", 1)
	plan.Nodes[0].RetryLimit = 3
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		return &models.TaskExecutionResult{Status: models.StatusFailed, Error: "agent failed"}, nil
	}), &NoopEventPublisher{}, 1)

	if _, err := runner.ExecuteTask(context.Background(), plan.ID, plan.Nodes[0].ID); err != nil {
		t.Fatal(err)
	}
	persisted, err := planStore.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Nodes[0].Status != models.StatusFailed {
		t.Fatalf("manual failure should remain failed, got %s", persisted.Nodes[0].Status)
	}
	if persisted.Nodes[0].Error != "agent failed" {
		t.Fatalf("unexpected error: %q", persisted.Nodes[0].Error)
	}
}
