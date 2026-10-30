package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	defaultQuestionTimeoutSeconds = 900
	maxQuestionTimeoutSeconds     = 86400
	maxQuestionsPerPlan           = 50
)

type createQuestionPayload struct {
	TaskID        string                  `json:"task_id,omitempty"`
	Header        string                  `json:"header,omitempty"`
	Prompt        string                  `json:"prompt"`
	Options       []models.QuestionOption `json:"options,omitempty"`
	MultiSelect   bool                    `json:"multi_select,omitempty"`
	AllowFreeText bool                    `json:"allow_free_text,omitempty"`
	DefaultOption string                  `json:"default_option,omitempty"`
	TimeoutSec    int                     `json:"timeout_seconds,omitempty"`
}

type answerQuestionPayload struct {
	Answer       string `json:"answer,omitempty"`
	AnswerOption string `json:"answer_option,omitempty"`
}

// listQuestions returns the structured questions attached to one plan.
func (r *planRuntime) listQuestions(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if hasPendingQuestions(plan) {
		if _, err := r.scheduler.ExpireQuestionsNow(plan.ID); err == nil {
			if refreshed, getErr := r.store.GetPlan(plan.ID); getErr == nil && refreshed != nil {
				plan = refreshed
			}
		}
	}
	status := strings.TrimSpace(c.Query("status"))
	items := make([]models.PlanQuestion, 0, len(plan.Questions))
	for _, question := range plan.Questions {
		if status != "" && question.Status != status {
			continue
		}
		items = append(items, question)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].AnsweredAt != nil && items[j].AnsweredAt != nil {
			return items[i].AnsweredAt.Before(*items[j].AnsweredAt)
		}
		return items[i].Prompt < items[j].Prompt
	})
	c.JSON(http.StatusOK, gin.H{"questions": items})
}

// createQuestion is the agent-facing tool endpoint. A task that needs a human
// decision calls it (or returns a Question in its execution result) and the
// plan pauses until the question is answered or expires.
func (r *planRuntime) createQuestion(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	var payload createQuestionPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid question payload", "code": "invalid_question"})
		return
	}
	question, err := buildQuestion(plan, payload, resolveUserID(c))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_question"})
		return
	}
	if err := r.storeQuestion(plan.ID, question, true); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "question_conflict"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"question": question, "plan_id": plan.ID})
}

func buildQuestion(plan *models.PlanGraph, payload createQuestionPayload, actor string) (*models.PlanQuestion, error) {
	prompt := strings.TrimSpace(payload.Prompt)
	if prompt == "" {
		return nil, errors.New("prompt is required")
	}
	if len([]rune(prompt)) > 4000 {
		return nil, errors.New("prompt is longer than 4000 characters")
	}
	timeout := payload.TimeoutSec
	if timeout <= 0 {
		timeout = defaultQuestionTimeoutSeconds
	}
	if timeout > maxQuestionTimeoutSeconds {
		timeout = maxQuestionTimeoutSeconds
	}
	now := time.Now().UTC()
	expires := now.Add(time.Duration(timeout) * time.Second)
	question := &models.PlanQuestion{
		ID:            "q-" + uuid.NewString(),
		PlanID:        plan.ID,
		Revision:      plan.CurrentRevision,
		TaskID:        strings.TrimSpace(payload.TaskID),
		Header:        strings.TrimSpace(payload.Header),
		Prompt:        prompt,
		Options:       payload.Options,
		MultiSelect:   payload.MultiSelect,
		AllowFreeText: payload.AllowFreeText,
		DefaultOption: strings.TrimSpace(payload.DefaultOption),
		Status:        models.QuestionPending,
		RequestedAt:   now,
		ExpiresAt:     &expires,
	}
	if question.TaskID != "" {
		if task := findPlanTask(plan, question.TaskID); task != nil {
			question.NodeType = task.Type
			question.Revision = task.Revision
		}
	}
	// A default option must reference a real option, otherwise timeout
	// auto-answer would record an answer that was never offered.
	if question.DefaultOption != "" && len(question.Options) > 0 {
		found := false
		for _, option := range question.Options {
			if option.ID == question.DefaultOption {
				found = true
				break
			}
		}
		if !found {
			return nil, errors.New("default_option must match one of the options")
		}
	}
	_ = actor
	return question, nil
}

