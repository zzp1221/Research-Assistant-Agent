package scheduler

// Regression tests for the third independent review
// (reports/scheduler-independent-review-2026-11-12/review-v3.md). Each case
// pins one adjacent path of the V2 fixes: artifact trust across the automatic
// retry, the persisted terminal commit, the shared approval-timeout pipeline,
// the claim counters and the required-task contract.

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/store"
)

// --- F1: artifact trust across the automatic retry path. ---

func TestAutoRetryCannotRecertifyFailedDiagnostic(t *testing.T) {
	p := reviewBoundaryPlan("v3-auto-retry")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusPending
	p.Nodes[0].Status = models.StatusPending
	p.Nodes[0].OutputArtifacts = []string{"report"}
	p.Nodes[0].RetryLimit = 5
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s := NewScheduler(st, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		calls++
		if calls == 1 {
			return &models.TaskExecutionResult{
				Status:    models.StatusFailed,
				Error:     "temporary network timeout",
				Failure:   &models.FailureInfo{Class: "transient", Code: "transient_failure", Recoverable: true, Message: "temporary network timeout", Stage: "task"},
				Artifacts: []models.Artifact{{Key: "report", Value: "diagnostic from the failed attempt", ProducerTaskID: "core"}},
			}, nil
		}
		// The retry finishes without re-producing the report.
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "completed without a new report"}, nil
	}), nil, 1)
	s.SetFinalizer(&planner.PlanFinalizer{})
	if err := s.ExecutePlan(context.Background(), p.ID); err != nil {
		t.Fatalf("execute plan: %v", err)
	}
	got, _ := st.GetPlan(p.ID)
	if got.Outcome == models.OutcomeCompleted || got.Outcome == models.OutcomePartial {
		t.Fatalf("failed attempt's diagnostic re-certified after an automatic retry: %s/%s artifact=%q",
			got.Status, got.Outcome, got.Artifacts["report"].Value)
	}
}

func TestAutoRetryReproducingArtifactIsAccepted(t *testing.T) {
	p := reviewBoundaryPlan("v3-auto-retry-reproduce")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusPending
	p.Nodes[0].Status = models.StatusPending
	p.Nodes[0].OutputArtifacts = []string{"report"}
	p.Nodes[0].RetryLimit = 5
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s := NewScheduler(st, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		calls++
		if calls == 1 {
			return &models.TaskExecutionResult{
				Status:    models.StatusFailed,
				Error:     "temporary network timeout",
				Failure:   &models.FailureInfo{Class: "transient", Code: "transient_failure", Recoverable: true, Message: "temporary network timeout", Stage: "task"},
				Artifacts: []models.Artifact{{Key: "report", Value: "diagnostic", ProducerTaskID: "core"}},
			}, nil
		}
		return &models.TaskExecutionResult{
			Status:    models.StatusCompleted,
			Result:    "recovered",
			Artifacts: []models.Artifact{{Key: "report", Value: "fresh report", ProducerTaskID: "core"}},
		}, nil
	}), nil, 1)
	s.SetFinalizer(&planner.PlanFinalizer{})
	if err := s.ExecutePlan(context.Background(), p.ID); err != nil {
		t.Fatalf("execute plan: %v", err)
	}
	got, _ := st.GetPlan(p.ID)
	if got.Outcome != models.OutcomeCompleted {
		t.Fatalf("retry re-produced the required evidence but outcome is %s/%s (%s)", got.Status, got.Outcome, got.OutcomeReason)
	}
}

// --- F2: the terminal commit survives a restart and runs once. ---

type v3MemorySink struct{ records int }

func (m *v3MemorySink) RecordPlan(context.Context, *models.PlanGraph) error { m.records++; return nil }

func (m *v3MemorySink) RecordTask(context.Context, *models.PlanGraph, *models.TaskNode, *models.TaskExecutionResult) error {
	return nil
}

