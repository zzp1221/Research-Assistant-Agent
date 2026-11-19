package store

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"research-assistant-backend/internal/models"
)

// MemoryPlanStore is the first-phase in-memory implementation of PlanStore.
type MemoryPlanStore struct {
	mu        sync.RWMutex
	plans     map[string]*models.PlanGraph
	events    map[string][]models.PlanEvent
	seqFloors map[string]int64
}

func NewMemoryPlanStore() *MemoryPlanStore {
	return &MemoryPlanStore{
		plans:     map[string]*models.PlanGraph{},
		events:    map[string][]models.PlanEvent{},
		seqFloors: map[string]int64{},
	}
}

func (s *MemoryPlanStore) SavePlan(plan *models.PlanGraph) error {
	if plan == nil || strings.TrimSpace(plan.ID) == "" {
		return fmt.Errorf("plan is nil or has empty id")
	}
	cloned, err := clonePlanGraph(plan)
	if err != nil {
		return err
	}
	if err := validateJSONValue(cloned); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[cloned.ID] = cloned
	if _, ok := s.events[cloned.ID]; !ok {
		s.events[cloned.ID] = []models.PlanEvent{}
	}
	return nil
}

func (s *MemoryPlanStore) GetPlan(planID string) (*models.PlanGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	plan, ok := s.plans[planID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	return clonePlanGraph(plan)
}

func (s *MemoryPlanStore) ListPlans() ([]*models.PlanGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	plans := make([]*models.PlanGraph, 0, len(s.plans))
	for _, plan := range s.plans {
		cloned, err := clonePlanGraph(plan)
		if err != nil {
			return nil, err
		}
		plans = append(plans, cloned)
	}
	return plans, nil
}

// ListPlanSummaries returns lightweight, independently owned projections.
func (s *MemoryPlanStore) ListPlanSummaries() ([]PlanSummaryRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]PlanSummaryRecord, 0, len(s.plans))
	for _, plan := range s.plans {
		out = append(out, SummarizePlanRecord(plan))
	}
	return out, nil
}

func (s *MemoryPlanStore) UpdatePlan(planID string, update func(*models.PlanGraph) error) error {
	if update == nil {
		return fmt.Errorf("plan update callback is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[planID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	working, err := clonePlanGraph(plan)
	if err != nil {
		return err
	}
	if err := update(working); err != nil {
		return err
	}
	if working.ID != planID {
		return fmt.Errorf("plan update cannot change id")
	}
	// The callback can retain working or insert caller-owned maps and slices.
	// A second copy is the object whose ownership transfers to this store.
	candidate, err := clonePlanGraph(working)
	if err != nil {
		return err
	}
	if err := validateJSONValue(candidate); err != nil {
		return err
	}
	s.plans[planID] = candidate
	return nil
}

func (s *MemoryPlanStore) AppendEvent(planID string, event models.PlanEvent) error {
	_, err := s.AppendEventReturning(planID, event)
	return err
}

func (s *MemoryPlanStore) AppendEventReturning(planID string, event models.PlanEvent) (models.PlanEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[planID]; !ok {
		return models.PlanEvent{}, fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	if event.PlanID != planID {
		return models.PlanEvent{}, fmt.Errorf("event plan id does not match %s", planID)
	}
	cloned, err := clonePlanEvent(event)
	if err != nil {
		return models.PlanEvent{}, err
	}
	maximum := s.seqFloors[planID]
	for _, stored := range s.events[planID] {
		if stored.Seq > maximum {
			maximum = stored.Seq
		}
	}
	if cloned.Seq < 0 || cloned.Seq > 0 && cloned.Seq <= maximum {
		return models.PlanEvent{}, fmt.Errorf("event seq must increase beyond %d", maximum)
	}
	if cloned.Seq == 0 {
		if maximum == math.MaxInt64 {
			return models.PlanEvent{}, fmt.Errorf("event seq exhausted")
		}
		cloned.Seq = maximum + 1
	}
	if err := validateJSONValue(cloned); err != nil {
		return models.PlanEvent{}, err
	}
	returned, err := clonePlanEvent(cloned)
	if err != nil {
		return models.PlanEvent{}, err
	}
	s.events[planID] = append(s.events[planID], cloned)
	s.seqFloors[planID] = cloned.Seq
	return returned, nil
}

func (s *MemoryPlanStore) ListEventsAfter(planID string, afterSeq int64) ([]models.PlanEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.plans[planID]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	out := make([]models.PlanEvent, 0)
	for _, event := range s.events[planID] {
		if event.Seq <= afterSeq {
			continue
		}
		cloned, err := clonePlanEvent(event)
		if err != nil {
			return nil, err
		}
		out = append(out, cloned)
	}
	return out, nil
}

func (s *MemoryPlanStore) ListEvents(planID string) ([]models.PlanEvent, error) {
	return s.ListEventsAfter(planID, math.MinInt64)
}

func (s *MemoryPlanStore) DeletePlan(planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[planID]; !ok {
		return fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	delete(s.plans, planID)
	delete(s.events, planID)
	return nil
}
