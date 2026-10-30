package models

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// PlanRevision is an immutable snapshot of an editable plan graph. Editing a
// plan never mutates a revision: it creates a new revision whose parent points
// at the version it was derived from.
type PlanRevision struct {
	ID             string     `json:"id"`
	PlanID         string     `json:"plan_id"`
	Revision       int        `json:"revision"`
	ParentRevision int        `json:"parent_revision"`
	Graph          *PlanGraph `json:"graph"`
	// Status is one of draft | approved | executing | completed | superseded.
	Status      string     `json:"status"`
	CreatedBy   string     `json:"created_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	Reason      string     `json:"reason,omitempty"`
	DiffSummary []string   `json:"diff_summary,omitempty"`
	Hash        string     `json:"hash,omitempty"`
	ApprovedBy  string     `json:"approved_by,omitempty"`
	ApprovedAt  *time.Time `json:"approved_at,omitempty"`
}

// Revision status values.
const (
	RevisionDraft      = "draft"
	RevisionApproved   = "approved"
	RevisionExecuting  = "executing"
	RevisionCompleted  = "completed"
	RevisionSuperseded = "superseded"
	RevisionRejected   = "rejected"
)

// Review status values.
const (
	ReviewNotRequired = "not_required"
	ReviewPending     = "pending"
	ReviewApproved    = "approved"
	ReviewRejected    = "rejected"
)

// ReviewState is the execution-before-review gate persisted with the plan.
type ReviewState struct {
	Required     bool       `json:"required"`
	Status       string     `json:"status"`
	Reason       string     `json:"reason,omitempty"`
	Risk         string     `json:"risk,omitempty"` // low | medium | high
	EstimatedUSD float64    `json:"estimated_usd,omitempty"`
	ReviewedBy   string     `json:"reviewed_by,omitempty"`
	ReviewedAt   *time.Time `json:"reviewed_at,omitempty"`
}

// QuestionOption is one selectable answer of a structured question.
type QuestionOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// PlanQuestion is a decision request from an agent. Unlike a runtime action it
// has no side effect: it asks for information and can carry free text, a
// default option and a timeout.
type PlanQuestion struct {
	ID            string           `json:"id"`
	PlanID        string           `json:"plan_id"`
	Revision      int              `json:"revision,omitempty"`
	TaskID        string           `json:"task_id,omitempty"`
	NodeType      string           `json:"node_type,omitempty"`
	Header        string           `json:"header,omitempty"`
	Prompt        string           `json:"prompt"`
	Options       []QuestionOption `json:"options,omitempty"`
	MultiSelect   bool             `json:"multi_select,omitempty"`
	AllowFreeText bool             `json:"allow_free_text,omitempty"`
	DefaultOption string           `json:"default_option,omitempty"`
	Status        string           `json:"status"`
	RequestedAt   time.Time        `json:"requested_at"`
	Answer        string           `json:"answer,omitempty"`
	AnswerOption  string           `json:"answer_option,omitempty"`
	AnsweredBy    string           `json:"answered_by,omitempty"`
	AnsweredAt    *time.Time       `json:"answered_at,omitempty"`
	ExpiresAt     *time.Time       `json:"expires_at,omitempty"`
	AutoAnswered  bool             `json:"auto_answered,omitempty"`
}

// Question status values.
const (
	QuestionPending  = "pending"
	QuestionAnswered = "answered"
	QuestionExpired  = "expired"
	QuestionCanceled = "canceled"
)

// RevisionGraphHash returns a stable sha256 of the revision graph. The graph is
// serialized with encoding/json so map ordering is deterministic.
func RevisionGraphHash(graph *PlanGraph) string {
	if graph == nil {
		return ""
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// RequestedAtOrCreated returns the request timestamp, falling back to zero for
// questions persisted before the field existed.
func (q PlanQuestion) RequestedAtOrCreated() time.Time {
	return q.RequestedAt
}

// FindQuestion returns the question with the given id, or nil.
func FindQuestion(plan *PlanGraph, questionID string) *PlanQuestion {
	if plan == nil {
		return nil
	}
	for i := range plan.Questions {
		if plan.Questions[i].ID == questionID {
			return &plan.Questions[i]
		}
	}
	return nil
}

// FindRuntimeActionByID returns the runtime action with the given id, or nil.
func FindRuntimeActionByID(plan *PlanGraph, actionID string) *RuntimeActionRequest {
	if plan == nil {
		return nil
	}
	for i := range plan.RuntimeActions {
		if plan.RuntimeActions[i].ID == actionID {
			return &plan.RuntimeActions[i]
		}
	}
	return nil
}
