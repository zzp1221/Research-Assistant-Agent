package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

type eventFaultStore struct {
	store.PlanStore
	appendErr    error
	beforeAppend func(models.PlanEvent)
}

func (s *eventFaultStore) AppendEventReturning(id string, event models.PlanEvent) (models.PlanEvent, error) {
	if s.beforeAppend != nil {
		s.beforeAppend(event)
	}
	if s.appendErr != nil {
		return models.PlanEvent{}, s.appendErr
	}
	return s.PlanStore.AppendEventReturning(id, event)
}

type persistenceCountExecutor struct{ calls atomic.Int32 }

func (e *persistenceCountExecutor) ExecuteTask(context.Context, *models.PlanGraph, *models.TaskNode) (*models.TaskExecutionResult, error) {
	e.calls.Add(1)
	return &models.TaskExecutionResult{Status: models.StatusCompleted}, nil
}

func TestPersistenceFailedEventDoesNotPublishOrTouchActivity(t *testing.T) {
	for _, cause := range []error{fmt.Errorf("%w: disk full", store.ErrNotCommitted), store.ErrCommitUnknown} {
		t.Run(cause.Error(), func(t *testing.T) {
			base := store.NewMemoryPlanStore()
			plan := governanceTestPlan("failed-event", 1)
			if err := base.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			fault := &eventFaultStore{PlanStore: base, appendErr: cause}
			publisher := &capturePublisher{}
			runner := NewScheduler(fault, nil, publisher, 1)
			before := time.Unix(10, 0)
			runner.lastActivity[plan.ID] = before
			err := runner.publishAndStore(plan.ID, models.PlanEvent{PlanID: plan.ID, EventType: "task_log"})
			if !errors.Is(err, cause) {
				t.Fatalf("error = %v, want cause %v", err, cause)
			}
			if len(publisher.events) != 0 || !runner.lastActivity[plan.ID].Equal(before) {
				t.Fatal("uncommitted event published or counted as progress")
			}
			history, _ := base.ListEvents(plan.ID)
			if len(history) != 0 {
				t.Fatal("failed event appeared in history")
			}
		})
	}
}

func TestPersistencePlanStartFailureDoesNotDispatch(t *testing.T) {
	base := store.NewMemoryPlanStore()
	plan := governanceTestPlan("start-event-failure", 1)
	if err := base.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	fault := &eventFaultStore{PlanStore: base, appendErr: store.ErrNotCommitted}
	executor := &persistenceCountExecutor{}
	runner := NewScheduler(fault, executor, &capturePublisher{}, 1)
	if err := runner.ExecutePlan(context.Background(), plan.ID); !errors.Is(err, store.ErrNotCommitted) {
		t.Fatalf("ExecutePlan error = %v", err)
	}
	if executor.calls.Load() != 0 {
		t.Fatal("task executed after plan_started could not be persisted")
	}
	got, _ := base.GetPlan(plan.ID)
	if got.Status != models.StatusInProgress {
		t.Fatalf("separate committed plan state was rewritten as %s", got.Status)
	}
}

func TestPersistenceTaskStartFailureDoesNotDispatch(t *testing.T) {
	base := store.NewMemoryPlanStore()
	plan := governanceTestPlan("task-event-failure", 1)
	plan.Status = models.StatusInProgress
	plan.Nodes[0].Status = models.StatusReady
	if err := base.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	fault := &eventFaultStore{PlanStore: base, appendErr: store.ErrCommitUnknown}
	executor := &persistenceCountExecutor{}
	runner := NewScheduler(fault, executor, nil, 1)
	err := runner.runReadyNodes(context.Background(), plan.ID, plan.Nodes)
	if !errors.Is(err, store.ErrCommitUnknown) {
		t.Fatalf("dispatch error = %v", err)
	}
	if executor.calls.Load() != 0 || len(runner.globalSlots) != 0 {
		t.Fatal("failed start dispatched work or leaked a global slot")
	}
}

