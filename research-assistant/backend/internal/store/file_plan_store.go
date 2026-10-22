package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"research-assistant-backend/internal/models"
	"sync"
	"time"
)

// fileSnapshot is the legacy JSON snapshot format. It is read once and migrated
// to the append-only log below.
type fileSnapshot struct {
	Plans  map[string]*models.PlanGraph  `json:"plans"`
	Events map[string][]models.PlanEvent `json:"events"`
}

// fileLogRecord is one line in the append-only plan log. Plan records carry a
// full snapshot (last write wins); event records are appended separately.
type fileLogRecord struct {
	Kind  string            `json:"kind"`
	Plan  *models.PlanGraph `json:"plan,omitempty"`
	Event *models.PlanEvent `json:"event,omitempty"`
}

// FilePlanStore persists scheduler state as an append-only JSONL event log.
// Each SavePlan/UpdatePlan appends a full plan snapshot; AppendEvent appends an
// event. The file is never rewritten, so a crash can at most leave a truncated
// trailing line, which is ignored on recovery.
type FilePlanStore struct {
	mu     sync.RWMutex
	path   string
	plans  map[string]*models.PlanGraph
	events map[string][]models.PlanEvent
}

func NewFilePlanStore(path string) (*FilePlanStore, error) {
	if path == "" {
		return nil, fmt.Errorf("plan store path is empty")
	}
	store := &FilePlanStore{
		path:   path,
		plans:  map[string]*models.PlanGraph{},
		events: map[string][]models.PlanEvent{},
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read plan store: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return store, nil
	}
	if store.tryLoadLegacySnapshot(data) {
		if err := store.migrateLegacySnapshot(); err != nil {
			return nil, err
		}
		return store, nil
	}
	if err := store.loadLog(data); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FilePlanStore) SavePlan(plan *models.PlanGraph) error {
	if plan == nil {
		return fmt.Errorf("plan is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[plan.ID] = clonePlanGraph(plan)
	if _, ok := s.events[plan.ID]; !ok {
		s.events[plan.ID] = []models.PlanEvent{}
	}
	return s.appendRecordLocked(fileLogRecord{Kind: "plan", Plan: clonePlanGraph(plan)})
}

func (s *FilePlanStore) GetPlan(planID string) (*models.PlanGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	plan, ok := s.plans[planID]
	if !ok {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}
	return clonePlanGraph(plan), nil
}

func (s *FilePlanStore) ListPlans() ([]*models.PlanGraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	plans := make([]*models.PlanGraph, 0, len(s.plans))
	for _, plan := range s.plans {
		plans = append(plans, clonePlanGraph(plan))
	}
	return plans, nil
}

func (s *FilePlanStore) UpdatePlan(planID string, update func(*models.PlanGraph) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[planID]
	if !ok {
		return fmt.Errorf("plan not found: %s", planID)
	}
	working := clonePlanGraph(plan)
	if err := update(working); err != nil {
		return err
	}
	s.plans[planID] = working
	return s.appendRecordLocked(fileLogRecord{Kind: "plan", Plan: clonePlanGraph(working)})
}

func (s *FilePlanStore) AppendEvent(planID string, event models.PlanEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[planID]; !ok {
		return fmt.Errorf("plan not found: %s", planID)
	}
	cloned := clonePlanEvent(event)
	s.events[planID] = append(s.events[planID], cloned)
	return s.appendRecordLocked(fileLogRecord{Kind: "event", Event: &cloned})
}

func (s *FilePlanStore) ListEvents(planID string) ([]models.PlanEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.plans[planID]; !ok {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}
	events := s.events[planID]
	out := make([]models.PlanEvent, 0, len(events))
	for _, event := range events {
		out = append(out, clonePlanEvent(event))
	}
	return out, nil
}

func (s *FilePlanStore) tryLoadLegacySnapshot(data []byte) bool {
	var snapshot fileSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return false
	}
	if snapshot.Plans == nil && snapshot.Events == nil {
		return false
	}
	if snapshot.Plans != nil {
		s.plans = snapshot.Plans
	}
	if snapshot.Events != nil {
		s.events = snapshot.Events
	}
	return true
}

func (s *FilePlanStore) migrateLegacySnapshot() error {
	legacyPath := fmt.Sprintf("%s.legacy-%s", s.path, time.Now().UTC().Format("20060102T150405"))
	if err := os.Rename(s.path, legacyPath); err != nil {
		return fmt.Errorf("rotate legacy plan snapshot: %w", err)
	}
	for _, plan := range s.plans {
		if err := s.appendRecordLocked(fileLogRecord{Kind: "plan", Plan: clonePlanGraph(plan)}); err != nil {
			return err
		}
	}
	for planID, events := range s.events {
		for _, event := range events {
			cloned := clonePlanEvent(event)
			if err := s.appendRecordLocked(fileLogRecord{Kind: "event", Event: &cloned}); err != nil {
				return fmt.Errorf("migrate events for plan %s: %w", planID, err)
			}
		}
	}
	return nil
}

func (s *FilePlanStore) loadLog(data []byte) error {
	lines := bytes.Split(data, []byte("\n"))
	for index, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var record fileLogRecord
		if err := json.Unmarshal(line, &record); err != nil {
			// A crash can leave a truncated final line. Ignore only that line.
			if isLastNonEmptyLine(lines, index) {
				continue
			}
			return fmt.Errorf("decode plan log line %d: %w", index+1, err)
		}
		switch record.Kind {
		case "plan":
			if record.Plan == nil || record.Plan.ID == "" {
				continue
			}
			s.plans[record.Plan.ID] = record.Plan
			if _, ok := s.events[record.Plan.ID]; !ok {
				s.events[record.Plan.ID] = []models.PlanEvent{}
			}
		case "event":
			if record.Event == nil || record.Event.PlanID == "" {
				continue
			}
			s.events[record.Event.PlanID] = append(s.events[record.Event.PlanID], *record.Event)
		}
	}
	return nil
}

func isLastNonEmptyLine(lines [][]byte, index int) bool {
	for next := index + 1; next < len(lines); next++ {
		if len(bytes.TrimSpace(lines[next])) > 0 {
			return false
		}
	}
	return true
}

func (s *FilePlanStore) appendRecordLocked(record fileLogRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create plan store directory: %w", err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode plan log record: %w", err)
	}
	file, err := os.OpenFile(s.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open plan log: %w", err)
	}
	defer file.Close()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("append plan log: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync plan log: %w", err)
	}
	return nil
}
