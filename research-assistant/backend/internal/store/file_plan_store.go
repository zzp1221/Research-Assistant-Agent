package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
	"research-assistant-backend/internal/models"
)

// fileSnapshot and the old record kinds remain readable for upgrades.
type fileSnapshot struct {
	Plans  map[string]*models.PlanGraph  `json:"plans"`
	Events map[string][]models.PlanEvent `json:"events"`
}

type fileLogRecord struct {
	Format     int               `json:"plan_store_format,omitempty"`
	Kind       string            `json:"kind"`
	PlanID     string            `json:"plan_id,omitempty"`
	Generation string            `json:"generation,omitempty"`
	Plan       *models.PlanGraph `json:"plan,omitempty"`
	Event      *models.PlanEvent `json:"event,omitempty"`
}

type planEnvelope struct {
	Format        int               `json:"plan_store_format"`
	PlanID        string            `json:"plan_id"`
	Generation    string            `json:"generation"`
	EventSeqFloor int64             `json:"event_seq_floor"`
	State         string            `json:"state"`
	Plan          *models.PlanGraph `json:"plan"`
}

// FilePlanStore has one durable authority per plan, including deleted plans.
// The JSONL file is authoritative only for events. One process may write a store.
type FilePlanStore struct {
	mu          sync.RWMutex
	path        string
	snapshotDir string
	plans       map[string]*models.PlanGraph
	events      map[string][]models.PlanEvent
	envelopes   map[string]planEnvelope
	logOffset   int64
	unhealthy   error
	io          fileStoreIO
}

func NewFilePlanStore(path string) (*FilePlanStore, error) {
	return newFilePlanStore(path, defaultFileStoreIO())
}

func newFilePlanStore(path string, ops fileStoreIO) (*FilePlanStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("plan store path is empty")
	}
	s := &FilePlanStore{
		path: path, snapshotDir: path + ".d", io: ops,
		plans: map[string]*models.PlanGraph{}, events: map[string][]models.PlanEvent{},
		envelopes: map[string]planEnvelope{},
	}
	if err := os.MkdirAll(s.snapshotDir, 0o700); err != nil {
		return nil, fmt.Errorf("create plan snapshot directory: %w", err)
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// Health permits callers to stop dispatch before attempting another transition.
func (s *FilePlanStore) Health() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.healthLocked()
}

func (s *FilePlanStore) healthLocked() error {
	if s.unhealthy != nil {
		return fmt.Errorf("%w: %v", ErrStoreRecoveryRequired, s.unhealthy)
	}
	return nil
}

func (s *FilePlanStore) sealLocked(op, planID, generation string, seq int64, cause error) error {
	err := &CommitError{Operation: op, PlanID: planID, Generation: generation, Seq: seq, Cause: cause}
	s.unhealthy = err
	return err
}

func (s *FilePlanStore) SavePlan(plan *models.PlanGraph) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.healthLocked(); err != nil {
		return err
	}
	if plan == nil || strings.TrimSpace(plan.ID) == "" {
		return fmt.Errorf("plan is nil or has empty id")
	}
	cloned, err := clonePlanGraph(plan)
	if err != nil {
		return err
	}
	return s.saveCandidateLocked(cloned, "save")
}

func (s *FilePlanStore) saveCandidateLocked(plan *models.PlanGraph, op string) error {
	meta, exists := s.envelopes[plan.ID]
	active := exists && meta.State == "active"
	if active && meta.Format == 2 {
		oldRaw, err := json.Marshal(s.plans[plan.ID])
		if err != nil {
			return fmt.Errorf("encode committed plan: %w", err)
		}
		newRaw, err := json.Marshal(plan)
		if err != nil {
			return fmt.Errorf("encode candidate plan: %w", err)
		}
		if bytes.Equal(oldRaw, newRaw) {
			// JSON-equivalent memory values can still differ in concrete Go
			// type or nil/empty flags. Publish the independently owned candidate
			// while preserving the no-disk-write optimization.
			s.plans[plan.ID] = plan
			meta.Plan = plan
			s.envelopes[plan.ID] = meta
			return nil
		}
	}
	if !active {
		meta = planEnvelope{PlanID: plan.ID, Generation: uuid.NewString(), EventSeqFloor: meta.EventSeqFloor}
	}
	meta.Format, meta.State, meta.Plan = 2, "active", plan
	if err := s.writeEnvelopeLocked(meta, op); err != nil {
		return err
	}
	s.envelopes[plan.ID], s.plans[plan.ID] = meta, plan
	if !active {
		s.events[plan.ID] = []models.PlanEvent{}
	}
	return nil
}

