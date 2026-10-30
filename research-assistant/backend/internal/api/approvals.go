package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/gin-gonic/gin"
)

// inboxItem is a unified pending approval or question, aggregated across every
// plan owned by the caller. It is a query view, not a separate write model.
type inboxItem struct {
	ID             string                  `json:"id"`
	Kind           string                  `json:"kind"` // approval | question
	PlanID         string                  `json:"plan_id"`
	PlanTitle      string                  `json:"plan_title"`
	TaskID         string                  `json:"task_id,omitempty"`
	NodeType       string                  `json:"node_type,omitempty"`
	Revision       int                     `json:"revision,omitempty"`
	Title          string                  `json:"title"`
	Prompt         string                  `json:"prompt,omitempty"`
	Risk           string                  `json:"risk,omitempty"`
	EstimatedUSD   float64                 `json:"estimated_usd,omitempty"`
	EstimatedBytes int64                   `json:"estimated_bytes,omitempty"`
	WaitingSeconds int                     `json:"waiting_seconds"`
	RequestedAt    time.Time               `json:"requested_at"`
	ExpiresAt      *time.Time              `json:"expires_at,omitempty"`
	ActionKind     string                  `json:"action_kind,omitempty"`
	Options        []models.QuestionOption `json:"options,omitempty"`
	MultiSelect    bool                    `json:"multi_select,omitempty"`
	AllowFreeText  bool                    `json:"allow_free_text,omitempty"`
	DefaultOption  string                  `json:"default_option,omitempty"`
	Details        map[string]any          `json:"details,omitempty"`
}

type inboxCursor struct {
	RequestedAt int64  `json:"t"`
	ID          string `json:"id"`
}

