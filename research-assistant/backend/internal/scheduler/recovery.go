package scheduler

import (
	"context"
	"fmt"
	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/recovery"
	"time"
)

func (s *Scheduler) recoveryContext(ctx context.Context, plan *models.PlanGraph, task *models.TaskNode) (context.Context, error) {
	state := models.RecoveryState{ChainID: recovery.Hash([]string{plan.OwnerID, plan.ID, task.ID}), MaxActions: 2, MaxHTTPAttempts: 64}
	if task.Recovery != nil {
		state = *task.Recovery
	}
	controller := recovery.New(state, func(next models.RecoveryState) error {
		return s.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
			node := getNodeByID(current, task.ID)
			if node == nil || !executionMatches(node, task.ExecutionID, task.LeaseOwner, task.ExecutionEpoch) {
				return ErrStaleExecution
			}
			node.Recovery = &next
			return nil
		})
	})
	// Persist the original allowance before the first HTTP/tool dispatch.
	if err := s.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		node := getNodeByID(current, task.ID)
		if node == nil || !executionMatches(node, task.ExecutionID, task.LeaseOwner, task.ExecutionEpoch) {
			return ErrStaleExecution
		}
		initial := controller.Snapshot()
		node.Recovery = &initial
		return nil
	}); err != nil {
		return ctx, err
	}
	ctx = recovery.WithController(ctx, controller)
	ctx = agentprofile.WithExecutionGuard(ctx, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, err := s.store.GetPlan(plan.ID)
		if err != nil {
			return err
		}
		node := getNodeByID(current, task.ID)
		if node == nil || !executionMatches(node, task.ExecutionID, task.LeaseOwner, task.ExecutionEpoch) {
			return ErrStaleExecution
		}
		if current.Status == models.StatusCanceled || node.Status == models.StatusCanceled {
			return context.Canceled
		}
		if node.LeaseExpiresAt != nil && node.LeaseExpiresAt.Before(time.Now()) {
			return fmt.Errorf("execution_lease_expired")
		}
		return nil
	})
	return ctx, nil
}
