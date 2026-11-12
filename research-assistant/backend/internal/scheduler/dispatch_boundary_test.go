package scheduler

// Regression tests for the second independent review of the 2026-11-11
// scheduler remediation (reports/scheduler-independent-review-2026-11-12/review.md).
// Each case fixes one interleaving or trust boundary with channels/barriers
// rather than sleeps, so it reproduces deterministically.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/store"
)

type v2QuestionBeforeLeaseStore struct {
	store.PlanStore
	inserted atomic.Bool
}

func (s *v2QuestionBeforeLeaseStore) UpdatePlan(id string, update func(*models.PlanGraph) error) error {
	if s.inserted.CompareAndSwap(false, true) {
		if err := s.PlanStore.UpdatePlan(id, func(p *models.PlanGraph) error {
			p.Status = models.StatusAwaitingInput
			p.Nodes[0].Status = models.StatusAwaitingInput
			p.Questions = []models.PlanQuestion{{ID: "q1", PlanID: p.ID, TaskID: "core", Prompt: "supply dataset", Status: models.QuestionPending, RequestedAt: time.Now()}}
			return nil
		}); err != nil {
			return err
		}
	}
	return s.PlanStore.UpdatePlan(id, update)
}

func TestLeaseRefusesTaskWithPendingQuestion(t *testing.T) {
	p := reviewBoundaryPlan("v2-manual-question")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusPending
	p.Nodes[0].Status = models.StatusPending
	p.Nodes[0].OutputArtifacts = nil
	// At the initial API/scheduler check there is no question. Commit the
	// question immediately before the lease transaction to exercise its guard.
	st := &v2QuestionBeforeLeaseStore{PlanStore: store.NewMemoryPlanStore()}
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s := NewScheduler(st, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		calls++
		return &models.TaskExecutionResult{Status: models.StatusCompleted}, nil
	}), nil, 1)
	_, err := s.ExecuteTask(context.Background(), p.ID, "core")
	got, _ := st.GetPlan(p.ID)
	if calls != 0 {
		t.Fatalf("executor launched during pending question: calls=%d err=%v task=%s question=%s", calls, err, got.Nodes[0].Status, got.Questions[0].Status)
	}
}

// Return both pre-dispatch snapshots before allowing either request to acquire
// its lease. This fixes the interleaving without sleeps or probability loops.
type v2SnapshotBarrierStore struct {
	store.PlanStore
	arrivals atomic.Int32
	gate     chan struct{}
}

func (s *v2SnapshotBarrierStore) GetPlan(id string) (*models.PlanGraph, error) {
	p, err := s.PlanStore.GetPlan(id)
	if err != nil {
		return nil, err
	}
	n := s.arrivals.Add(1)
	if n <= 2 {
		if n == 2 {
			close(s.gate)
		}
		<-s.gate
	}
	return p, nil
}

func TestConcurrentManualTakesOnlyOneLease(t *testing.T) {
	p := reviewBoundaryPlan("v2-concurrent-manual")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusPending
	p.Nodes[0].Status = models.StatusPending
	p.Nodes[0].OutputArtifacts = nil
	base := store.NewMemoryPlanStore()
	if err := base.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	st := &v2SnapshotBarrierStore{PlanStore: base, gate: make(chan struct{})}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int32
	s := NewScheduler(st, taskExecutorFunc(func(ctx context.Context, _ *models.PlanGraph, _ *models.TaskNode) (*models.TaskExecutionResult, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &models.TaskExecutionResult{Status: models.StatusCompleted}, nil
	}), nil, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() { _, err := s.ExecuteTask(ctx, p.ID, "core"); done <- err }()
	}
	completed := 0
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("first executor did not start")
	}
	select {
	case <-entered:
	case <-done:
		completed++
	case <-ctx.Done():
		close(release)
		t.Fatal("second request neither refused nor started")
	}
	close(release)
	for completed < 2 {
		select {
		case err := <-done:
			t.Logf("request error=%v", err)
			completed++
		case <-ctx.Done():
			t.Fatal("request did not return")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("same task executed %d times concurrently despite maxConcurrent=1", calls.Load())
	}
}