func encodeInboxCursor(item inboxItem) string {
	raw, _ := json.Marshal(inboxCursor{RequestedAt: item.RequestedAt.UnixNano(), ID: item.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeInboxCursor(raw string) (inboxCursor, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return inboxCursor{}, false
	}
	var cursor inboxCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return inboxCursor{}, false
	}
	return cursor, true
}

// listApprovals aggregates pending runtime actions and questions across the
// caller's plans. The cursor is keyset based on (requested_at, id) so new items
// never shift an open page.
func (r *planRuntime) listApprovals(c *gin.Context) {
	userID := resolveUserID(c)
	status := strings.ToLower(strings.TrimSpace(c.DefaultQuery("status", "pending")))
	kind := strings.ToLower(strings.TrimSpace(c.DefaultQuery("kind", "all")))
	if kind != "all" && kind != "approval" && kind != "question" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kind must be approval, question or all", "code": "invalid_kind"})
		return
	}
	planID := strings.TrimSpace(c.Query("plan_id"))
	limit := parsePositiveInt(c.Query("limit"), 50)
	if limit > 200 {
		limit = 200
	}
	plans, err := r.store.ListPlans()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "store_unavailable"})
		return
	}
	now := time.Now().UTC()
	items := []inboxItem{}
	for _, plan := range plans {
		if plan == nil || plan.OwnerID != userID {
			continue
		}
		if planID != "" && plan.ID != planID {
			continue
		}
		if hasPendingQuestions(plan) {
			_, _ = r.scheduler.ExpireQuestionsNow(plan.ID)
			if refreshed, getErr := r.store.GetPlan(plan.ID); getErr == nil && refreshed != nil {
				plan = refreshed
			}
		}
		title := planDisplayTitle(plan)
		if kind == "all" || kind == "approval" {
			for _, action := range plan.RuntimeActions {
				if status == "pending" && action.Status != models.RuntimeActionPending {
					continue
				}
				if status == "all" && action.Status != models.RuntimeActionPending && action.Status != models.RuntimeActionApproved && action.Status != models.RuntimeActionRejected && action.Status != models.RuntimeActionExpired {
					continue
				}
				if action.Status != models.RuntimeActionPending && status != "all" {
					continue
				}
				waiting := 0
				if !action.RequestedAt.IsZero() {
					waiting = int(now.Sub(action.RequestedAt).Seconds())
					if waiting < 0 {
						waiting = 0
					}
				}
				expires := action.RequestedAt.Add(time.Duration(approvalTimeoutSecondsForAPI()) * time.Second)
				items = append(items, inboxItem{
					ID:             action.ID,
					Kind:           "approval",
					PlanID:         plan.ID,
					PlanTitle:      title,
					TaskID:         action.TaskID,
					NodeType:       action.NodeType,
					Revision:       plan.CurrentRevision,
					Title:          action.Title,
					Risk:           action.Risk,
					EstimatedBytes: action.EstimatedBytes,
					WaitingSeconds: waiting,
					RequestedAt:    action.RequestedAt,
					ExpiresAt:      &expires,
					ActionKind:     string(action.Kind),
					Details:        action.Details,
				})
			}
		}
		if kind == "all" || kind == "question" {
			for _, question := range plan.Questions {
				if status == "pending" && question.Status != models.QuestionPending {
					continue
				}
				if status == "all" && question.Status != models.QuestionPending && question.Status != models.QuestionAnswered && question.Status != models.QuestionExpired {
					continue
				}
				if question.Status != models.QuestionPending && status != "all" {
					continue
				}
				requestedAt := question.RequestedAtOrCreated()
				waiting := 0
				if !requestedAt.IsZero() {
					waiting = int(now.Sub(requestedAt).Seconds())
					if waiting < 0 {
						waiting = 0
					}
				}
				items = append(items, inboxItem{
					ID:             question.ID,
					Kind:           "question",
					PlanID:         plan.ID,
					PlanTitle:      title,
					TaskID:         question.TaskID,
					NodeType:       question.NodeType,
					Revision:       question.Revision,
					Title:          question.Header,
					Prompt:         question.Prompt,
					WaitingSeconds: waiting,
					RequestedAt:    requestedAt,
					ExpiresAt:      question.ExpiresAt,
					Options:        question.Options,
					MultiSelect:    question.MultiSelect,
					AllowFreeText:  question.AllowFreeText,
					DefaultOption:  question.DefaultOption,
				})
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if !items[i].RequestedAt.Equal(items[j].RequestedAt) {
			return items[i].RequestedAt.After(items[j].RequestedAt)
		}
		return items[i].ID > items[j].ID
	})
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		cursor, ok := decodeInboxCursor(raw)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cursor", "code": "invalid_cursor"})
			return
		}
		filtered := items[:0]
		for _, item := range items {
			stamp := item.RequestedAt.UnixNano()
			if stamp < cursor.RequestedAt || (stamp == cursor.RequestedAt && item.ID < cursor.ID) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	total := len(items)
	pendingTotal := pendingInboxCount(items)
	nextCursor := ""
	if len(items) > limit {
		nextCursor = encodeInboxCursor(items[limit-1])
		items = items[:limit]
	}
	c.JSON(http.StatusOK, gin.H{
		"items":       items,
		"total":       total,
		"next_cursor": nextCursor,
		"pending":     pendingTotal,
	})
}

func pendingInboxCount(items []inboxItem) int {
	return len(items)
}

type bulkApprovalPayload struct {
	Kind     string   `json:"kind,omitempty"`
	IDs      []string `json:"ids"`
	Decision string   `json:"decision"`
	Note     string   `json:"note,omitempty"`
}

const maxBulkApprovals = 50