func (s *FilePlanStore) GetPlan(planID string) (*models.PlanGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.healthLocked(); err != nil {
		return nil, err
	}
	plan, ok := s.plans[planID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	return clonePlanGraph(plan)
}

func (s *FilePlanStore) ListPlans() ([]*models.PlanGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.healthLocked(); err != nil {
		return nil, err
	}
	out := make([]*models.PlanGraph, 0, len(s.plans))
	for _, plan := range s.plans {
		cloned, err := clonePlanGraph(plan)
		if err != nil {
			return nil, err
		}
		out = append(out, cloned)
	}
	return out, nil
}

func (s *FilePlanStore) ListPlanSummaries() ([]PlanSummaryRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.healthLocked(); err != nil {
		return nil, err
	}
	out := make([]PlanSummaryRecord, 0, len(s.plans))
	for _, plan := range s.plans {
		out = append(out, SummarizePlanRecord(plan))
	}
	return out, nil
}

func (s *FilePlanStore) UpdatePlan(planID string, update func(*models.PlanGraph) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.healthLocked(); err != nil {
		return err
	}
	plan, ok := s.plans[planID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	if update == nil {
		return fmt.Errorf("update callback is nil")
	}
	working, err := clonePlanGraph(plan)
	if err != nil {
		return err
	}
	if err := update(working); err != nil {
		return err
	}
	if working.ID != planID {
		return fmt.Errorf("update cannot change plan id")
	}
	// A callback can retain working or assign an external child into it.
	candidate, err := clonePlanGraph(working)
	if err != nil {
		return err
	}
	return s.saveCandidateLocked(candidate, "update")
}

func (s *FilePlanStore) AppendEvent(planID string, event models.PlanEvent) error {
	_, err := s.AppendEventReturning(planID, event)
	return err
}

func (s *FilePlanStore) AppendEventReturning(planID string, event models.PlanEvent) (models.PlanEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.healthLocked(); err != nil {
		return models.PlanEvent{}, err
	}
	if _, ok := s.plans[planID]; !ok {
		return models.PlanEvent{}, fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	if event.PlanID != planID {
		return models.PlanEvent{}, fmt.Errorf("event plan id does not match %s", planID)
	}
	if event.Seq < 0 {
		return models.PlanEvent{}, fmt.Errorf("event sequence must be nonnegative")
	}
	cloned, err := clonePlanEvent(event)
	if err != nil {
		return models.PlanEvent{}, err
	}
	meta := s.envelopes[planID]
	upper := maxEventSeq(s.events[planID], meta.EventSeqFloor)
	if cloned.Seq == 0 {
		if upper == math.MaxInt64 {
			return models.PlanEvent{}, fmt.Errorf("event sequence exhausted")
		}
		cloned.Seq = upper + 1
	} else if cloned.Seq <= upper {
		return models.PlanEvent{}, fmt.Errorf("event sequence must exceed %d", upper)
	}
	returned, err := clonePlanEvent(cloned)
	if err != nil {
		return models.PlanEvent{}, err
	}
	// Prepare the memory publication before the durable append.
	updated := make([]models.PlanEvent, len(s.events[planID])+1)
	copy(updated, s.events[planID])
	updated[len(updated)-1] = cloned
	record := fileLogRecord{Format: 2, Kind: "event", PlanID: planID, Generation: meta.Generation, Event: &cloned}
	if err := s.appendRecordLocked(record); err != nil {
		return models.PlanEvent{}, err
	}
	s.events[planID] = updated
	return returned, nil
}

func maxEventSeq(events []models.PlanEvent, floor int64) int64 {
	for _, event := range events {
		if event.Seq > floor {
			floor = event.Seq
		}
	}
	return floor
}

func nextEventSeq(events []models.PlanEvent) int64 { return maxEventSeq(events, 0) + 1 }

func (s *FilePlanStore) ListEventsAfter(planID string, afterSeq int64) ([]models.PlanEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if err := s.healthLocked(); err != nil {
		return nil, err
	}
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

func (s *FilePlanStore) ListEvents(planID string) ([]models.PlanEvent, error) {
	return s.ListEventsAfter(planID, math.MinInt64)
}

func (s *FilePlanStore) DeletePlan(planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.healthLocked(); err != nil {
		return err
	}
	if _, ok := s.plans[planID]; !ok {
		return fmt.Errorf("%w: %s", ErrPlanNotFound, planID)
	}
	meta := s.envelopes[planID]
	meta.Format, meta.State, meta.Plan = 2, "deleted", nil
	meta.EventSeqFloor = maxEventSeq(s.events[planID], meta.EventSeqFloor)
	if err := s.writeEnvelopeLocked(meta, "delete"); err != nil {
		return err
	}
	s.envelopes[planID] = meta
	delete(s.plans, planID)
	delete(s.events, planID)
	return nil
}

func safePlanFileID(id string) string {
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, "\\", "_")
	id = strings.ReplaceAll(id, "..", "_")
	if id == "" {
		return "plan"
	}
	return id
}

func (s *FilePlanStore) snapshotPath(id string) string {
	return filepath.Join(s.snapshotDir, safePlanFileID(id)+".json")
}
