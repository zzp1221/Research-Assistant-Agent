package scheduler

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/google/uuid"
)

// questionTimeoutSeconds is the default lifetime of a structured question.
func questionTimeoutSeconds() int {
	if raw := strings.TrimSpace(os.Getenv("QUESTION_TIMEOUT_SECONDS")); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			return value
		}
	}
	return 900
}

// hasAwaitingInput reports whether any task is paused on a structured question.
func hasAwaitingInput(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusAwaitingInput {
			return true
		}
	}
	for _, question := range plan.Questions {
		if question.Status == models.QuestionPending {
			return true
		}
	}
	return false
}

// hasAwaitingReview reports whether the plan is held before execution by the
// review gate.
func hasAwaitingReview(plan *models.PlanGraph) bool {
	return plan != nil && plan.Status == models.StatusAwaitingReview
}

// applyQuestionFromTask persists a question returned by a task execution and
// pauses the task/plan instead of marking the task failed.
func (s *Scheduler) applyQuestionFromTask(planID, taskID string, question *models.PlanQuestion) error {
	now := time.Now().UTC()
	event := models.PlanEvent{}
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if question == nil {
			return fmt.Errorf("question is nil")
		}
		if question.ID == "" {
			question.ID = "q-" + uuid.NewString()
		}
		question.PlanID = planID
		question.TaskID = taskID
		question.NodeType = task.Type
		question.Revision = task.Revision
		question.Status = models.QuestionPending
		if question.RequestedAt.IsZero() {
			question.RequestedAt = now
		}
		if question.ExpiresAt == nil {
			expires := now.Add(time.Duration(questionTimeoutSeconds()) * time.Second)
			question.ExpiresAt = &expires
		}
		plan.Questions = append(plan.Questions, *question)
		task.Status = models.StatusAwaitingInput
		task.Error = ""
		task.FinishedAt = nil
		task.UpdatedAt = now
		plan.Status = models.StatusAwaitingInput
		plan.UpdatedAt = now
		event = models.PlanEvent{
			PlanID:    planID,
			TaskID:    taskID,
			EventType: "question_requested",
			Revision:  task.Revision,
			Payload: map[string]any{
				"question":    question,
				"question_id": question.ID,
				"header":      question.Header,
				"prompt":      question.Prompt,
			},
			Timestamp: now,
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.publishAndStore(planID, event)
	return nil
}

// ExpireQuestionsNow runs the timeout sweep for one plan outside the execution
// loop. API reads call it so a paused plan still honours its question timeout
// even when no scheduler goroutine is running (for example after a restart).
func (s *Scheduler) ExpireQuestionsNow(planID string) (bool, error) {
	return s.expireQuestions(planID)
}

// expireQuestions answers overdue questions from their default option, or
// blocks the task and plan when no default exists. It returns true when the
// plan moved to a terminal blocked state.
func (s *Scheduler) expireQuestions(planID string) (bool, error) {
	now := time.Now().UTC()
	expiredTerminal := false
	events := []models.PlanEvent{}
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if len(plan.Questions) == 0 {
			return nil
		}
		changed := false
		for i := range plan.Questions {
			question := &plan.Questions[i]
			if question.Status != models.QuestionPending {
				continue
			}
			if question.ExpiresAt == nil || now.Before(*question.ExpiresAt) {
				continue
			}
			changed = true
			task := getNodeByID(plan, question.TaskID)
			if question.DefaultOption != "" {
				question.Status = models.QuestionAnswered
				question.AnswerOption = question.DefaultOption
				question.Answer = optionLabel(question.Options, question.DefaultOption)
				question.AnsweredBy = "system:timeout"
				question.AnsweredAt = &now
				question.AutoAnswered = true
				if task != nil {
					if allDepsCompleted(plan, task) {
						task.Status = models.StatusReady
					} else {
						task.Status = models.StatusPending
					}
					task.Error = ""
					task.FinishedAt = nil
					task.UpdatedAt = now
					if task.Inputs == nil {
						task.Inputs = map[string]any{}
					}
					task.Inputs["question_answer"] = question.Answer
					task.Inputs["question_answer_option"] = question.AnswerOption
				}
				if plan.Status == models.StatusAwaitingInput {
					plan.Status = models.StatusPending
				}
				events = append(events, models.PlanEvent{
					PlanID:    planID,
					TaskID:    question.TaskID,
					EventType: "question_answered",
					Revision:  question.Revision,
					Payload: map[string]any{
						"question_id":   question.ID,
						"answer":        question.Answer,
						"answer_option": question.AnswerOption,
						"answered_by":   "system:timeout",
						"auto":          true,
					},
					Timestamp: now,
				})
				continue
			}
			// No default: block the task and fail the plan with an actionable
			// missing requirement so the user knows exactly what is needed.
			question.Status = models.QuestionExpired
			question.AnsweredAt = &now
			if task != nil {
				task.Status = models.StatusBlocked
				task.Error = "question expired without an answer"
				task.FinishedAt = &now
				task.UpdatedAt = now
			}
			plan.Status = models.StatusFailed
			plan.Outcome = models.OutcomeBlocked
			plan.OutcomeReason = "question expired without an answer"
			plan.Usage.FinishedAt = &now
			plan.MissingRequirements = appendMissingRequirement(plan.MissingRequirements, models.MissingRequirement{
				Kind:        "question_timeout",
				Name:        question.Header,
				Detail:      question.Prompt,
				Remediation: "回答问题后重试该任务，或为问题设置默认选项。",
			})
			expiredTerminal = true
			events = append(events, models.PlanEvent{
				PlanID:    planID,
				TaskID:    question.TaskID,
				EventType: "question_expired",
				Revision:  question.Revision,
				Payload: map[string]any{
					"question_id": question.ID,
					"prompt":      question.Prompt,
				},
				Timestamp: now,
			})
		}
		if changed {
			plan.UpdatedAt = now
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	for _, event := range events {
		s.publishAndStore(planID, event)
	}
	return expiredTerminal, nil
}

func optionLabel(options []models.QuestionOption, id string) string {
	for _, option := range options {
		if option.ID == id {
			return option.Label
		}
	}
	return id
}

func allDepsCompleted(plan *models.PlanGraph, task *models.TaskNode) bool {
	if plan == nil || task == nil {
		return false
	}
	for _, dependency := range task.Dependencies {
		found := false
		for _, node := range plan.Nodes {
			if node == nil || node.ID != dependency {
				continue
			}
			found = true
			if node.Status != models.StatusCompleted && node.Status != models.StatusSkipped {
				return false
			}
		}
		if !found {
			return false
		}
	}
	return true
}