func TestPersistenceLogFailureDrainsAndStopsFurtherDispatch(t *testing.T) {
	base := store.NewMemoryPlanStore()
	plan := governanceTestPlan("log-event-failure", 1)
	if err := base.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	fault := &eventFaultStore{PlanStore: base, appendErr: store.ErrNotCommitted}
	publisher := &capturePublisher{}
	runner := NewScheduler(fault, nil, publisher, 1)
	logs := make(chan string)
	done := make(chan struct{})
	go runner.forwardTaskLogs(plan.ID, plan.Nodes[0].ID, logs, done)
	for i := 0; i < 200; i++ {
		logs <- "line"
	}
	close(logs)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("failed log consumer did not drain")
	}
	if len(publisher.events) != 0 {
		t.Fatal("failed logs were published")
	}
	fault.appendErr = nil
	if err := runner.runReadyNodes(context.Background(), plan.ID, plan.Nodes); !errors.Is(err, store.ErrNotCommitted) {
		t.Fatalf("log failure did not stop dispatch: %v", err)
	}
}

func TestPersistenceTerminalEventFailureRecoversOnce(t *testing.T) {
	base := store.NewMemoryPlanStore()
	plan := governanceTestPlan("terminal-event-failure", 1)
	now := time.Now()
	plan.Status, plan.Outcome = models.StatusCompleted, models.OutcomeCompleted
	plan.Usage.FinishedAt = &now
	if err := base.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	fault := &eventFaultStore{PlanStore: base, appendErr: store.ErrNotCommitted}
	publisher := &capturePublisher{}
	first := NewScheduler(fault, nil, publisher, 1)
	var callbacks atomic.Int32
	first.onTerminal = func(context.Context, *models.PlanGraph) { callbacks.Add(1) }
	first.completeTerminalOnce(context.Background(), plan.ID)
	if callbacks.Load() != 0 || len(publisher.events) != 0 {
		t.Fatal("terminal side effects ran without an event")
	}
	fault.appendErr = nil
	// A new scheduler simulates recovery after a claimed terminal transition.
	second := NewScheduler(fault, nil, publisher, 1)
	second.onTerminal = first.onTerminal
	if !second.ReconcileTerminalCommits(context.Background(), plan.ID) {
		t.Fatal("missing claimed event was not recovered")
	}
	second.ReconcileTerminalCommits(context.Background(), plan.ID)
	history, _ := base.ListEvents(plan.ID)
	if len(history) != 1 || len(publisher.events) != 1 || callbacks.Load() != 1 {
		t.Fatalf("terminal recovery events=%d published=%d callbacks=%d", len(history), len(publisher.events), callbacks.Load())
	}
}

func TestPersistenceConcurrentTerminalRecoveryDoesNotStealLiveClaim(t *testing.T) {
	base := store.NewMemoryPlanStore()
	plan := governanceTestPlan("terminal-live-claim", 1)
	now := time.Now()
	plan.Status, plan.Outcome = models.StatusCompleted, models.OutcomeCompleted
	plan.Usage.FinishedAt = &now
	if err := base.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	fault := &eventFaultStore{PlanStore: base, beforeAppend: func(event models.PlanEvent) {
		if event.EventType == "plan_completed" {
			once.Do(func() { close(entered); <-release })
		}
	}}
	runner := NewScheduler(fault, nil, nil, 1)
	var callbacks atomic.Int32
	runner.onTerminal = func(context.Context, *models.PlanGraph) { callbacks.Add(1) }
	done := make(chan struct{})
	go func() { runner.completeTerminalOnce(context.Background(), plan.ID); close(done) }()
	<-entered
	recovered := make(chan struct{})
	go func() { runner.ReconcileTerminalCommits(context.Background(), plan.ID); close(recovered) }()
	close(release)
	<-done
	<-recovered
	history, _ := base.ListEvents(plan.ID)
	if len(history) != 1 || callbacks.Load() != 1 {
		t.Fatalf("live claim duplicated: events=%d callbacks=%d", len(history), callbacks.Load())
	}
}
