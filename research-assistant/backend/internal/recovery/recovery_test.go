package recovery

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestLayersSharePersistedAllowanceAfterRestart(t *testing.T) {
	var saved models.RecoveryState
	controller := New(models.RecoveryState{ChainID: "chain", MaxActions: 2}, func(state models.RecoveryState) error { saved = state; return nil })
	ctx := WithController(context.Background(), controller)
	if err := HTTPAttempt(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := HTTPAttempt(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := Reserve(ctx, "local_repair", Classify(errors.New("schema mismatch"), "repair", "code-v1")); err != nil {
		t.Fatal(err)
	}
	restarted := New(saved, nil)
	if err := restarted.Reserve("task_retry", Classify(errors.New("HTTP 503"), "task", "code-v2")); err == nil {
		t.Fatal("outer layer renewed spent recovery allowance")
	}
	if got := restarted.Snapshot(); got.UsedActions != 2 || got.HTTPAttempts != 2 {
		t.Fatalf("lost counters: %#v", got)
	}
}

func TestNoProgressIgnoresVolatilePathsAndRequiresRelevantChange(t *testing.T) {
	first := Classify(errors.New("2026-11-08T12:34:56Z ModuleNotFoundError at /tmp/random-one/file.py"), "dependency", "environment-v1")
	second := Classify(errors.New("2026-11-08T13:59:00Z ModuleNotFoundError at /tmp/random-two/file.py"), "dependency", "environment-v1")
	if first.Fingerprint != second.Fingerprint {
		t.Fatal("volatile details changed deterministic failure fingerprint")
	}
	c := New(models.RecoveryState{ChainID: "chain", MaxActions: 3}, nil)
	if err := c.Reserve("repair", first); err != nil {
		t.Fatal(err)
	}
	if err := c.Reserve("task_retry", second); err == nil || err.Error() != "recovery_no_progress" {
		t.Fatalf("repeated unchanged failure: %v", err)
	}
	changed := Classify(errors.New(second.Message), "dependency", "environment-v2")
	if err := c.Reserve("repair", changed); err != nil {
		t.Fatalf("relevant change not allowed: %v", err)
	}
}

func TestFailedPersistencePreventsDispatchAndCancellationSpendsNothing(t *testing.T) {
	c := New(models.RecoveryState{ChainID: "chain"}, func(models.RecoveryState) error { return errors.New("stale_execution") })
	if err := c.Reserve("repair", nil); err == nil {
		t.Fatal("dispatched without durable reservation")
	}
	if c.Snapshot().UsedActions != 0 {
		t.Fatal("uncommitted reservation changed memory state")
	}
	ctx, cancel := context.WithCancel(WithController(context.Background(), New(models.RecoveryState{}, nil)))
	cancel()
	if !errors.Is(Reserve(ctx, "repair", nil), context.Canceled) {
		t.Fatal("canceled dispatch allowed")
	}
}

func TestFailureClassesDoNotRetryMissingInputPermissionOrBudget(t *testing.T) {
	for _, test := range []struct{ message, class string }{{"missing dataset", "missing_input"}, {"tool_forbidden", "permission"}, {"approval denied", "permission"}, {"token budget exceeded", "budget"}, {"context canceled", "canceled"}, {"HTTP 429", "transient"}, {"No module named torch", "environment"}, {"output schema invalid", "output"}} {
		t.Run(test.class, func(t *testing.T) {
			f := Classify(fmt.Errorf("%s", test.message), "task", "input")
			if f.Class != test.class {
				t.Fatalf("%q classified as %s", test.message, f.Class)
			}
			if test.class == "missing_input" || test.class == "permission" || test.class == "budget" || test.class == "canceled" {
				if err := New(models.RecoveryState{}, nil).Reserve("task_retry", f); err == nil {
					t.Fatal("forbidden recovery permitted")
				}
			}
		})
	}
}

func TestVerifiedRepairPersistsRawEvidenceBeforeLedger(t *testing.T) {
	var saved models.RecoveryState
	c := New(models.RecoveryState{ChainID: "chain"}, func(next models.RecoveryState) error { saved = next; return nil })
	ctx := WithCompatibility(WithController(context.Background(), c), Compatibility{EnvironmentHash: "env", ProtocolHash: "protocol", ExecutionID: "exec"})
	if err := RecordVerifiedRepair(ctx, "print(missing)", "print(1)", "NameError", "1"); err != nil {
		t.Fatal(err)
	}
	if len(saved.Evidence) != 4 || len(saved.Ledger) != 1 || !saved.Ledger[0].Verified || saved.Ledger[0].BeforeStateHash == saved.Ledger[0].AfterStateHash {
		t.Fatalf("missing repair evidence: %#v", saved)
	}
	if err := RecordVerifiedRepair(ctx, "print(missing)", "print(1)", "NameError", "1"); err != nil || len(c.Snapshot().Ledger) != 1 {
		t.Fatal("duplicate repair replayed")
	}
}
