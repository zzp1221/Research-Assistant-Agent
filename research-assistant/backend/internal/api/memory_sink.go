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
	return memory.RecordPlan(ctx, store, plan)
}

func (apiMemorySink) RecordTask(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode, result *models.TaskExecutionResult) error {
	store := MemoryStore()
	if store == nil || !memory.Enabled() {
		return nil
	}
	return memory.RecordTask(ctx, store, plan, task, result)
}
