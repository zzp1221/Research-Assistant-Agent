package scheduler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/store"
)

// partialPlan is the reference scenario: core A feeds report C, optional B feeds
// C through an optional dependency. B fails, C still runs and reports the loss.
func partialPlan(id string) *models.PlanGraph {
	now := time.Now()
	optional := true
	return &models.PlanGraph{
		ID: id, TraceID: "trace-" + id, Status: models.StatusPending,
		IntentType: "Code_Execution",
		Budget:     models.RunBudget{MaxTaskAttempts: 50, MaxDurationSec: 300},
		Artifacts:  map[string]models.Artifact{},
		CreatedAt:  now, UpdatedAt: now,
		Acceptance: &models.AcceptanceContract{
			Version:           "acceptance/v1",
			Goals:             []models.AcceptanceGoal{{ID: "g1", Statement: "core experiment reproduced", EvidenceRefs: []string{"report"}}},
			RequiredArtifacts: []string{"report"},
		},
		Nodes: []*models.TaskNode{
			{ID: "a", Name: "core experiment", Type: "generic", AssignedTo: "general_agent", Status: models.StatusCompleted, CreatedAt: now, UpdatedAt: now},
			{ID: "b", Name: "optional metric", Type: "generic", AssignedTo: "general_agent", Status: models.StatusPending, Optional: &optional, CreatedAt: now, UpdatedAt: now},
			{ID: "c", Name: "report", Type: "generic", AssignedTo: "general_agent", Status: models.StatusPending, Dependencies: []string{"a"}, OptionalDependencies: []string{"b"}, OutputArtifacts: []string{"report"}, CreatedAt: now, UpdatedAt: now},
		},
		Edges: []*models.TaskEdge{
			{ID: "e1", From: "a", To: "c", Type: "control"},
			{ID: "e2", From: "b", To: "c", Type: "optional"},
		},
	}
}

