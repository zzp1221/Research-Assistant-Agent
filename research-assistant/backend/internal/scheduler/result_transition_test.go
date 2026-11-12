package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

// waitPlan builds an upstream node plus a hard dependent, with an execution
// lease already taken on the upstream node.
func waitPlan(t *testing.T, planStore store.PlanStore, planID string) (*models.PlanGraph, string, string, int) {
	t.Helper()
	plan := governanceTestPlan(planID, 2)
	plan.Nodes[1].Dependencies = []string{plan.Nodes[0].ID}
	plan.Edges = []*models.TaskEdge{{ID: "e1", From: plan.Nodes[0].ID, To: plan.Nodes[1].ID, Type: "control"}}
	plan.Status = models.StatusInProgress
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	executionID, owner, epoch, err := s.markTaskStarted(plan.ID, plan.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := planStore.GetPlan(plan.ID)
	return current, executionID, owner, epoch
}

func TestWaitingApprovalDoesNotBlockDependents(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan, executionID, owner, epoch := waitPlan(t, planStore, "wait-approval")
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	action := &models.RuntimeActionRequest{ID: "ra-1", Kind: models.RuntimeActionToolCall, Title: "Approve tool", Status: models.RuntimeActionPending, RequestedAt: time.Now()}
	result := &models.TaskExecutionResult{Status: models.StatusAwaitingApproval, RuntimeAction: action}
	if _, waiting, err := s.applyTaskResult(plan.ID, plan.Nodes[0].ID, executionID, owner, epoch, result, nil, true); err != nil || !waiting {
		t.Fatalf("wait applied=%v err=%v", waiting, err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	upstream, dependent := stored.Nodes[0], stored.Nodes[1]
	if upstream.Status != models.StatusAwaitingApproval {
		t.Fatalf("upstream status=%s", upstream.Status)
	}
	if upstream.Failure != nil || upstream.FinishedAt != nil {
		t.Fatalf("wait recorded a failure: %#v", upstream)
	}
	if upstream.ExecutionID != "" || upstream.LeaseOwner != "" {
		t.Fatalf("wait kept the execution lease: %#v", upstream)
	}
	if upstream.Recovery == nil || !upstream.Recovery.Waiting || upstream.Recovery.PendingRetry {
		t.Fatalf("recovery state=%#v", upstream.Recovery)
	}
	if dependent.Status != models.StatusPending || dependent.BlockInfo != nil {
		t.Fatalf("dependent was blocked by a wait: %#v", dependent)
	}
	if stored.Status != models.StatusAwaitingApproval || stored.Outcome != "" {
		t.Fatalf("plan status=%s outcome=%q", stored.Status, stored.Outcome)
	}
	for _, event := range mustEvents(t, planStore, plan.ID) {
		if event.EventType == "task_failed" || event.EventType == "task_blocked" {
			t.Fatalf("wait published %s", event.EventType)
		}
	}
}

func TestWaitingInputDoesNotChargeRetryOrBlock(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan, executionID, owner, epoch := waitPlan(t, planStore, "wait-input")
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	before, _ := planStore.GetPlan(plan.ID)
	attemptsBefore := before.Nodes[0].RunCount
	result := &models.TaskExecutionResult{
		Status:   models.StatusAwaitingInput,
		Question: &models.PlanQuestion{Header: "选择", Prompt: "选哪个？"},
	}
	if _, waiting, err := s.applyTaskResult(plan.ID, plan.Nodes[0].ID, executionID, owner, epoch, result, nil, true); err != nil || !waiting {
		t.Fatalf("wait applied=%v err=%v", waiting, err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	upstream := stored.Nodes[0]
	if upstream.Status != models.StatusAwaitingInput {
		t.Fatalf("status=%s", upstream.Status)
	}
	if len(stored.Questions) != 1 || stored.Questions[0].Status != models.QuestionPending {
		t.Fatalf("questions=%#v", stored.Questions)
	}
	if stored.Nodes[1].Status != models.StatusPending {
		t.Fatalf("dependent status=%s", stored.Nodes[1].Status)
	}
	// The attempt counter records real executions; a wait is not a new failure
	// retry, so the recovery allowance must be untouched.
	if upstream.RunCount != attemptsBefore {
		t.Fatalf("run count changed: %d -> %d", attemptsBefore, upstream.RunCount)
	}
	if upstream.Recovery == nil || !upstream.Recovery.Waiting || upstream.Recovery.UsedActions != 0 {
		t.Fatalf("recovery=%#v", upstream.Recovery)
	}
}

func TestStaleExecutionCannotPauseTask(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan, executionID, owner, epoch := waitPlan(t, planStore, "stale-wait")
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	result := &models.TaskExecutionResult{
		Status:   models.StatusAwaitingInput,
		Question: &models.PlanQuestion{Prompt: "stale?"},
	}
	_, _, err := s.applyTaskResult(plan.ID, plan.Nodes[0].ID, executionID+"-other", owner, epoch, result, nil, true)
	if !errors.Is(err, ErrStaleExecution) {
		t.Fatalf("stale wait accepted: %v", err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Nodes[0].Status != models.StatusInProgress || len(stored.Questions) != 0 {
		t.Fatalf("stale result mutated the graph: %#v", stored.Nodes[0])
	}
}

func TestExceptionKindsBeatQuestionAndRuntimeAction(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan, executionID, owner, epoch := waitPlan(t, planStore, "exception-wins")
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	result := &models.TaskExecutionResult{
		Status:        models.StatusFailed,
		Error:         "executor crashed",
		Question:      &models.PlanQuestion{Prompt: "ignored"},
		RuntimeAction: &models.RuntimeActionRequest{ID: "ra-ignored", Status: models.RuntimeActionPending},
	}
	if _, waiting, err := s.applyTaskResult(plan.ID, plan.Nodes[0].ID, executionID, owner, epoch, result, errors.New("executor crashed"), true); err != nil || waiting {
		t.Fatalf("exception was turned into a wait: waiting=%v err=%v", waiting, err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if len(stored.Questions) != 0 {
		t.Fatal("question from a failed execution was persisted")
	}
	for _, action := range stored.RuntimeActions {
		if action.ID == "ra-ignored" {
			t.Fatal("runtime action from a failed execution was persisted")
		}
	}
	if stored.Nodes[0].Status == models.StatusAwaitingApproval || stored.Nodes[0].Status == models.StatusAwaitingInput {
		t.Fatalf("failed result accepted as wait: %s", stored.Nodes[0].Status)
	}
}

func TestBareWaitStatusIsAProtocolError(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan, executionID, owner, epoch := waitPlan(t, planStore, "bare-wait")
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	result := &models.TaskExecutionResult{Status: models.StatusAwaitingApproval}
	_, waiting, err := s.applyTaskResult(plan.ID, plan.Nodes[0].ID, executionID, owner, epoch, result, nil, true)
	if err == nil || waiting {
		t.Fatalf("bare wait accepted: waiting=%v err=%v", waiting, err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Nodes[0].Status != models.StatusInProgress {
		t.Fatalf("node status changed to %s", stored.Nodes[0].Status)
	}
}

func TestUnrecognizedStatusFailsWithProtocolReason(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan, executionID, owner, epoch := waitPlan(t, planStore, "unknown-status")
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	result := &models.TaskExecutionResult{Status: models.TaskStatus("mystery")}
	if _, waiting, err := s.applyTaskResult(plan.ID, plan.Nodes[0].ID, executionID, owner, epoch, result, nil, false); err != nil || waiting {
		t.Fatalf("unknown status: waiting=%v err=%v", waiting, err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Nodes[0].Status != models.StatusFailed {
		t.Fatalf("status=%s", stored.Nodes[0].Status)
	}
	if !strings.Contains(stored.Nodes[0].Error, "protocol error") {
		t.Fatalf("error=%q", stored.Nodes[0].Error)
	}
}

func TestRealFailureBlocksDependentsWithReason(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan, executionID, owner, epoch := waitPlan(t, planStore, "real-failure")
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	result := &models.TaskExecutionResult{Status: models.StatusFailed, Error: "ModuleNotFoundError: torch"}
	if _, _, err := s.applyTaskResult(plan.ID, plan.Nodes[0].ID, executionID, owner, epoch, result, nil, false); err != nil {
		t.Fatal(err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	dependent := stored.Nodes[1]
	if dependent.Status != models.StatusBlocked {
		t.Fatalf("dependent status=%s", dependent.Status)
	}
	if dependent.BlockInfo == nil || dependent.BlockInfo.Code != models.BlockUpstreamFailed {
		t.Fatalf("block info=%#v", dependent.BlockInfo)
	}
	if len(dependent.BlockInfo.Causes) != 1 || dependent.BlockInfo.Causes[0].RootTaskID != stored.Nodes[0].ID {
		t.Fatalf("causes=%#v", dependent.BlockInfo.Causes)
	}
	if dependent.Error == "" {
		t.Fatal("blocked dependent has no short explanation")
	}
	found := false
	for _, event := range mustEvents(t, planStore, plan.ID) {
		if event.EventType == "task_blocked" && event.TaskID == dependent.ID {
			if event.Payload["reason"] != models.BlockUpstreamFailed || event.Payload["upstream_task_id"] != stored.Nodes[0].ID {
				t.Fatalf("event payload=%#v", event.Payload)
			}
			if event.Payload["block_info"] == nil {
				t.Fatalf("event has no structured block info: %#v", event.Payload)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no task_blocked event for the dependent")
	}
}

func mustEvents(t *testing.T, planStore store.PlanStore, planID string) []models.PlanEvent {
	t.Helper()
	events, err := planStore.ListEventsAfter(planID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestSchedulerQuestionWaitKeepsDependentsPending(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "plan-wait-chain", IntentType: "General", Status: models.StatusPending, OwnerID: "owner",
		Nodes: []*models.TaskNode{
			{ID: "n1", Name: "ask", Type: "ask_user_question", AssignedTo: "general_agent", Status: models.StatusPending, Inputs: map[string]any{"question_prompt": "which?"}, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5},
			{ID: "n2", Name: "after", Type: "general_synthesis", AssignedTo: "general_agent", Status: models.StatusPending, Dependencies: []string{"n1"}, CreatedAt: now, UpdatedAt: now, TimeoutSeconds: 5},
		},
		Edges:     []*models.TaskEdge{{ID: "e1", From: "n1", To: "n2", Type: "control"}},
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	executor := NewRoutedTaskExecutor(&questionAgent{}, &questionAgent{}, &questionAgent{})
	s := NewScheduler(planStore, executor, &NoopEventPublisher{}, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.ExecutePlan(ctx, plan.ID) }()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		stored, err := planStore.GetPlan(plan.ID)
		if err == nil && stored.Nodes[0].Status == models.StatusAwaitingInput {
			if stored.Nodes[1].Status != models.StatusPending || stored.Nodes[1].BlockInfo != nil {
				t.Fatalf("dependent of a waiting task was stopped: %#v", stored.Nodes[1])
			}
			cancel()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatalf("scheduler never paused on the question")
}
