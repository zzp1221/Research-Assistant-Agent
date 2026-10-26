package store

import "research-assistant-backend/internal/models"

// PlanStore persists plan graphs and their event history.
type PlanStore interface {
	SavePlan(plan *models.PlanGraph) error
	GetPlan(planID string) (*models.PlanGraph, error)
	ListPlans() ([]*models.PlanGraph, error)
	UpdatePlan(planID string, update func(*models.PlanGraph) error) error
	AppendEvent(planID string, event models.PlanEvent) error
	ListEvents(planID string) ([]models.PlanEvent, error)
	// ListEventsAfter returns events with Seq greater than afterSeq, enabling
	// incremental SSE replay.
	ListEventsAfter(planID string, afterSeq int64) ([]models.PlanEvent, error)
	// DeletePlan removes a plan and its event history.
	DeletePlan(planID string) error
}
