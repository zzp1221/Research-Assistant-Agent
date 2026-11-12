package scheduler

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

// The lazy sweep and the execution loop must be able to notice the same timeout
// without running the terminal side effects twice.
func TestRepeatedQuestionTimeoutFinalizesOnce(t *testing.T) {
	p := reviewBoundaryPlan("repeat-question-timeout")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusAwaitingInput
	p.Nodes[0].Status = models.StatusAwaitingInput
	past := time.Now().Add(-time.Minute)
	p.Questions = []models.PlanQuestion{{
		ID: "q1", PlanID: p.ID, TaskID: "core", Prompt: "supply data",
		Status: models.QuestionPending, RequestedAt: past.Add(-time.Minute), ExpiresAt: &past,
	}}
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	callbacks := 0
	s := NewScheduler(st, nil, nil, 1)
	s.onTerminal = func(context.Context, *models.PlanGraph) { callbacks++ }

	first, err := s.ExpireQuestionsNow(p.ID)
	if err != nil || !first {
		t.Fatalf("first sweep expired=%v err=%v", first, err)
	}
	// A second entry point observes the already-terminal plan.
	second, err := s.ExpireQuestionsNow(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.finishExternalTerminal(context.Background(), p.ID)

	events := mustEvents(t, st, p.ID)
	terminal := 0
	for _, event := range events {
		if event.EventType == "plan_failed" || event.EventType == "plan_completed" {
			terminal++
		}
	}
	if second {
		t.Fatal("a second sweep re-expired an already answered question set")
	}
	if terminal != 1 || callbacks != 1 {
		t.Fatalf("terminal side effects ran %d/%d time(s), want 1", terminal, callbacks)
	}
}

// A retried node must re-produce its artifacts to be trusted again; a completed
// retry without fresh output keeps the node completed but the evidence missing.
func TestRetryInvalidatesProducedArtifacts(t *testing.T) {
	p := reviewBoundaryPlan("retry-invalidates")
	p.Status = models.StatusFailed
	p.CurrentRevision = 1
	p.Nodes[0].Status = models.StatusFailed
	p.Nodes[0].OutputArtifacts = []string{"report"}
	p.Artifacts["report"] = models.Artifact{Key: "report", Value: "old diagnostic", ProducerTaskID: "core", Revision: 1}
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(st, nil, nil, 1)
	if err := s.RetryTask(p.ID, "core"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetPlan(p.ID)
	if _, exists := got.Artifacts["report"]; exists {
		t.Fatalf("retry kept the previous attempt's artifact: %#v", got.Artifacts["report"])
	}
	found := false
	for _, event := range mustEvents(t, st, p.ID) {
		if event.EventType == "artifacts_invalidated" && event.TaskID == "core" {
			found = true
		}
	}
	if !found {
		t.Fatal("no artifacts_invalidated event was recorded")
	}
}

// A plan waiting on a question must refuse an automatic lease too, not only a
// manual request.
func TestQuestionWaitBlocksLeaseAcquisition(t *testing.T) {
	p := reviewBoundaryPlan("question-lease")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusAwaitingInput
	p.Nodes[0].Status = models.StatusPending
	p.Nodes[0].OutputArtifacts = nil
	p.Questions = []models.PlanQuestion{{
		ID: "q1", PlanID: p.ID, TaskID: "core", Prompt: "supply data",
		Status: models.QuestionPending, RequestedAt: time.Now(),
	}}
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(st, nil, nil, 1)
	if _, _, _, err := s.markTaskStarted(p.ID, "core", taskDispatchGuard(p, "core")); err == nil {
		t.Fatal("lease was taken while a question was pending")
	}
	got, _ := st.GetPlan(p.ID)
	if got.Nodes[0].Status != models.StatusPending || got.Nodes[0].ExecutionID != "" {
		t.Fatalf("refused lease still mutated the node: %#v", got.Nodes[0])
	}
}

// A node whose lease is still valid cannot be started by another request.
func TestLiveLeaseBlocksSecondDispatch(t *testing.T) {
	p := reviewBoundaryPlan("live-lease")
	p.Nodes = p.Nodes[:1]
	p.Status = models.StatusInProgress
	future := time.Now().Add(5 * time.Minute)
	p.Nodes[0].Status = models.StatusPending
	p.Nodes[0].OutputArtifacts = nil
	p.Nodes[0].ExecutionID = "exec-1"
	p.Nodes[0].LeaseOwner = "coder_agent"
	p.Nodes[0].LeaseExpiresAt = &future
	st := store.NewMemoryPlanStore()
	if err := st.SavePlan(p); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(st, nil, nil, 1)
	if _, _, _, err := s.markTaskStarted(p.ID, "core", taskDispatchGuard(p, "core")); err == nil {
		t.Fatal("a node with a live lease was started a second time")
	}
}
