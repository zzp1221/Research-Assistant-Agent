package store

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/models"
)

// fileSnapshot is the legacy JSON snapshot format. It is read once and migrated
// to the current snapshot + event log layout.
type fileSnapshot struct {
	Plans  map[string]*models.PlanGraph  `json:"plans"`
	Events map[string][]models.PlanEvent `json:"events"`
}

// fileLogRecord is one line in the compact append-only plan log.
type fileLogRecord struct {
	Kind   string            `json:"kind"`
	PlanID string            `json:"plan_id,omitempty"`
	Plan   *models.PlanGraph `json:"plan,omitempty"`
	Event  *models.PlanEvent `json:"event,omitempty"`
}

// FilePlanStore persists each PlanGraph as an atomically replaced snapshot
// file and keeps a small append-only JSONL log for plan ids and events.
type FilePlanStore struct {
	mu          sync.RWMutex
	path        string
	snapshotDir string
	plans       map[string]*models.PlanGraph
	events      map[string][]models.PlanEvent
}

func NewFilePlanStore(path string) (*FilePlanStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("plan store path is empty")
	}
	store := &FilePlanStore{
		path:        path,
		snapshotDir: path + ".d",
		plans:       map[string]*models.PlanGraph{},
		events:      map[string][]models.PlanEvent{},
	}
	if err := os.MkdirAll(store.snapshotDir, 0o700); err != nil {
		return nil, fmt.Errorf("create plan snapshot directory: %w", err)
	}
	if err := store.loadSnapshots(); err != nil {
		return nil, err
	}
	if err := store.loadExistingLog(); err != nil {
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
	cloned := clonePlanGraph(plan)
	s.plans[cloned.ID] = cloned
	if _, ok := s.events[cloned.ID]; !ok {
		s.events[cloned.ID] = []models.PlanEvent{}
	}
	if err := s.writeSnapshotLocked(cloned); err != nil {
		return err
	}
	return s.appendRecordLocked(fileLogRecord{Kind: "plan_saved", PlanID: cloned.ID})
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
	// Dirty check: the scheduler polls frequently, and rewriting an unchanged
	// multi-megabyte snapshot plus fsync was the main source of write amplification.
	previousRaw, previousErr := json.Marshal(plan)
	workingRaw, workingErr := json.Marshal(working)
	if previousErr == nil && workingErr == nil && bytes.Equal(previousRaw, workingRaw) {
		return nil
	}
	s.plans[planID] = working
	if err := s.writeSnapshotLocked(working); err != nil {
		return err
	}
	return s.appendRecordLocked(fileLogRecord{Kind: "plan_updated", PlanID: planID})
}

func (s *FilePlanStore) AppendEvent(planID string, event models.PlanEvent) error {
	_, err := s.AppendEventReturning(planID, event)
	return err
}

func (s *FilePlanStore) AppendEventReturning(planID string, event models.PlanEvent) (models.PlanEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[planID]; !ok {
		return models.PlanEvent{}, fmt.Errorf("plan not found: %s", planID)
	}
	cloned := clonePlanEvent(event)
	if cloned.Seq <= 0 {
		cloned.Seq = nextEventSeq(s.events[planID])
	}
	s.events[planID] = append(s.events[planID], cloned)
	if err := s.appendRecordLocked(fileLogRecord{Kind: "event", Event: &cloned}); err != nil {
		return models.PlanEvent{}, err
	}
	return clonePlanEvent(cloned), nil
}

func nextEventSeq(events []models.PlanEvent) int64 {
	var max int64
	for _, event := range events {
		if event.Seq > max {
			max = event.Seq
		}
	}
	return max + 1
}

