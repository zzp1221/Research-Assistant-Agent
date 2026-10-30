package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"research-assistant-backend/internal/agentctx"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
	"research-assistant-backend/internal/usage"
	"sort"
	"time"
)

func (s *Scheduler) findReadyNodes(plan *models.PlanGraph, availableSlots int) []*models.TaskNode {
	if availableSlots <= 0 {
		return nil
	}

	ready := []*models.TaskNode{}
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusReady && node.IsEnabled() {
			ready = append(ready, node)
		}
	}

	if len(ready) == 0 {
		return nil
	}

	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].CreatedAt.Before(ready[j].CreatedAt)
	})

	serial := make([]*models.TaskNode, 0, len(ready))
	parallel := make([]*models.TaskNode, 0, len(ready))
	for _, node := range ready {
		if node.Parallelizable {
			parallel = append(parallel, node)
		} else {
			serial = append(serial, node)
		}
	}

	if len(serial) > 0 {
		return serial[:1]
	}
	if len(parallel) > availableSlots {
		return parallel[:availableSlots]
	}
	return parallel
}

func (s *Scheduler) runReadyNodes(ctx context.Context, planID string, ready []*models.TaskNode) error {
	for _, task := range ready {
		// The global slot bounds concurrent task execution across plans so one
		// user cannot occupy every CPU/sandbox slot. When saturated the caller
		// simply loops again after a wake-up.
		if !s.acquireGlobalSlot() {
			return nil
		}
		action, pending, err := s.prepareRuntimeGate(planID, task.ID)
		if err != nil {
			s.releaseGlobalSlot()
			return err
		}
		if pending {
			s.releaseGlobalSlot()
			return nil
		}
		if action != nil && action.Status == models.RuntimeActionRejected {
			s.releaseGlobalSlot()
			return nil
		}
		// Budget gate: evaluate plan limits and reserve the user budget before
		// any work starts, so an exhausted budget never dispatches another task.
		if blocked, err := s.budgetGate(planID); err != nil {
			s.releaseGlobalSlot()
			return err
		} else if blocked {
			s.releaseGlobalSlot()
			return nil
		}
		planForBudget, err := s.store.GetPlan(planID)
		if err != nil {
			s.releaseGlobalSlot()
			return err
		}
		if _, err := s.reserveTaskBudget(planForBudget, task); err != nil {
			s.releaseGlobalSlot()
			return nil
		}
		executionID, leaseOwner, executionEpoch, err := s.markTaskStarted(planID, task.ID)
		if err != nil {
			s.releaseGlobalSlot()
			return err
		}

		taskID := task.ID
		taskType := task.Type
		go func() {
			// Wake the scheduler and free the global slot on every exit path.
			defer s.notifyProgress()
			defer s.releaseGlobalSlot()
			defer s.touchActivity(planID)
			defer s.releaseTaskReservation(planID, taskID)

			plan, err := s.store.GetPlan(planID)
			if err != nil {
				log.Printf("scheduler: failed to load plan %s for task %s: %v", planID, taskID, err)
				return
			}
			currentTask := getNodeByID(plan, taskID)
			if currentTask == nil {
				log.Printf("scheduler: task not found during execution: %s", taskID)
				return
			}

			logChannel := make(chan string, 64)
			recorder := usage.NewRecorder()
			// Bind the plan owner so runtime model profiles resolve per user.
			execCtx := modelresolver.WithOwner(usage.WithRecorder(agentctx.WithLogChannel(ctx, logChannel), recorder), plan.OwnerID)
			taskTimeout := time.Duration(currentTask.TimeoutSeconds) * time.Second
			if taskTimeout <= 0 {
				taskTimeout = 10 * time.Minute
			}
			taskCtx, cancelTask := context.WithTimeout(execCtx, taskTimeout)
			forwardDone := make(chan struct{})
			go s.forwardTaskLogs(planID, taskID, logChannel, forwardDone)
			startedAt := time.Now()

			result, execErr := s.executor.ExecuteTask(taskCtx, plan, currentTask)
			if errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
				execErr = fmt.Errorf("task execution timed out after %s: %w", taskTimeout, taskCtx.Err())
			} else if errors.Is(taskCtx.Err(), context.Canceled) {
				execErr = context.Canceled
			}
			cancelTask()
			close(logChannel)
			<-forwardDone

			if execErr != nil {
				result = &models.TaskExecutionResult{
					Status: models.StatusFailed,
					Error:  execErr.Error(),
				}
			}
			if result == nil {
				result = &models.TaskExecutionResult{
					Status: models.StatusFailed,
					Error:  "task execution returned nil result",
				}
			}
			result.Usage = taskUsageFromSnapshot(recorder.Snapshot(), time.Since(startedAt))
			metrics.RecordTask(taskType, string(result.Status), time.Since(startedAt))
			s.settleTaskBudget(planID, taskID, result.Usage)

			// A structured question pauses the task without consuming a retry or
			// failing the plan; the scheduler resumes it after the answer.
			if result.Question != nil {
				if err := s.applyQuestionFromTask(planID, taskID, result.Question); err != nil {
					log.Printf("scheduler: persist question failed for %s: %v", taskID, err)
				}
				return
			}

			if result.Status != models.StatusCompleted {
				if result.Error == "" {
					result.Error = fmt.Sprintf("task returned non-completed status %q", result.Status)
				}
				retrying, err := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, result, true)
				if errors.Is(err, ErrStaleExecution) {
					s.publishDiscardedResult(planID, taskID, executionID, leaseOwner)
					return
				}
				if err != nil {
					log.Printf("scheduler: markTaskFailed failed for %s: %v", taskID, err)
					return
				}
				if !retrying && shouldBlockOnFailure(currentTask) {
					if err := s.blockDependents(planID, taskID); err != nil {
						log.Printf("scheduler: blockDependents failed for %s: %v", taskID, err)
						return
					}
				}
				s.postTaskBudgetCheck(planID)
				return
			}

			if err := s.markTaskCompleted(planID, taskID, executionID, leaseOwner, executionEpoch, result); errors.Is(err, ErrStaleExecution) {
				s.publishDiscardedResult(planID, taskID, executionID, leaseOwner)
				return
			} else if err != nil {
				log.Printf("scheduler: markTaskCompleted failed for %s: %v", taskID, err)
				failed := &models.TaskExecutionResult{Status: models.StatusFailed, Error: err.Error()}
				if _, markErr := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, failed, true); markErr != nil {
					log.Printf("scheduler: markTaskFailed after completion validation failed for %s: %v", taskID, markErr)
				}
				s.postTaskBudgetCheck(planID)
				return
			}
			s.postTaskBudgetCheck(planID)
		}()
	}
	return nil
}

