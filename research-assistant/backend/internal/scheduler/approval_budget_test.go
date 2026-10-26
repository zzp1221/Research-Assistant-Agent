package scheduler

import (
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

func TestEffectivePlanElapsedExcludesApprovalWait(t *testing.T) {
	started := time.Now().Add(-40 * time.Minute)
	waitingSince := time.Now().Add(-25 * time.Minute)
	plan := &models.PlanGraph{
		Budget: models.RunBudget{MaxDurationSec: 2100},
		Usage: models.RunUsage{
			StartedAt:            &started,
			ApprovalWaitingSince: &waitingSince,
			ApprovalWaitSeconds:  300, // an earlier 5 minute approval window
		},
	}
	elapsed := effectivePlanElapsed(plan, time.Now())
	// 40 minutes total - 25 minutes current wait - 5 minutes previous wait = 10.
	if elapsed > 11*time.Minute || elapsed < 9*time.Minute {
		t.Fatalf("expected about 10 minutes of billable execution, got %v", elapsed)
	}
	if budgetExceeded(plan) {
		t.Fatalf("35 minute budget must not be exceeded while approval wait is excluded")
	}
}

func TestBudgetExceededStillFiresWithoutApprovalWait(t *testing.T) {
	started := time.Now().Add(-60 * time.Minute)
	plan := &models.PlanGraph{
		Budget: models.RunBudget{MaxDurationSec: 2100},
		Usage:  models.RunUsage{StartedAt: &started},
	}
	if !budgetExceeded(plan) {
		t.Fatalf("expected budget to be exceeded after 60 minutes")
	}
}

func TestApprovalTimeoutSecondsDefault(t *testing.T) {
	t.Setenv("APPROVAL_TIMEOUT_SECONDS", "")
	if got := approvalTimeoutSeconds(); got != 1800 {
		t.Fatalf("expected default 1800, got %d", got)
	}
	t.Setenv("APPROVAL_TIMEOUT_SECONDS", "5")
	if got := approvalTimeoutSeconds(); got != 5 {
		t.Fatalf("expected 5, got %d", got)
	}
}