func (s *FilePlanStore) ListEventsAfter(planID string, afterSeq int64) ([]models.PlanEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.plans[planID]; !ok {
		return nil, fmt.Errorf("plan not found: %s", planID)
	}
	out := make([]models.PlanEvent, 0)
	for _, event := range s.events[planID] {
		if event.Seq <= afterSeq {
			continue
		}
		out = append(out, clonePlanEvent(event))
	}
	return out, nil
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

func (s *FilePlanStore) DeletePlan(planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.plans[planID]; !ok {
		return fmt.Errorf("plan not found: %s", planID)
	}
	delete(s.plans, planID)
	delete(s.events, planID)
	_ = os.Remove(filepath.Join(s.snapshotDir, safePlanFileID(planID)+".json"))
	return s.appendRecordLocked(fileLogRecord{Kind: "plan_deleted", PlanID: planID})
}

func (s *FilePlanStore) loadSnapshots() error {
	entries, err := os.ReadDir(s.snapshotDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read plan snapshot directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.snapshotDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read plan snapshot %s: %w", entry.Name(), err)
		}
		var plan models.PlanGraph
		if err := json.Unmarshal(raw, &plan); err != nil {
			return fmt.Errorf("decode plan snapshot %s: %w", entry.Name(), err)
		}
		if plan.ID == "" {
			continue
		}
		s.plans[plan.ID] = &plan
		if _, ok := s.events[plan.ID]; !ok {
			s.events[plan.ID] = []models.PlanEvent{}
		}
	}
	return nil
}

func (s *FilePlanStore) loadExistingLog() error {
	info, err := os.Stat(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat plan log: %w", err)
	}
	if info.Size() == 0 {
		return nil
	}

	prefix := make([]byte, 512)
	file, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("open plan log: %w", err)
	}
	n, readErr := io.ReadFull(file, prefix)
	_ = file.Close()
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return fmt.Errorf("read plan log prefix: %w", readErr)
	}
	trimmed := bytes.TrimSpace(prefix[:n])
	if len(trimmed) > 0 && trimmed[0] == '{' && bytes.Contains(trimmed, []byte("plans")) {
		data, err := os.ReadFile(s.path)
		if err != nil {
			return fmt.Errorf("read legacy plan snapshot: %w", err)
		}
		if s.tryLoadLegacySnapshot(data) {
			return s.migrateLegacySnapshot()
		}
	}
	return s.loadAppendOnlyLog()
}

func (s *FilePlanStore) loadAppendOnlyLog() error {
	file, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("open plan log: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64*1024)
	var pending string
	lineNumber := 0
	legacyPlans := map[string]*models.PlanGraph{}

	processLine := func(line string, isLast bool) error {
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		var record fileLogRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			if isLast {
				return nil
			}
			return fmt.Errorf("decode plan log line %d: %w", lineNumber, err)
		}
		switch record.Kind {
		case "plan":
			if record.Plan != nil && record.Plan.ID != "" {
				legacyPlans[record.Plan.ID] = record.Plan
			}
		case "plan_saved", "plan_updated":
			if record.PlanID == "" {
				return nil
			}
			if _, exists := s.plans[record.PlanID]; exists {
				return nil
			}
			plan, err := s.readSnapshot(record.PlanID)
			if err == nil {
				s.plans[record.PlanID] = plan
				if _, ok := s.events[record.PlanID]; !ok {
					s.events[record.PlanID] = []models.PlanEvent{}
				}
			}
		case "event":
			if record.Event == nil || record.Event.PlanID == "" {
				return nil
			}
			s.events[record.Event.PlanID] = append(s.events[record.Event.PlanID], *record.Event)
		case "plan_deleted":
			if record.PlanID == "" {
				return nil
			}
			delete(s.plans, record.PlanID)
			delete(s.events, record.PlanID)
		}
		return nil
	}

	for {
		line, readErr := reader.ReadString(byte(10))
		if len(line) > 0 {
			if pending != "" {
				lineNumber++
				if err := processLine(pending, false); err != nil {
					return err
				}
			}
			pending = line
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read plan log: %w", readErr)
		}
	}
	if pending != "" {
		lineNumber++
		if err := processLine(pending, true); err != nil {
			return err
		}
	}

	if len(legacyPlans) > 0 {
		return s.migrateAppendOnlyLog(legacyPlans)
	}
	return nil
}

