package scheduler

import (
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

// twoRootPlan has two independent roots feeding one consumer.
func twoRootPlan(id string) *models.PlanGraph {
	now := time.Now()
	return &models.PlanGraph{
		ID: id, TraceID: "trace-" + id, Status: models.StatusPending,
		Budget:    models.RunBudget{MaxTaskAttempts: 50, MaxDurationSec: 300},
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "a", Name: "root A", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusCompleted, CreatedAt: now, UpdatedAt: now},
			{ID: "b", Name: "root B", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusCompleted, CreatedAt: now, UpdatedAt: now},
			{ID: "c", Name: "consumer", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusPending, Dependencies: []string{"a", "b"}, CreatedAt: now, UpdatedAt: now},
		},
		Edges: []*models.TaskEdge{
			{ID: "e1", From: "a", To: "c", Type: "control"},
			{ID: "e2", From: "b", To: "c", Type: "control"},
		},
	}
}

func TestTwoRootsKeepBothCausesWhenOneIsRetried(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := twoRootPlan("blocked-two-roots")
	now := time.Now()
	plan.Status = models.StatusFailed
	for _, node := range plan.Nodes[:2] {
		node.Status = models.StatusFailed
		node.Error = "boom"
		node.FinishedAt = &now
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	rootA := getNodeByID(plan, "a")
	rootB := getNodeByID(plan, "b")
	if err := planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		consumer := getNodeByID(current, "c")
		recordBlockCause(consumer, blockCauseFor(getNodeByID(current, "a"), "a", models.StatusFailed, models.BlockUpstreamFailed), now)
		recordBlockCause(consumer, blockCauseFor(getNodeByID(current, "b"), "b", models.StatusFailed, models.BlockUpstreamFailed), now)
		consumer.Status = models.StatusBlocked
		consumer.FinishedAt = &now
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = rootA
	_ = rootB

	// Retrying one root must keep the other effective cause.
	if err := s.RetryTask(plan.ID, "a"); err != nil {
		t.Fatal(err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	consumer := getNodeByID(stored, "c")
	if consumer.Status != models.StatusBlocked {
		t.Fatalf("consumer released while a second cause was still effective: %s", consumer.Status)
	}
	if consumer.BlockInfo == nil || len(consumer.BlockInfo.Causes) != 1 || consumer.BlockInfo.Causes[0].RootTaskID != "b" {
		t.Fatalf("causes=%#v", consumer.BlockInfo)
	}

	// Retrying the second root releases the consumer.
	if err := s.RetryTask(plan.ID, "b"); err != nil {
		t.Fatal(err)
	}
	stored, _ = planStore.GetPlan(plan.ID)
	consumer = getNodeByID(stored, "c")
	if consumer.Status != models.StatusPending {
		t.Fatalf("consumer status=%s, want pending", consumer.Status)
	}
	if consumer.BlockInfo != nil || consumer.Error != "" || consumer.FinishedAt != nil {
		t.Fatalf("stale block state survived: %#v", consumer)
	}
	found := false
	for _, event := range mustEvents(t, planStore, plan.ID) {
		if event.EventType == "task_unblocked" && event.TaskID == "c" {
			found = true
		}
	}
	if !found {
		t.Fatal("no task_unblocked event was recorded")
	}
}

func TestStaleCauseFromOlderEpochIsDropped(t *testing.T) {
	plan := twoRootPlan("blocked-epoch")
	now := time.Now()
	plan.Nodes[0].Status = models.StatusCompleted
	plan.Nodes[1].Status = models.StatusCompleted
	consumer := plan.Nodes[2]
	consumer.Status = models.StatusBlocked
	consumer.BlockInfo = &models.BlockInfo{
		Code: models.BlockUpstreamFailed, OccurredAt: now,
		Causes: []models.BlockCause{{Code: models.BlockUpstreamFailed, RootTaskID: "a", Status: models.StatusFailed, ExecutionEpoch: 1}},
	}
	// The root was retried: new epoch, and it is no longer failed.
	plan.Nodes[0].ExecutionEpoch = 2
	events := refreshBlockedNodes(plan, now)
	if len(events) != 1 || events[0].EventType != "task_unblocked" {
		t.Fatalf("events=%#v", events)
	}
	if consumer.Status != models.StatusPending || consumer.BlockInfo != nil {
		t.Fatalf("stale cause was not dropped: %#v", consumer)
	}
}

func TestLegacyBlockedNodeWithoutReasonIsNotAutoReleased(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := twoRootPlan("blocked-legacy")
	now := time.Now()
	plan.Status = models.StatusFailed
	plan.Nodes[0].Status = models.StatusFailed
	plan.Nodes[0].FinishedAt = &now
	plan.Nodes[1].Status = models.StatusCompleted
	consumer := plan.Nodes[2]
	consumer.Status = models.StatusBlocked
	consumer.Error = "upstream failed"
	consumer.FinishedAt = &now
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	// Retrying root A must not silently release a node whose block source is
	// unproven (it was blocked before block_info existed).
	if err := s.RetryTask(plan.ID, "a"); err != nil {
		t.Fatal(err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	consumer = getNodeByID(stored, "c")
	if consumer.Status != models.StatusBlocked {
		t.Fatalf("unproven legacy block was auto-released: %s", consumer.Status)
	}
}

func TestBlockedRootCarriesItsOwnReason(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := twoRootPlan("blocked-root")
	now := time.Now()
	plan.Status = models.StatusInProgress
	plan.Nodes[0].Status = models.StatusCompleted
	plan.Nodes[1].Status = models.StatusCompleted
	plan.Nodes[2].Status = models.StatusInProgress
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	executionID, owner, epoch, err := s.markTaskStarted(plan.ID, "c")
	if err != nil {
		t.Fatal(err)
	}
	result := &models.TaskExecutionResult{
		Status: models.StatusBlocked,
		Error:  "dataset not found: /data/mnist",
		MissingRequirements: []models.MissingRequirement{{
			Kind: "dataset", Name: "mnist", Detail: "dataset not found", Remediation: "上传数据集后重试",
		}},
	}
	if _, _, err := s.applyTaskResult(plan.ID, "c", executionID, owner, epoch, result, nil, false); err != nil {
		t.Fatal(err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	root := getNodeByID(stored, "c")
	if root.Status != models.StatusBlocked {
		t.Fatalf("status=%s", root.Status)
	}
	if root.BlockInfo == nil || root.BlockInfo.Code != models.BlockUpstreamBlocked || root.BlockInfo.Message == "" {
		t.Fatalf("blocked root has no reason: %#v", root.BlockInfo)
	}
	if root.BlockInfo.OccurredAt.IsZero() {
		t.Fatalf("block info has no timestamp: %#v", root.BlockInfo)
	}
	_ = now
}

func TestBlockPropagatesTransitivelyWithRootAndVia(t *testing.T) {
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "blocked-chain",
		Nodes: []*models.TaskNode{
			{ID: "a", Name: "A", Status: models.StatusFailed, Error: "boom", Revision: 3, ExecutionEpoch: 2},
			{ID: "b", Name: "B", Status: models.StatusPending, Dependencies: []string{"a"}},
			{ID: "c", Name: "C", Status: models.StatusPending, Dependencies: []string{"b"}},
		},
		Edges: []*models.TaskEdge{
			{ID: "e1", From: "a", To: "b", Type: "control"},
			{ID: "e2", From: "b", To: "c", Type: "control"},
		},
	}
	events := propagateBlockToDependents(plan, plan.Nodes[0], now)
	if len(events) != 2 {
		t.Fatalf("events=%d, want 2", len(events))
	}
	c := getNodeByID(plan, "c")
	if c.Status != models.StatusBlocked || c.BlockInfo == nil {
		t.Fatalf("c=%#v", c)
	}
	if len(c.BlockInfo.Causes) != 1 {
		t.Fatalf("causes=%#v", c.BlockInfo.Causes)
	}
	cause := c.BlockInfo.Causes[0]
	if cause.RootTaskID != "a" || cause.ViaTaskID != "b" {
		t.Fatalf("cause root=%s via=%s", cause.RootTaskID, cause.ViaTaskID)
	}
	if cause.Revision != 3 || cause.ExecutionEpoch != 2 || cause.Status != models.StatusFailed {
		t.Fatalf("cause=%#v", cause)
	}
	if getNodeByID(plan, "b").BlockInfo.Causes[0].ViaTaskID != "a" {
		t.Fatalf("direct hop lost its via: %#v", getNodeByID(plan, "b").BlockInfo)
	}
}

func TestWaitingUpstreamDoesNotPropagateBlock(t *testing.T) {
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "blocked-wait",
		Nodes: []*models.TaskNode{
			{ID: "a", Name: "A", Status: models.StatusAwaitingApproval},
			{ID: "b", Name: "B", Status: models.StatusPending, Dependencies: []string{"a"}},
		},
	}
	if events := propagateBlockToDependents(plan, plan.Nodes[0], now); len(events) != 0 {
		t.Fatalf("wait propagated a block: %#v", events)
	}
	if getNodeByID(plan, "b").Status != models.StatusPending {
		t.Fatal("dependent of a waiting node changed status")
	}
}

func TestBlockPropagationNeverOverwritesRunningNodes(t *testing.T) {
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "blocked-running",
		Nodes: []*models.TaskNode{
			{ID: "a", Name: "A", Status: models.StatusFailed, Error: "boom"},
			{ID: "b", Name: "B", Status: models.StatusInProgress, Dependencies: []string{"a"}},
		},
	}
	_ = propagateBlockToDependents(plan, plan.Nodes[0], now)
	if plan.Nodes[1].Status != models.StatusInProgress || plan.Nodes[1].BlockInfo != nil {
		t.Fatalf("running node was rewritten: %#v", plan.Nodes[1])
	}
}