func TestLazyQuestionTimeoutRunsTerminalPipelineOnce(t *testing.T) {
	p := reviewBoundaryPlan("v2-lazy-question")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusAwaitingInput
	p.Nodes[0].Status = models.StatusAwaitingInput
	past := time.Now().Add(-time.Minute)
	p.Questions = []models.PlanQuestion{{ID: "q1", PlanID: p.ID, TaskID: "core", Prompt: "supply data", Status: models.QuestionPending, RequestedAt: past.Add(-time.Minute), ExpiresAt: &past}}
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	s := NewScheduler(st, nil, nil, 1)
	s.onTerminal = func(context.Context, *models.PlanGraph) { callbacks++ }
	expired, err := s.ExpireQuestionsNow(p.ID)
	if err != nil || !expired {
		t.Fatalf("expired=%v err=%v", expired, err)
	}
	events, _ := st.ListEvents(p.ID)
	terminalEvents := 0
	for _, e := range events {
		if e.EventType == "plan_failed" {
			terminalEvents++
		}
	}
	got, _ := st.GetPlan(p.ID)
	if callbacks != 1 || terminalEvents != 1 {
		t.Fatalf("lazy sweep committed %s/%s but omitted terminal pipeline: callbacks=%d plan_failed=%d", got.Status, got.Outcome, callbacks, terminalEvents)
	}
}

func TestRetryCannotRecertifyOldArtifact(t *testing.T) {
	p := reviewBoundaryPlan("v2-retry-old-evidence")
	p.Status = models.StatusFailed
	p.Outcome = models.OutcomeFailed
	p.CurrentRevision = 1
	p.Nodes[0].Status = models.StatusFailed
	p.Nodes[0].Error = "first run failed after writing diagnostic"
	p.Artifacts["report"] = models.Artifact{Key: "report", Value: "old diagnostic", ProducerTaskID: "core", Revision: 1, CreatedAt: time.Now().Add(-time.Hour)}
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(st, taskExecutorFunc(func(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
		// The retry finishes but does not re-produce the diagnostic artifact.
		return &models.TaskExecutionResult{Status: models.StatusCompleted, Result: "completed without a new report"}, nil
	}), nil, 1)
	s.SetFinalizer(&planner.PlanFinalizer{})
	if err := s.RetryTask(p.ID, "core"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecuteTask(context.Background(), p.ID, "core"); err != nil {
		t.Logf("retry rejected: %v", err)
		return
	}
	latest, _ := st.GetPlan(p.ID)
	if _, err := s.reconcilePlanState(context.Background(), latest); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetPlan(p.ID)
	if got.Outcome == models.OutcomeCompleted || got.Outcome == models.OutcomePartial {
		t.Fatalf("previous failed run's report was re-certified after retry: %s/%s details=%+v artifact=%+v", got.Status, got.Outcome, got.OutcomeDetails, got.Artifacts["report"])
	}
}

func TestClaimThresholdRequiresTrustedGraph(t *testing.T) {
	p := reviewBoundaryPlan("v2-untrusted-claim-graph")
	p.CurrentRevision = 2
	p.Nodes[1].OutputArtifacts = []string{"claim_evidence_graph"}
	p.Acceptance.Goals[0].EvidenceRefs = nil
	p.Acceptance.Goals[0].MinVerifiedClaims = 1
	p.Artifacts["claim_evidence_graph"] = models.Artifact{Key: "claim_evidence_graph", ProducerTaskID: "optional", Revision: 1, Value: `{"version":"claim.evidence/v1","status":"assessed","summary":{"verified":1,"total_claims":1},"claims":[{"claim_id":"claim-1","status":"verified"}]}`}
	got := reviewBoundaryReconcile(t, p)
	if got.Outcome == models.OutcomeCompleted || got.Outcome == models.OutcomePartial {
		t.Fatalf("failed producer's stale graph met verified-claim threshold: %s/%s details=%+v", got.Status, got.Outcome, got.OutcomeDetails)
	}
}

func TestRequiredTaskMustActuallyComplete(t *testing.T) {
	p := reviewBoundaryPlan("v2-required-skipped")
	p.Artifacts["report"] = models.Artifact{Key: "report", Value: "core report", ProducerTaskID: "core"}
	p.Nodes = append(p.Nodes, &models.TaskNode{ID: "required-validation", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusSkipped})
	p.Acceptance.RequiredTaskIDs = append(p.Acceptance.RequiredTaskIDs, "required-validation")
	got := reviewBoundaryReconcile(t, p)
	if got.Outcome == models.OutcomeCompleted || got.Outcome == models.OutcomePartial {
		t.Fatalf("explicitly required validation was skipped but core accepted: %s/%s", got.Status, got.Outcome)
	}
}