func TestOptionalFailureYieldsPartialWithLosses(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := partialPlan("partial-plan")
	plan.Status = models.StatusInProgress
	now := time.Now()
	plan.Nodes[1].Status = models.StatusFailed
	plan.Nodes[1].Error = "metric dataset missing"
	plan.Nodes[1].FinishedAt = &now
	plan.Nodes[2].Status = models.StatusCompleted
	plan.Nodes[2].FinishedAt = &now
	plan.Artifacts["report"] = models.Artifact{Key: "report", Value: "core conclusion", ProducerTaskID: "c"}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	s.SetFinalizer(&planner.PlanFinalizer{})
	hint, _ := planStore.GetPlan(plan.ID)
	if outcome, err := s.reconcilePlanState(context.Background(), hint); err != nil || outcome != reconcileTerminal {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Status != models.StatusCompleted || final.Outcome != models.OutcomePartial {
		t.Fatalf("status=%s outcome=%s reason=%q", final.Status, final.Outcome, final.OutcomeReason)
	}
	if final.OutcomeDetails == nil {
		t.Fatal("partial outcome has no details")
	}
	if len(final.OutcomeDetails.OptionalLosses) != 1 || final.OutcomeDetails.OptionalLosses[0].TaskID != "b" {
		t.Fatalf("losses=%#v", final.OutcomeDetails.OptionalLosses)
	}
	if len(final.OutcomeDetails.AcceptedGoals) != 1 || !final.OutcomeDetails.AcceptedGoals[0].Verified {
		t.Fatalf("accepted goals=%#v", final.OutcomeDetails.AcceptedGoals)
	}
	if len(final.OutcomeDetails.Limitations) == 0 {
		t.Fatal("partial outcome does not say what the loss restricts")
	}
	// The failed optional node keeps its real status; partial never rewrites it.
	if getNodeByID(final, "b").Status != models.StatusFailed {
		t.Fatalf("optional node status was rewritten: %s", getNodeByID(final, "b").Status)
	}
	found := false
	for _, event := range mustEvents(t, planStore, plan.ID) {
		if event.EventType == "plan_completed" {
			if event.Payload["outcome"] != string(models.OutcomePartial) {
				t.Fatalf("event outcome=%v", event.Payload["outcome"])
			}
			if event.Payload["outcome_details"] == nil {
				t.Fatal("terminal event has no outcome details")
			}
			found = true
		}
	}
	if !found {
		t.Fatal("no completed plan event")
	}
}

func TestCoreFailureIsNeverPartial(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	plan := partialPlan("partial-core-fail")
	plan.Status = models.StatusInProgress
	now := time.Now()
	plan.Nodes[0].Status = models.StatusFailed
	plan.Nodes[0].Error = "core experiment failed"
	plan.Nodes[0].FinishedAt = &now
	plan.Nodes[1].Status = models.StatusCompleted
	plan.Nodes[2].Status = models.StatusBlocked
	plan.Nodes[2].BlockInfo = &models.BlockInfo{Code: models.BlockUpstreamFailed, OccurredAt: now, Causes: []models.BlockCause{{Code: models.BlockUpstreamFailed, RootTaskID: "a", Status: models.StatusFailed}}}
	plan.Nodes[2].FinishedAt = &now
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	s.SetFinalizer(&planner.PlanFinalizer{})
	hint, _ := planStore.GetPlan(plan.ID)
	if _, err := s.reconcilePlanState(context.Background(), hint); err != nil {
		t.Fatal(err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Status != models.StatusFailed || final.Outcome != models.OutcomeFailed {
		t.Fatalf("status=%s outcome=%s", final.Status, final.Outcome)
	}
}

func TestLegacyPlanWithoutOptionalFieldsStaysStrict(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	plan := &models.PlanGraph{
		ID: "legacy-strict", Status: models.StatusInProgress, IntentType: "General",
		Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "a", Status: models.StatusCompleted, CreatedAt: now, UpdatedAt: now},
			{ID: "b", Status: models.StatusFailed, Error: "boom", FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
		},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	s.SetFinalizer(&planner.PlanFinalizer{})
	hint, _ := planStore.GetPlan(plan.ID)
	if _, err := s.reconcilePlanState(context.Background(), hint); err != nil {
		t.Fatal(err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Status != models.StatusFailed || final.Outcome != models.OutcomeFailed {
		t.Fatalf("legacy plan became tolerant: status=%s outcome=%s", final.Status, final.Outcome)
	}
	if final.OutcomeDetails != nil && len(final.OutcomeDetails.OptionalLosses) > 0 {
		t.Fatal("legacy plan reported optional losses")
	}
}

func TestDegradedEvidenceIsNeedsReviewNotPartial(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	optional := true
	graph := models.ClaimEvidenceGraph{
		Status: "degraded", StatusReason: "no run metrics",
		Claims:  []models.ClaimEvidenceResult{{ClaimID: "c1"}},
		Summary: models.ClaimEvidenceSummary{TotalClaims: 1, Verified: 0},
	}
	rawBytes, _ := json.Marshal(graph)
	raw := string(rawBytes)
	plan := &models.PlanGraph{
		ID: "evidence-review", Status: models.StatusInProgress, IntentType: "Paper_Reproduction",
		Artifacts: map[string]models.Artifact{"claim_evidence_graph": {Key: "claim_evidence_graph", Type: "json", Value: raw}},
		CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "a", Status: models.StatusCompleted, CreatedAt: now, UpdatedAt: now},
			{ID: "b", Status: models.StatusFailed, Optional: &optional, Error: "metric missing", FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
		},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	s.SetFinalizer(&planner.PlanFinalizer{})
	hint, _ := planStore.GetPlan(plan.ID)
	if _, err := s.reconcilePlanState(context.Background(), hint); err != nil {
		t.Fatal(err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Outcome != models.OutcomeNeedsReview {
		t.Fatalf("outcome=%s, want needs_review", final.Outcome)
	}
}

func TestSmokePlanKeepsSmokeWithOptionalLoss(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	now := time.Now()
	optional := true
	mode := `{"effective_mode":"smoke"}`
	plan := &models.PlanGraph{
		ID: "smoke-loss", Status: models.StatusInProgress, IntentType: "Paper_Reproduction",
		Artifacts: map[string]models.Artifact{"reproduction_mode_report": {Key: "reproduction_mode_report", Value: mode}},
		CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "a", Status: models.StatusCompleted, CreatedAt: now, UpdatedAt: now},
			{ID: "b", Status: models.StatusFailed, Optional: &optional, Error: "plot failed", FinishedAt: &now, CreatedAt: now, UpdatedAt: now},
		},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(planStore, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	s.SetFinalizer(&planner.PlanFinalizer{})
	hint, _ := planStore.GetPlan(plan.ID)
	if _, err := s.reconcilePlanState(context.Background(), hint); err != nil {
		t.Fatal(err)
	}
	final, _ := planStore.GetPlan(plan.ID)
	if final.Outcome != models.OutcomeSmoke {
		t.Fatalf("outcome=%s, want smoke", final.Outcome)
	}
	if final.OutcomeDetails == nil || len(final.OutcomeDetails.OptionalLosses) != 1 {
		t.Fatalf("smoke plan did not list its optional loss: %#v", final.OutcomeDetails)
	}
}

func TestOptionalConsumerWaitsForOptionalProducerToEnd(t *testing.T) {
	now := time.Now()
	optional := true
	plan := partialPlan("partial-ready")
	plan.Nodes[0].Status = models.StatusCompleted
	plan.Nodes[1].Status = models.StatusAwaitingApproval
	plan.Nodes[2].Status = models.StatusPending
	s := NewScheduler(store.NewMemoryPlanStore(), NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	events := s.promotePendingNodesToReady(plan)
	for _, event := range events {
		if event.TaskID == "c" && event.EventType == "task_ready" {
			t.Fatal("consumer stepped over a task that is still waiting")
		}
	}
	if plan.Nodes[2].Status != models.StatusPending {
		t.Fatalf("consumer status=%s", plan.Nodes[2].Status)
	}
	// Once the optional producer ends (even unsuccessfully) the consumer runs.
	plan.Nodes[1].Status = models.StatusFailed
	plan.Nodes[1].FinishedAt = &now
	events = s.promotePendingNodesToReady(plan)
	found := false
	for _, event := range events {
		if event.TaskID == "c" && event.EventType == "task_ready" {
			found = true
		}
	}
	if !found {
		t.Fatalf("consumer was not released after the optional producer ended: %#v", plan.Nodes[2])
	}
	_ = optional
}

func TestOptionalProducerArtifactIsNotATrustedInput(t *testing.T) {
	optional := true
	plan := &models.PlanGraph{
		ID: "partial-artifact",
		Nodes: []*models.TaskNode{
			{ID: "b", Status: models.StatusFailed, Optional: &optional, OutputArtifacts: []string{"metric"}},
			{ID: "c", Status: models.StatusPending, OptionalDependencies: []string{"b"}, RequiredArtifacts: []string{"metric"}},
		},
		Artifacts: map[string]models.Artifact{"metric": {Key: "metric", Value: "diagnostic only", ProducerTaskID: "b"}},
	}
	if requiredArtifactsSatisfied(plan, plan.Nodes[1]) {
		t.Fatal("a diagnostic artifact from an optional producer was accepted as a trusted input")
	}
}
