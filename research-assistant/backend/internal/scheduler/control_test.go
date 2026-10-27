package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

func TestPausePlanStopsDispatchAndResumeCompletes(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("pause-plan", 1)
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	runner := NewScheduler(planStore, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		calls.Add(1)
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "ok"}, nil
	}), &NoopEventPublisher{}, 1)

	if err := runner.PausePlan(plan.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runner.ExecutePlan(context.Background(), plan.ID) }()

	time.Sleep(600 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatalf("paused plan dispatched %d tasks", calls.Load())
	}
	if err := runner.ResumePlan(plan.ID, "tester"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("execute after resume: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("plan did not finish after resume")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls=%d", calls.Load())
	}
	finalPlan, _ := planStore.GetPlan(plan.ID)
	if finalPlan.Status != models.StatusCompleted {
		t.Fatalf("status=%s", finalPlan.Status)
	}
}

func TestResetFromNodeRewindsDescendantsAndArtifacts(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("rewind-plan", 3)
	now := time.Now()
	plan.Nodes[1].Dependencies = []string{plan.Nodes[0].ID}
	plan.Nodes[2].Dependencies = []string{plan.Nodes[1].ID}
	plan.Edges = []*models.TaskEdge{
		{ID: "e1", From: plan.Nodes[0].ID, To: plan.Nodes[1].ID, Type: "control"},
		{ID: "e2", From: plan.Nodes[1].ID, To: plan.Nodes[2].ID, Type: "control"},
	}
	for _, node := range plan.Nodes {
		node.Status = models.StatusCompleted
		node.FinishedAt = &now
	}
	plan.Status = models.StatusCompleted
	plan.Outcome = models.OutcomeCompleted
	plan.Artifacts = map[string]models.Artifact{
		"first":  {Key: "first", ProducerTaskID: plan.Nodes[0].ID},
		"second": {Key: "second", ProducerTaskID: plan.Nodes[1].ID},
		"third":  {Key: "third", ProducerTaskID: plan.Nodes[2].ID},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	runner := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	if err := runner.ResetFromNode(plan.ID, plan.Nodes[1].ID); err != nil {
		t.Fatal(err)
	}
	updated, _ := planStore.GetPlan(plan.ID)
	if updated.Status != models.StatusPending {
		t.Fatalf("status=%s", updated.Status)
	}
	if updated.Nodes[0].Status != models.StatusCompleted {
		t.Fatalf("upstream node was reset: %s", updated.Nodes[0].Status)
	}
	for _, index := range []int{1, 2} {
		if updated.Nodes[index].Status != models.StatusPending {
			t.Fatalf("node %d status=%s", index, updated.Nodes[index].Status)
		}
	}
	if _, exists := updated.Artifacts["second"]; exists {
		t.Fatal("rewound node artifact was not cleared")
	}
	if _, exists := updated.Artifacts["third"]; exists {
		t.Fatal("descendant artifact was not cleared")
	}
	if _, exists := updated.Artifacts["first"]; !exists {
		t.Fatal("upstream artifact must be preserved")
	}
	events, _ := planStore.ListEventsAfter(plan.ID, 0)
	found := false
	for _, event := range events {
		if event.EventType == "plan_rewound" {
			found = true
		}
	}
	if !found {
		t.Fatal("plan_rewound event missing")
	}
}

func TestStallWatchdogPublishesEvent(t *testing.T) {
	t.Setenv("PLAN_STALL_SECONDS", "1")
	planStore := store.NewMemoryPlanStore()
	plan := governanceTestPlan("stall-plan", 1)
	plan.Nodes[0].TimeoutSeconds = 20
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	runner := NewScheduler(planStore, taskExecutorFunc(func(ctx context.Context, _ *models.PlanGraph, _ *models.TaskNode) (*models.TaskExecutionResult, error) {
		<-release
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "done"}, nil
	}), &NoopEventPublisher{}, 1)
	done := make(chan error, 1)
	go func() { done <- runner.ExecutePlan(context.Background(), plan.ID) }()

	deadline := time.Now().Add(6 * time.Second)
	stalled := false
	for time.Now().Before(deadline) {
		events, err := planStore.ListEventsAfter(plan.ID, 0)
		if err == nil {
			for _, event := range events {
				if event.EventType == "plan_stalled" {
					stalled = true
				}
			}
		}
		if stalled {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("plan did not finish")
	}
	if !stalled {
		t.Fatal("plan_stalled event was not published")
	}
}