func (s *FilePlanStore) migrateAppendOnlyLog(legacyPlans map[string]*models.PlanGraph) error {
	for id, plan := range legacyPlans {
		s.plans[id] = plan
		if _, ok := s.events[id]; !ok {
			s.events[id] = []models.PlanEvent{}
		}
		if err := s.writeSnapshotLocked(plan); err != nil {
			return err
		}
	}

	legacyPath := fmt.Sprintf("%s.legacy-%s", s.path, time.Now().UTC().Format("20060102T150405"))
	if err := os.Rename(s.path, legacyPath); err != nil {
		return fmt.Errorf("rotate legacy plan log: %w", err)
	}

	for _, plan := range legacyPlans {
		if err := s.appendRecordLocked(fileLogRecord{Kind: "plan_saved", PlanID: plan.ID}); err != nil {
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
	return gzipFile(legacyPath)
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
		if err := s.writeSnapshotLocked(plan); err != nil {
			return err
		}
		if err := s.appendRecordLocked(fileLogRecord{Kind: "plan_saved", PlanID: plan.ID}); err != nil {
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

func (s *FilePlanStore) writeSnapshotLocked(plan *models.PlanGraph) error {
	if plan == nil || plan.ID == "" {
		return fmt.Errorf("plan is nil or has empty id")
	}
	if err := os.MkdirAll(s.snapshotDir, 0o700); err != nil {
		return fmt.Errorf("create plan snapshot directory: %w", err)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("encode plan snapshot: %w", err)
	}
	temp, err := os.CreateTemp(s.snapshotDir, ".plan-*.tmp")
	if err != nil {
		return fmt.Errorf("create plan snapshot temp file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return fmt.Errorf("chmod plan snapshot: %w", err)
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write plan snapshot: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync plan snapshot: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close plan snapshot: %w", err)
	}
	return os.Rename(tempName, filepath.Join(s.snapshotDir, safePlanFileID(plan.ID)+".json"))
}

func (s *FilePlanStore) readSnapshot(planID string) (*models.PlanGraph, error) {
	raw, err := os.ReadFile(filepath.Join(s.snapshotDir, safePlanFileID(planID)+".json"))
	if err != nil {
		return nil, err
	}
	var plan models.PlanGraph
	if err := json.Unmarshal(raw, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
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
	if _, err := file.Write(append(data, byte(10))); err != nil {
		return fmt.Errorf("append plan log: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync plan log: %w", err)
	}
	return nil
}

func safePlanFileID(id string) string {
	id = strings.ReplaceAll(id, "/", "_")
	id = strings.ReplaceAll(id, "..", "_")
	if id == "" {
		return "plan"
	}
	return id
}

func gzipFile(path string) error {
	source, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open legacy plan log: %w", err)
	}
	defer source.Close()
	destinationPath := path + ".gz"
	destination, err := os.Create(destinationPath)
	if err != nil {
		return fmt.Errorf("create compressed legacy plan log: %w", err)
	}
	writer := gzip.NewWriter(destination)
	if _, err := io.Copy(writer, source); err != nil {
		_ = writer.Close()
		_ = destination.Close()
		_ = os.Remove(destinationPath)
		return fmt.Errorf("compress legacy plan log: %w", err)
	}
	if err := writer.Close(); err != nil {
		_ = destination.Close()
		_ = os.Remove(destinationPath)
		return fmt.Errorf("close compressed legacy plan log: %w", err)
	}
	if err := destination.Close(); err != nil {
		_ = os.Remove(destinationPath)
		return fmt.Errorf("close compressed legacy plan log: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove uncompressed legacy plan log: %w", err)
	}
	return nil
}