func TestTerminalPipelineSurvivesRestart(t *testing.T) {
	p := reviewBoundaryPlan("v3-restart-terminal")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusAwaitingInput
	p.Nodes[0].Status = models.StatusAwaitingInput
	past := time.Now().Add(-time.Minute)
	p.Questions = []models.PlanQuestion{{ID: "q1", PlanID: p.ID, TaskID: "core", Prompt: "supply data", Status: models.QuestionPending, RequestedAt: past.Add(-time.Minute), ExpiresAt: &past}}
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	// First process commits the timeout and then stops before the side effects.
	first := NewScheduler(st, nil, nil, 1)
	if expired, err := first.expireQuestions(p.ID); err != nil || !expired {
		t.Fatalf("expired=%v err=%v", expired, err)
	}
	// Second process: the API/startup compensation must finish the pipeline once.
	callbacks := 0
	sink := &v3MemorySink{}
	second := NewScheduler(st, nil, nil, 1)
	second.SetMemorySink(sink)
	second.onTerminal = func(context.Context, *models.PlanGraph) { callbacks++ }
	if !second.ReconcileTerminalCommits(context.Background(), p.ID) {
		t.Fatal("compensation did not notice a terminal plan without its side effects")
	}
	// A later observer must not repeat it.
	second.ReconcileTerminalCommits(context.Background(), p.ID)
	events, _ := st.ListEvents(p.ID)
	terminal := 0
	for _, event := range events {
		switch event.EventType {
		case "plan_failed", "plan_completed", "plan_canceled":
			terminal++
		}
	}
	if terminal != 1 || callbacks != 1 || sink.records != 1 {
		t.Fatalf("terminal pipeline ran %d/%d/%d time(s) after restart, want 1/1/1", terminal, callbacks, sink.records)
	}
}

func TestCancelRunsTerminalPipelineOnce(t *testing.T) {
	p := reviewBoundaryPlan("v3-cancel-once")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusInProgress
	p.Nodes[0].Status = models.StatusInProgress
	lease := time.Now().Add(time.Minute)
	p.Nodes[0].ExecutionID = "exec-1"
	p.Nodes[0].LeaseOwner = "coder_agent"
	p.Nodes[0].LeaseExpiresAt = &lease
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	sink := &v3MemorySink{}
	s := NewScheduler(st, nil, nil, 1)
	s.SetMemorySink(sink)
	if err := s.CancelPlanWithActor(p.ID, "canceled by user", "tester"); err != nil {
		t.Fatal(err)
	}
	// The execution loop wakes up, sees the canceled status and finalizes again.
	s.finishExternalTerminal(context.Background(), p.ID)
	s.reconcileTerminalOnly(context.Background(), p.ID)
	events, _ := st.ListEvents(p.ID)
	canceled := 0
	for _, event := range events {
		if event.EventType == "plan_canceled" {
			canceled++
		}
	}
	// Cancel owns its own finalization (it records memory itself and does not use
	// the onTerminal hook), so the later observers must not repeat it.
	if canceled != 1 || sink.records != 1 {
		t.Fatalf("cancel side effects ran %d/%d time(s), want 1/1", canceled, sink.records)
	}
	if plan, _ := st.GetPlan(p.ID); plan == nil || plan.TerminalCommittedAt == nil {
		t.Fatal("cancel did not persist the terminal commit marker")
	}
}

// reconcileTerminalOnly mirrors the loop's terminal observation without
// introducing a second public entry point.
func (s *Scheduler) reconcileTerminalOnly(ctx context.Context, planID string) {
	plan, err := s.store.GetPlan(planID)
	if err != nil || plan == nil {
		return
	}
	_, _ = s.reconcilePlanState(ctx, plan)
}

// --- F3: the approval timeout shares the same pipeline. ---