// taskUsageFromSnapshot converts the usage recorder output into the persisted
// task usage shape. Duration is measured by the scheduler so it includes agent
// overhead beyond the individual LLM calls.
func taskUsageFromSnapshot(snapshot usage.Snapshot, duration time.Duration) *models.TaskUsage {
	taskUsage := &models.TaskUsage{
		LLMPromptTokens:     snapshot.PromptTokens,
		LLMCompletionTokens: snapshot.CompletionTokens,
		LLMCalls:            snapshot.LLMCalls,
		LLMCostUSD:          snapshot.CostUSD,
		LLMEstimated:        snapshot.Estimated,
		SandboxSeconds:      snapshot.SandboxSeconds,
		DurationSeconds:     duration.Seconds(),
		Attempts:            1,
	}
	if len(snapshot.ByModel) > 0 {
		taskUsage.Models = map[string]int{}
		for model, entry := range snapshot.ByModel {
			taskUsage.Models[model] = entry.Calls
		}
	}
	return taskUsage
}

func applyTaskUsage(task *models.TaskNode, plan *models.PlanGraph, taskUsage *models.TaskUsage) {
	if task == nil || taskUsage == nil {
		return
	}
	if task.Usage == nil {
		task.Usage = &models.TaskUsage{}
	}
	task.Usage.Add(taskUsage)
	if plan != nil {
		plan.Usage.AddTaskUsage(taskUsage)
	}
	// Prompt-budget diagnostics live in task metadata; surface them in usage
	// and metrics so truncation is observable.
	if task.Metadata != nil {
		if report, ok := task.Metadata["prompt_budget"].(prompts.PromptReport); ok {
			task.Usage.PromptBudgetLimit = report.Budget
			task.Usage.PromptTruncatedSections = len(report.TruncatedSections())
			for _, section := range report.Sections {
				if section.Truncated || section.Dropped {
					metrics.RecordPromptTruncatedSection(section.Name)
				}
			}
		}
	}
}

func (s *Scheduler) forwardTaskLogs(planID, taskID string, logChannel <-chan string, done chan<- struct{}) {
	defer close(done)
	for line := range logChannel {
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:     planID,
			EventType:  "task_log",
			TaskID:     taskID,
			TaskStatus: string(models.StatusInProgress),
			Payload: map[string]any{
				"message": line,
			},
			Timestamp: time.Now(),
		})
	}
}
