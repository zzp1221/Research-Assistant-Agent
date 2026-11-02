package api

import (
	"context"

	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
)

// apiMemorySink bridges the scheduler's optional MemorySink to the process-wide
// memory store. Failures are logged by callers as best-effort only.
type apiMemorySink struct{}

func (apiMemorySink) RecordPlan(ctx context.Context, plan *models.PlanGraph) error {
	store := MemoryStore()
	if store == nil || !memory.Enabled() {
		return nil
	}
	if err := memory.RecordPlan(ctx, store, plan); err != nil {
		return err
	}
	// W1: the deterministic plan records become wiki points and the paper page
	// is refreshed. Wiki failures never change the plan result.
	tagAndRefreshPlan(store, plan)
	return nil
}

func (apiMemorySink) RecordTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode, result *models.TaskExecutionResult) error {
	store := MemoryStore()
	if store == nil || !memory.Enabled() {
		return nil
	}
	if err := memory.RecordTask(ctx, store, plan, task, result); err != nil {
		return err
	}
	tagTaskAndIngest(ctx, store, plan, task, result)
	return nil
}