// storeQuestion appends the question to the plan and pauses the task. It is
// used both by the HTTP tool endpoint and by the scheduler when an execution
// result carries a Question.
func (r *planRuntime) storeQuestion(planID string, question *models.PlanQuestion, fromAPI bool) error {
	now := time.Now().UTC()
	return r.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
		pending := 0
		for _, existing := range current.Questions {
			if existing.Status == models.QuestionPending {
				pending++
			}
		}
		if pending >= maxQuestionsPerPlan {
			return errors.New("too many pending questions for this plan")
		}
		current.Questions = append(current.Questions, *question)
		if task := findPlanTask(current, question.TaskID); task != nil {
			task.Status = models.StatusAwaitingInput
			task.Error = ""
			task.UpdatedAt = now
		}
		if fromAPI {
			current.Status = models.StatusAwaitingInput
		}
		current.UpdatedAt = now
		return nil
	})
}

// answerQuestion resolves a pending question and unblocks the waiting task.
func (r *planRuntime) answerQuestion(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	var payload answerQuestionPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid answer payload", "code": "invalid_answer"})
		return
	}
	answer := strings.TrimSpace(payload.Answer)
	answerOption := strings.TrimSpace(payload.AnswerOption)
	if answer == "" && answerOption == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "answer or answer_option is required", "code": "invalid_answer"})
		return
	}
	questionID := c.Param("questionId")
	actor := resolveUserID(c)
	now := time.Now().UTC()
	var answered models.PlanQuestion
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		question := models.FindQuestion(current, questionID)
		if question == nil {
			return errors.New("question not found")
		}
		if question.Status != models.QuestionPending {
			return errors.New("question is not pending (status=" + question.Status + ")")
		}
		if answerOption != "" && len(question.Options) > 0 {
			found := false
			for _, option := range question.Options {
				if option.ID == answerOption {
					found = true
					break
				}
			}
			if !found {
				return errors.New("answer_option does not match any offered option")
			}
		}
		if answerOption != "" {
			for _, option := range question.Options {
				if option.ID == answerOption && answer == "" {
					answer = option.Label
				}
			}
		}
		question.Status = models.QuestionAnswered
		question.Answer = answer
		question.AnswerOption = answerOption
		question.AnsweredBy = actor
		question.AnsweredAt = &now
		answered = *question
		// Resume the waiting task. It becomes ready when its dependencies are
		// already satisfied; otherwise pending so the scheduler promotes it.
		if task := findPlanTask(current, question.TaskID); task != nil {
			if allDependenciesCompletedForTask(current, task) {
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
			task.Inputs["question_answer"] = answer
			task.Inputs["question_answer_option"] = answerOption
		}
		if current.Status == models.StatusAwaitingInput {
			current.Status = models.StatusPending
		}
		current.UpdatedAt = now
		return nil
	}); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "question_not_answerable"})
		return
	}
	event := models.PlanEvent{
		PlanID:    plan.ID,
		TaskID:    answered.TaskID,
		EventType: "question_answered",
		Revision:  answered.Revision,
		Payload: map[string]any{
			"question_id":   answered.ID,
			"answer":        answer,
			"answer_option": answerOption,
			"answered_by":   actor,
			"auto":          false,
		},
		Timestamp: now,
	}
	_ = r.store.AppendEvent(plan.ID, event)
	r.events.Publish(plan.ID, event)
	recordAuditEvent("question_answered", actor, plan.ID, map[string]any{"question_id": answered.ID, "answer_option": answerOption})
	c.JSON(http.StatusOK, gin.H{"question": answered, "plan_id": plan.ID})
}

func allDependenciesCompletedForTask(plan *models.PlanGraph, task *models.TaskNode) bool {
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

// parsePositiveInt parses a query parameter with a fallback.
func parsePositiveInt(raw string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
func hasPendingQuestions(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	for _, question := range plan.Questions {
		if question.Status == models.QuestionPending {
			return true
		}
	}
	return false
}