func TestApprovalTimeoutUsesOneTerminalPipeline(t *testing.T) {
	t.Setenv("APPROVAL_TIMEOUT_SECONDS", "1")
	p := reviewBoundaryPlan("v3-approval-timeout")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusAwaitingApproval
	p.Nodes[0].Status = models.StatusAwaitingApproval
	p.RuntimeActions = []models.RuntimeActionRequest{{ID: "a1", PlanID: p.ID, TaskID: "core", Status: models.RuntimeActionPending, RequestedAt: time.Now().Add(-time.Hour)}}
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	sink := &v3MemorySink{}
	s := NewScheduler(st, nil, nil, 1)
	s.SetMemorySink(sink)
	s.onTerminal = func(context.Context, *models.PlanGraph) { callbacks++ }
	expired, err := s.ExpireApprovalsNow(p.ID)
	if err != nil || !expired {
		t.Fatalf("expiry=%v err=%v", expired, err)
	}
	s.ExpireApprovalsNow(p.ID)
	events, _ := st.ListEvents(p.ID)
	terminal := 0
	for _, event := range events {
		switch event.EventType {
		case "plan_failed", "plan_completed", "plan_canceled":
			terminal++
		}
	}
	if terminal != 1 || callbacks != 1 || sink.records != 1 {
		t.Fatalf("approval timeout ran its terminal pipeline %d/%d/%d time(s), want 1/1/1", terminal, callbacks, sink.records)
	}
}

// --- F4: claim counters come from the trusted graph. ---

func TestOutcomeDetailsClaimsFollowTrust(t *testing.T) {
	graphBody := `{"version":"claim.evidence/v1","status":"assessed","summary":{"verified":2,"total_claims":3},"claims":[{"claim_id":"c1","status":"verified"},{"claim_id":"c2","status":"verified"}]}`
	untrusted := reviewBoundaryPlan("v3-claims-untrusted")
	untrusted.CurrentRevision = 2
	untrusted.Nodes[1].OutputArtifacts = []string{"claim_evidence_graph"}
	untrusted.Acceptance.Goals[0].EvidenceRefs = nil
	untrusted.Acceptance.Goals[0].MinVerifiedClaims = 1
	untrusted.Artifacts["claim_evidence_graph"] = models.Artifact{Key: "claim_evidence_graph", ProducerTaskID: "optional", Revision: 1, Value: graphBody}
	if got := reviewBoundaryReconcile(t, untrusted); got.OutcomeDetails != nil && got.OutcomeDetails.ClaimsVerified != 0 {
		t.Fatalf("outcome details counted %d claims from an untrusted graph", got.OutcomeDetails.ClaimsVerified)
	}

	trusted := reviewBoundaryPlan("v3-claims-trusted")
	trusted.CurrentRevision = 1
	trusted.Nodes[1].OutputArtifacts = []string{"claim_evidence_graph"}
	trusted.Acceptance.Goals[0].EvidenceRefs = nil
	trusted.Acceptance.Goals[0].MinVerifiedClaims = 1
	trusted.Artifacts["claim_evidence_graph"] = models.Artifact{Key: "claim_evidence_graph", ProducerTaskID: "core", Revision: 1, Value: graphBody}
	trusted.Nodes[1].Status = models.StatusCompleted
	got := reviewBoundaryReconcile(t, trusted)
	if got.OutcomeDetails == nil || got.OutcomeDetails.ClaimsVerified != 2 || got.OutcomeDetails.ClaimsTotal != 3 {
		t.Fatalf("trusted graph counters missing: %+v (outcome %s/%s)", got.OutcomeDetails, got.Status, got.Outcome)
	}
}

// --- F5: a required task the plan disables cannot be silently accepted. ---

func TestDisabledRequiredTaskNeedsReview(t *testing.T) {
	p := reviewBoundaryPlan("v3-required-disabled")
	p.Artifacts["report"] = models.Artifact{Key: "report", Value: "core report", ProducerTaskID: "core"}
	disabled := false
	p.Nodes = append(p.Nodes, &models.TaskNode{ID: "required-validation", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusSkipped, Enabled: &disabled})
	p.Acceptance.RequiredTaskIDs = append(p.Acceptance.RequiredTaskIDs, "required-validation")
	got := reviewBoundaryReconcile(t, p)
	if got.Outcome == models.OutcomeCompleted || got.Outcome == models.OutcomePartial {
		t.Fatalf("required task disabled by the plan but core goal accepted: %s/%s", got.Status, got.Outcome)
	}
	if got.Outcome != models.OutcomeNeedsReview {
		t.Fatalf("want completed/needs_review for a contradictory contract, got %s/%s", got.Status, got.Outcome)
	}
}