// bulkApprovals decides many pending runtime actions at once. High-risk
// actions are deliberately rejected from the bulk path: they must be reviewed
// one by one.
func (r *planRuntime) bulkApprovals(c *gin.Context) {
	userID := resolveUserID(c)
	var payload bulkApprovalPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid bulk payload", "code": "invalid_bulk"})
		return
	}
	decision := strings.ToLower(strings.TrimSpace(payload.Decision))
	if decision != "approve" && decision != "reject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "decision must be approve or reject", "code": "invalid_bulk"})
		return
	}
	if kind := strings.ToLower(strings.TrimSpace(payload.Kind)); kind != "" && kind != "approval" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "bulk decisions only support runtime approvals", "code": "invalid_bulk"})
		return
	}
	if len(payload.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids are required", "code": "invalid_bulk"})
		return
	}
	if len(payload.IDs) > maxBulkApprovals {
		c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("at most %d approvals can be decided at once", maxBulkApprovals), "code": "bulk_too_large"})
		return
	}
	// Resolve every id first so a single invalid id never leaves a partial bulk
	// decision behind.
	type resolved struct {
		planID   string
		actionID string
	}
	found := make([]resolved, 0, len(payload.IDs))
	plans, err := r.store.ListPlans()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "code": "store_unavailable"})
		return
	}
	index := map[string]*models.PlanGraph{}
	for _, plan := range plans {
		if plan != nil && plan.OwnerID == userID {
			index[plan.ID] = plan
		}
	}
	for _, id := range payload.IDs {
		matched := false
		for planID, plan := range index {
			action := models.FindRuntimeActionByID(plan, id)
			if action == nil {
				continue
			}
			matched = true
			if action.Status != models.RuntimeActionPending {
				c.JSON(http.StatusConflict, gin.H{"error": "runtime action is not pending: " + id, "code": "action_not_pending"})
				return
			}
			if strings.EqualFold(strings.TrimSpace(action.Risk), "high") {
				c.JSON(http.StatusBadRequest, gin.H{"error": "high-risk actions require individual review: " + id, "code": "high_risk_requires_individual"})
				return
			}
			found = append(found, resolved{planID: planID, actionID: id})
			break
		}
		if !matched {
			c.JSON(http.StatusNotFound, gin.H{"error": "runtime action not found: " + id, "code": "action_not_found"})
			return
		}
	}
	now := time.Now().UTC()
	decided := make([]string, 0, len(found))
	for _, item := range found {
		if err := r.applyBulkDecision(item.planID, item.actionID, decision, userID, payload.Note, now); err != nil {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "code": "bulk_partial_failed", "decided": decided})
			return
		}
		decided = append(decided, item.actionID)
	}
	c.JSON(http.StatusOK, gin.H{"decided": decided, "count": len(decided), "decision": decision})
}

// applyBulkDecision mirrors the single-action state machine without going
// through HTTP, so bulk and individual decisions stay semantically identical.
func (r *planRuntime) applyBulkDecision(planID, actionID, decision, actor, note string, now time.Time) error {
	var taskID string
	if err := r.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		action := models.FindRuntimeActionByID(plan, actionID)
		if action == nil {
			return errors.New("runtime action not found")
		}
		if action.Status != models.RuntimeActionPending {
			return errors.New("runtime action is not pending")
		}
		if isTerminalPlanStatus(plan.Status) {
			return errors.New("plan has already ended")
		}
		task := findPlanTask(plan, action.TaskID)
		if task == nil {
			return errors.New("runtime action task not found")
		}
		taskID = task.ID
		action.DecidedAt = &now
		action.DecidedBy = actor
		action.DecisionNote = note
		if decision == "approve" {
			action.Status = models.RuntimeActionApproved
			task.Status = models.StatusPending
			task.Error = ""
			task.FinishedAt = nil
			task.UpdatedAt = now
			if plan.Status == models.StatusAwaitingApproval {
				plan.Status = models.StatusInProgress
			}
			plan.Outcome = ""
			plan.OutcomeReason = ""
		} else {
			action.Status = models.RuntimeActionRejected
			task.Status = models.StatusBlocked
			task.Error = "user rejected runtime action: " + string(action.Kind)
			task.FinishedAt = &now
			task.UpdatedAt = now
			plan.Status = models.StatusFailed
			plan.Outcome = models.OutcomeBlocked
			plan.OutcomeReason = task.Error
			plan.Usage.FinishedAt = &now
		}
		plan.UpdatedAt = now
		return nil
	}); err != nil {
		return err
	}
	event := models.PlanEvent{
		PlanID:    planID,
		TaskID:    taskID,
		EventType: "approval_bulk_decided",
		Payload: map[string]any{
			"action_id": actionID,
			"decision":  decision,
			"actor":     actor,
		},
		Timestamp: now,
	}
	_ = r.store.AppendEvent(planID, event)
	r.events.Publish(planID, event)
	recordAuditEvent("approval_bulk_decided", actor, planID, map[string]any{"action_id": actionID, "decision": decision})
	return nil
}
