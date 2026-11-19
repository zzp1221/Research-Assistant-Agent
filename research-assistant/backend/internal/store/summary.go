package store

import (
	"time"

	"research-assistant-backend/internal/models"
)

// PlanSummaryRecord is a lightweight projection used by the plan library. It
// deliberately excludes nodes/artifacts/base64 so listing 1,000 plans never
// deep-copies a graph.
type PlanSummaryRecord struct {
	ID             string
	OwnerID        string
	SessionID      string
	ProjectID      string
	Title          string
	UserIntent     string
	IntentType     string
	Status         string
	Outcome        string
	TotalNodes     int
	CompletedNodes int
	FailedNodes    int
	CostUSD        float64
	DurationSec    float64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	FinishedAt     *time.Time
	Paused         bool
	RunTrigger     string
	Archived       bool
	Usage          models.RunUsage
	// Review mirrors PlanGraph.Review so the plan library can badge plans that
	// are waiting for a human review without loading the whole graph.
	Review models.ReviewState
}

// PlanSummarySource is implemented by stores that can serve the plan library
// without materializing full PlanGraph copies.
type PlanSummarySource interface {
	ListPlanSummaries() ([]PlanSummaryRecord, error)
}

// SummarizePlanRecord builds the projection for one plan. It is shared by the
// memory and file stores.
func SummarizePlanRecord(plan *models.PlanGraph) PlanSummaryRecord {
	if plan == nil {
		return PlanSummaryRecord{}
	}
	duration := 0.0
	if plan.Usage.StartedAt != nil && plan.Usage.FinishedAt != nil {
		duration = plan.Usage.FinishedAt.Sub(*plan.Usage.StartedAt).Seconds()
		if duration < 0 {
			duration = 0
		}
	}
	return PlanSummaryRecord{
		ID:             plan.ID,
		OwnerID:        plan.OwnerID,
		SessionID:      plan.SessionID,
		ProjectID:      plan.ProjectID,
		Title:          plan.Title,
		UserIntent:     plan.UserIntent,
		IntentType:     plan.IntentType,
		Status:         string(plan.Status),
		Outcome:        string(plan.Outcome),
		TotalNodes:     len(plan.Nodes),
		CompletedNodes: plan.Meta.CompletedNodes,
		FailedNodes:    plan.Meta.FailedNodes,
		CostUSD:        plan.Usage.LLMCostUSD,
		DurationSec:    duration,
		CreatedAt:      plan.CreatedAt,
		UpdatedAt:      plan.UpdatedAt,
		FinishedAt:     copySummaryTime(plan.Usage.FinishedAt),
		Paused:         plan.Paused,
		RunTrigger:     plan.RunTrigger,
		Archived:       plan.Archived,
		Usage:          copySummaryUsage(plan.Usage),
		Review:         copySummaryReview(plan.Review),
	}
}

func copySummaryTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func copySummaryUsage(value models.RunUsage) models.RunUsage {
	value.StartedAt = copySummaryTime(value.StartedAt)
	value.FinishedAt = copySummaryTime(value.FinishedAt)
	value.ApprovalWaitingSince = copySummaryTime(value.ApprovalWaitingSince)
	if value.BudgetWarnings != nil {
		value.BudgetWarnings = append([]string{}, value.BudgetWarnings...)
	}
	return value
}

func copySummaryReview(value models.ReviewState) models.ReviewState {
	value.ReviewedAt = copySummaryTime(value.ReviewedAt)
	return value
}
