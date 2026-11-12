// Regression tests for the independent review of the 2026-11-11 scheduler
// remediation (reports/scheduler-independent-review-2026-11-11/review.md).
// Each case pins one boundary the review reproduced as broken, so the fix
// cannot silently regress.

package scheduler

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/store"
)

type reviewBoundaryExecutor func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error)

func (fn reviewBoundaryExecutor) ExecuteTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (*models.TaskExecutionResult, error) {
	return fn(ctx, plan, task)
}

func reviewBoundaryPlan(id string) *models.PlanGraph {
	now := time.Now()
	optional := true
	return &models.PlanGraph{ID: id, Status: models.StatusInProgress, IntentType: "Code_Execution", Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
		Nodes: []*models.TaskNode{
			{ID: "core", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusCompleted, OutputArtifacts: []string{"report"}, CreatedAt: now, UpdatedAt: now},
			{ID: "optional", Type: "generate_code", AssignedTo: "coder_agent", Optional: &optional, Status: models.StatusFailed, Error: "metric failed", CreatedAt: now, UpdatedAt: now},
		},
		Acceptance: &models.AcceptanceContract{Goals: []models.AcceptanceGoal{{ID: "g1", Statement: "core passed", EvidenceRefs: []string{"report"}}}, RequiredTaskIDs: []string{"core"}},
	}
}
func reviewBoundaryReconcile(t *testing.T, p *models.PlanGraph) *models.PlanGraph {
	t.Helper()
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(st, nil, nil, 1)
	s.SetFinalizer(&planner.PlanFinalizer{})
	if _, err := s.reconcilePlanState(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetPlan(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("status=%s outcome=%s reason=%q details=%+v", got.Status, got.Outcome, got.OutcomeReason, got.OutcomeDetails)
	return got
}
func TestTerminalCoreBlockedBeatsPartial(t *testing.T) {
	p := reviewBoundaryPlan("review-core-blocked")
	p.Artifacts["report"] = models.Artifact{Key: "report", Value: "valid report", ProducerTaskID: "core"}
	p.Nodes = append(p.Nodes, &models.TaskNode{ID: "required-setup", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusBlocked, Error: "missing dataset"})
	p.Acceptance.RequiredTaskIDs = append(p.Acceptance.RequiredTaskIDs, "required-setup")
	got := reviewBoundaryReconcile(t, p)
	if got.Outcome != models.OutcomeBlocked {
		t.Fatalf("core task remains blocked, want blocked; got %s/%s", got.Status, got.Outcome)
	}
}
func TestTerminalCoreBlockedBeatsSmoke(t *testing.T) {
	p := reviewBoundaryPlan("review-blocked-smoke")
	p.IntentType = "Paper_Reproduction"
	p.Nodes[0].Status = models.StatusBlocked
	p.Nodes[0].Error = "missing repository"
	p.Artifacts["reproduction_mode_report"] = models.Artifact{Key: "reproduction_mode_report", Value: `{"effective_mode":"smoke"}`}
	got := reviewBoundaryReconcile(t, p)
	if got.Outcome != models.OutcomeBlocked {
		t.Fatalf("core task never ran, want blocked; got %s/%s", got.Status, got.Outcome)
	}
}
func TestTerminalNoContractCannotBePartial(t *testing.T) {
	p := reviewBoundaryPlan("review-no-contract")
	p.Acceptance = nil
	got := reviewBoundaryReconcile(t, p)
	if got.Outcome == models.OutcomePartial {
		t.Fatalf("partial emitted without any acceptance contract or accepted goal")
	}
}
func TestTerminalCoreCanceledIsNotCompleted(t *testing.T) {
	p := reviewBoundaryPlan("review-core-canceled")
	p.Acceptance = nil
	p.Nodes = p.Nodes[:1]
	p.Nodes[0].Status = models.StatusCanceled
	got := reviewBoundaryReconcile(t, p)
	if got.Status == models.StatusCompleted {
		t.Fatalf("only core task canceled but plan reports %s/%s", got.Status, got.Outcome)
	}
}
func TestTerminalDiagnosticArtifactNeverVerifiesGoal(t *testing.T) {
	p := reviewBoundaryPlan("review-diagnostic-evidence")
	p.Nodes[1].OutputArtifacts = []string{"metric"}
	p.Acceptance.Goals[0].EvidenceRefs = []string{"metric"}
	p.Artifacts["metric"] = models.Artifact{Key: "metric", Value: "unverified diagnostic", ProducerTaskID: "optional", Revision: 1}
	p.CurrentRevision = 2
	got := reviewBoundaryReconcile(t, p)
	if got.Outcome == models.OutcomePartial || (got.OutcomeDetails != nil && len(got.OutcomeDetails.AcceptedGoals) > 0 && got.OutcomeDetails.AcceptedGoals[0].Verified) {
		t.Fatalf("failed producer's stale diagnostic accepted as verified evidence")
	}
}
func TestManualEntryRespectsPendingApproval(t *testing.T) {
	p := reviewBoundaryPlan("review-manual-wait")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusAwaitingApproval
	p.Nodes[0].Status = models.StatusAwaitingApproval
	p.Nodes[0].OutputArtifacts = nil
	p.RuntimeActions = []models.RuntimeActionRequest{{ID: "pending", TaskID: "core", Kind: models.RuntimeActionToolCall, Status: models.RuntimeActionPending, RequestedAt: time.Now()}}
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s := NewScheduler(st, reviewBoundaryExecutor(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		calls++
		return &models.TaskExecutionResult{Status: models.StatusCompleted}, nil
	}), nil, 1)
	_, err := s.ExecuteTask(context.Background(), p.ID, "core")
	got, _ := st.GetPlan(p.ID)
	if calls != 0 {
		t.Fatalf("manual entry executed %d time(s) with approval still pending: err=%v root=%s action=%s", calls, err, got.Nodes[0].Status, got.RuntimeActions[0].Status)
	}
}
func TestManualProtocolErrorReleasesLease(t *testing.T) {
	p := reviewBoundaryPlan("review-manual-protocol")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusPending
	p.Nodes[0].Status = models.StatusPending
	p.Nodes[0].OutputArtifacts = nil
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(st, reviewBoundaryExecutor(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		return &models.TaskExecutionResult{Status: models.StatusAwaitingApproval}, nil
	}), nil, 1)
	_, err := s.ExecuteTask(context.Background(), p.ID, "core")
	if err == nil {
		t.Fatal("expected protocol error")
	}
	got, _ := st.GetPlan(p.ID)
	if got.Nodes[0].Status == models.StatusInProgress || got.Nodes[0].ExecutionID != "" {
		t.Fatalf("manual execution returned protocol error but left status=%s lease=%q", got.Nodes[0].Status, got.Nodes[0].ExecutionID)
	}
}
func TestBlockedNodeCarriesReadableMessage(t *testing.T) {
	p := reviewBoundaryPlan("review-block-message")
	p.Nodes[0].Status = models.StatusFailed
	p.Nodes = append(p.Nodes, &models.TaskNode{ID: "child", Status: models.StatusPending, Dependencies: []string{"core"}})
	propagateBlockToDependents(p, p.Nodes[0], time.Now())
	c := p.Nodes[2]
	if c.BlockInfo == nil || c.BlockInfo.Message == "" {
		t.Fatalf("propagated block_info has no message: %+v; task.error=%q", c.BlockInfo, c.Error)
	}
}
func TestExplicitBindingRejectsFailedOptionalArtifact(t *testing.T) {
	p := reviewBoundaryPlan("review-binding")
	p.Nodes[1].OutputArtifacts = []string{"metric"}
	consumer := &models.TaskNode{ID: "consumer", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusPending, OptionalDependencies: []string{"optional"}, InputBindings: []models.InputBinding{{Target: "metric", Source: models.BindingSource{Kind: "artifact", Name: "metric"}, Required: false}}}
	p.Nodes = append(p.Nodes, consumer)
	p.Artifacts["metric"] = models.Artifact{Key: "metric", Value: "failed diagnostic", ProducerTaskID: "optional"}
	inputs, err := (&planner.ContextBinder{}).BuildInputs(p, consumer)
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := inputs["metric"]; ok {
		t.Fatalf("explicit artifact binding bypasses optional failure filtering: metric=%v", value)
	}
}
func TestQuestionTimeoutFinalizesPlanOnce(t *testing.T) {
	p := reviewBoundaryPlan("review-question-timeout")
	p.Status = models.StatusPending
	p.Nodes = p.Nodes[:1]
	p.Nodes[0].Status = models.StatusAwaitingInput
	past := time.Now().Add(-time.Minute)
	p.Questions = []models.PlanQuestion{{ID: "expired", TaskID: "core", Prompt: "provide data", Status: models.QuestionPending, RequestedAt: past.Add(-time.Minute), ExpiresAt: &past}}
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	s := NewScheduler(st, nil, nil, 1)
	s.onTerminal = func(context.Context, *models.PlanGraph) { callbacks++ }
	if err := s.ExecutePlan(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	events, _ := st.ListEvents(p.ID)
	terminalEvents := 0
	for _, e := range events {
		if e.EventType == "plan_failed" {
			terminalEvents++
		}
	}
	if callbacks != 1 || terminalEvents != 1 {
		t.Fatalf("question timeout skipped terminal pipeline: callbacks=%d plan_failed=%d", callbacks, terminalEvents)
	}
}
func TestNeedsReviewKeepsExecutionCompleted(t *testing.T) {
	p := reviewBoundaryPlan("review-needs-review")
	p.Nodes = p.Nodes[:1]
	p.Artifacts = map[string]models.Artifact{}
	got := reviewBoundaryReconcile(t, p)
	if got.Outcome != models.OutcomeNeedsReview || got.Status != models.StatusCompleted {
		t.Fatalf("semantic review has changed execution completion into failure: status=%s outcome=%s", got.Status, got.Outcome)
	}
}
