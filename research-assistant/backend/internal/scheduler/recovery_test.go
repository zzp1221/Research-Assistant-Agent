package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/recovery"
	"research-assistant-backend/internal/store"
)

func TestNestedRecoveryIsPersistedAndOuterRetryCannotRenewIt(t *testing.T) {
	plans := store.NewMemoryPlanStore()
	plan := governanceTestPlan("recovery-chain", 1)
	plan.Nodes[0].RetryLimit = 5
	if err := plans.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	s := NewScheduler(plans, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	execution, owner, epoch, err := s.markTaskStarted(plan.ID, plan.Nodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := plans.GetPlan(plan.ID)
	node := current.Nodes[0]
	ctx, err := s.recoveryContext(context.Background(), current, node)
	if err != nil {
		t.Fatal(err)
	}
	if err = recovery.HTTPAttempt(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err = recovery.Reserve(ctx, "local_repair", recovery.Classify(errors.New("invalid schema"), "repair", "v1")); err != nil {
		t.Fatal(err)
	}
	saved, _ := plans.GetPlan(plan.ID)
	if saved.Nodes[0].Recovery.UsedActions != 2 {
		t.Fatal("allowance was not durable before result")
	}
	retrying, err := s.markTaskFailed(plan.ID, node.ID, execution, owner, epoch, &models.TaskExecutionResult{Status: models.StatusFailed, Error: "HTTP 503"}, true)
	if err != nil || retrying {
		t.Fatalf("outer task renewed nested allowance: %v %v", retrying, err)
	}
	if err = agentprofile.CheckExecution(ctx); !errors.Is(err, ErrStaleExecution) {
		t.Fatalf("old execution guard accepted: %v", err)
	}
}

func TestClassifiedFailuresWaitBlockCancelAndOnlyProposeReplan(t *testing.T) {
	for _, test := range []struct {
		message  string
		status   models.TaskStatus
		proposal bool
	}{{"missing dataset", models.StatusBlocked, false}, {"approval denied", models.StatusBlocked, false}, {"context canceled", models.StatusCanceled, false}, {"ModuleNotFoundError: torch", models.StatusFailed, true}} {
		t.Run(test.message, func(t *testing.T) {
			plans := store.NewMemoryPlanStore()
			plan := governanceTestPlan("classify", 1)
			plan.Nodes[0].RetryLimit = 3
			plan.CurrentRevision = 4
			_ = plans.SavePlan(plan)
			s := NewScheduler(plans, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
			id, owner, epoch, _ := s.markTaskStarted(plan.ID, plan.Nodes[0].ID)
			retry, err := s.markTaskFailed(plan.ID, plan.Nodes[0].ID, id, owner, epoch, &models.TaskExecutionResult{Status: models.StatusFailed, Error: test.message}, true)
			if err != nil || retry {
				t.Fatalf("unexpected recovery: %v %v", retry, err)
			}
			saved, _ := plans.GetPlan(plan.ID)
			node := saved.Nodes[0]
			if node.Status != test.status || (node.ReplanProposal != nil) != test.proposal {
				t.Fatalf("incorrect classification: %#v", node)
			}
			if saved.CurrentRevision != 4 || len(saved.Nodes) != 1 {
				t.Fatal("replan proposal changed running graph")
			}
		})
	}
}

func TestTransientRetryHonorsBackoffAndCompletionSavesProvenance(t *testing.T) {
	plans := store.NewMemoryPlanStore()
	plan := governanceTestPlan("backoff", 1)
	plan.Nodes[0].RetryLimit = 1
	plan.CurrentRevision = 2
	_ = plans.SavePlan(plan)
	s := NewScheduler(plans, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	id, owner, epoch, _ := s.markTaskStarted(plan.ID, plan.Nodes[0].ID)
	failure := recovery.Classify(errors.New("HTTP 429"), "model", "v1")
	failure.RetryAfterMS = 3000
	if retry, err := s.markTaskFailed(plan.ID, plan.Nodes[0].ID, id, owner, epoch, &models.TaskExecutionResult{Status: models.StatusFailed, Error: "HTTP 429", Failure: failure}, true); err != nil || !retry {
		t.Fatalf("transient failure did not retry: %v %v", retry, err)
	}
	saved, _ := plans.GetPlan(plan.ID)
	node := saved.Nodes[0]
	if node.NextAttemptAt == nil || time.Until(*node.NextAttemptAt) < 2*time.Second {
		t.Fatal("Retry-After ignored")
	}
	node.Status = models.StatusReady
	if len(s.findReadyNodes(saved, 1)) != 0 {
		t.Fatal("dispatched before retry deadline")
	}
	id, owner, epoch, _ = s.markTaskStarted(plan.ID, node.ID)
	if err := s.markTaskCompleted(plan.ID, node.ID, id, owner, epoch, &models.TaskExecutionResult{Status: models.StatusCompleted, Metadata: map[string]any{"completed_execution_id": "forged", "completed_execution_epoch": 999, "completed_revision": 999}}); err != nil {
		t.Fatal(err)
	}
	saved, _ = plans.GetPlan(plan.ID)
	node = saved.Nodes[0]
	if node.ExecutionID != "" || node.Metadata["completed_execution_id"] != id || node.Metadata["completed_execution_epoch"] != epoch || node.Metadata["completed_revision"] != 2 {
		t.Fatal("completed execution provenance lost")
	}
}

func TestManualRetryResetCannotRenewSpentRecovery(t *testing.T) {
	plans := store.NewMemoryPlanStore()
	plan := governanceTestPlan("manual-budget", 1)
	plan.Status = models.StatusFailed
	node := plan.Nodes[0]
	node.Status = models.StatusFailed
	node.RunCount = 3
	node.Recovery = &models.RecoveryState{ChainID: "chain", MaxActions: 2, UsedActions: 2, HTTPAttempts: 3}
	node.Failure = recovery.Classify(errors.New("HTTP 503"), "task", "state")
	_ = plans.SavePlan(plan)
	s := NewScheduler(plans, NewDefaultTaskExecutor(), &NoopEventPublisher{}, 1)
	if err := s.RetryTask(plan.ID, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.markTaskStarted(plan.ID, node.ID); err == nil {
		t.Fatal("reset RunCount renewed spent allowance")
	}
	saved, _ := plans.GetPlan(plan.ID)
	if saved.Nodes[0].Recovery.UsedActions != 2 || saved.Nodes[0].ExecutionID != "" {
		t.Fatal("exhausted task was dispatched")
	}
}
