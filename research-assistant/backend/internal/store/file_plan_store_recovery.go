package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

func legacyGeneration(id string) string { return "legacy:" + id }

func validateEnvelope(meta planEnvelope) error {
	if meta.Format != 2 {
		return fmt.Errorf("unsupported plan store format: %d", meta.Format)
	}
	if strings.TrimSpace(meta.PlanID) == "" || strings.TrimSpace(meta.Generation) == "" || meta.EventSeqFloor < 0 {
		return fmt.Errorf("invalid plan envelope identity or sequence floor")
	}
	switch meta.State {
	case "active":
		if meta.Plan == nil || meta.Plan.ID != meta.PlanID {
			return fmt.Errorf("plan envelope id mismatch")
		}
	case "deleted":
		if meta.Plan != nil {
			return fmt.Errorf("deleted envelope contains a plan")
		}
	default:
		return fmt.Errorf("invalid plan envelope state: %s", meta.State)
	}
	return nil
}

type recoveryState struct {
	legacyPlans     map[string]*models.PlanGraph
	legacySnapshots map[string]*models.PlanGraph
	legacyEvents    map[string][]models.PlanEvent
	legacyDeleted   map[string]bool
	historicalMax   map[string]int64
	tagged          map[string]map[string][]models.PlanEvent
	v2              map[string]planEnvelope
	migrate         bool
	wholeSnapshot   bool
}

func newRecoveryState() *recoveryState {
	return &recoveryState{
		legacyPlans: map[string]*models.PlanGraph{}, legacySnapshots: map[string]*models.PlanGraph{},
		legacyEvents: map[string][]models.PlanEvent{}, legacyDeleted: map[string]bool{},
		historicalMax: map[string]int64{}, tagged: map[string]map[string][]models.PlanEvent{}, v2: map[string]planEnvelope{},
	}
}

func (s *FilePlanStore) load() error {
	r := newRecoveryState()
	if err := s.readSnapshots(r); err != nil {
		return err
	}
	raw, err := os.ReadFile(s.path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read plan log: %w", err)
	}
	normalized, err := r.readLog(raw)
	if err != nil {
		return err
	}
	if err := s.assembleRecovered(r); err != nil {
		return err
	}
	if r.migrate {
		if err := s.migrateRecovered(r, raw); err != nil {
			return err
		}
	} else if !bytes.Equal(raw, normalized) {
		// Repair the tail as one complete replacement, with all readers closed.
		if err := s.replaceBytesLocked(s.path, normalized, "repair_log", "", "", 0); err != nil {
			return err
		}
		s.logOffset = int64(len(normalized))
	} else {
		s.logOffset = int64(len(raw))
	}
	return s.initializeLog()
}

func (s *FilePlanStore) readSnapshots(r *recoveryState) error {
	entries, err := os.ReadDir(s.snapshotDir)
	if err != nil {
		return fmt.Errorf("read plan snapshots: %w", err)
	}
	identities := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.snapshotDir, entry.Name()))
		if err != nil {
			return fmt.Errorf("read plan snapshot %s: %w", entry.Name(), err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return fmt.Errorf("decode plan snapshot %s: %w", entry.Name(), err)
		}
		var id string
		if _, versioned := fields["plan_store_format"]; versioned {
			var meta planEnvelope
			if err := json.Unmarshal(raw, &meta); err != nil {
				return fmt.Errorf("decode plan envelope %s: %w", entry.Name(), err)
			}
			if err := validateEnvelope(meta); err != nil {
				return fmt.Errorf("plan envelope %s: %w", entry.Name(), err)
			}
			id = meta.PlanID
			r.v2[id] = meta
		} else {
			// A malformed envelope with a removed discriminator must not be
			// treated as an empty legacy plan and silently skipped.
			if _, envelopeLike := fields["generation"]; envelopeLike {
				return fmt.Errorf("snapshot %s is missing plan_store_format", entry.Name())
			}
			var plan models.PlanGraph
			if err := json.Unmarshal(raw, &plan); err != nil {
				return fmt.Errorf("decode legacy plan %s: %w", entry.Name(), err)
			}
			if strings.TrimSpace(plan.ID) == "" {
				return fmt.Errorf("snapshot %s has empty plan id", entry.Name())
			}
			id = plan.ID
			r.legacyPlans[id], r.legacySnapshots[id] = &plan, &plan
		}
		if entry.Name() != safePlanFileID(id)+".json" {
			return fmt.Errorf("snapshot filename does not match plan id: %s", entry.Name())
		}
		if previous, duplicate := identities[id]; duplicate {
			return fmt.Errorf("duplicate plan snapshots: %s and %s", previous, entry.Name())
		}
		identities[id] = entry.Name()
	}
	return nil
}

func (r *recoveryState) readLog(raw []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return raw, nil
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(trimmed, &object) == nil {
		_, hasPlans := object["plans"]
		_, hasEvents := object["events"]
		if hasPlans || hasEvents {
			var snapshot fileSnapshot
			if err := json.Unmarshal(trimmed, &snapshot); err != nil {
				return nil, fmt.Errorf("decode legacy snapshot: %w", err)
			}
			r.migrate, r.wholeSnapshot = true, true
			for id, plan := range snapshot.Plans {
				if plan == nil || plan.ID != id {
					return nil, fmt.Errorf("legacy snapshot plan id mismatch: %s", id)
				}
				r.legacyPlans[id] = plan
			}
			for id, events := range snapshot.Events {
				for _, event := range events {
					if event.PlanID == "" {
						event.PlanID = id
					}
					if event.PlanID != id {
						return nil, fmt.Errorf("legacy event plan id mismatch: %s", id)
					}
					if err := r.addLegacyEvent(event); err != nil {
						return nil, err
					}
				}
			}
			return raw, nil
		}
	}
	var normalized bytes.Buffer
	lines := bytes.Split(raw, []byte{'\n'})
	for index, line := range lines {
		isTail := index == len(lines)-1
		if len(bytes.TrimSpace(line)) == 0 {
			if !isTail {
				normalized.Write(line)
				normalized.WriteByte('\n')
			}
			continue
		}
		var record fileLogRecord
		if err := json.Unmarshal(line, &record); err != nil {
			if isTail {
				break
			}
			return nil, fmt.Errorf("decode plan log line %d: %w", index+1, err)
		}
		if err := r.applyRecord(record); err != nil {
			return nil, fmt.Errorf("plan log line %d: %w", index+1, err)
		}
		normalized.Write(line)
		normalized.WriteByte('\n')
	}
	return normalized.Bytes(), nil
}

func (r *recoveryState) addLegacyEvent(event models.PlanEvent) error {
	if event.PlanID == "" || event.Seq < 0 {
		return fmt.Errorf("invalid legacy event identity or sequence")
	}
	// Old versions persisted events before Seq existed, then appended positive
	// sequences starting at one. Keep those unsequenced events at zero: assigning
	// them new numbers would collide with or renumber existing SSE cursors.
	if event.Seq > r.historicalMax[event.PlanID] {
		r.historicalMax[event.PlanID] = event.Seq
	}
	r.legacyEvents[event.PlanID] = append(r.legacyEvents[event.PlanID], event)
	return nil
}

func (r *recoveryState) applyRecord(record fileLogRecord) error {
	if record.Format != 0 && record.Format != 2 {
		return fmt.Errorf("unsupported event log format: %d", record.Format)
	}
	if record.Format == 2 {
		if record.Kind != "event" || record.Generation == "" || record.PlanID == "" || record.Event == nil || record.Event.PlanID != record.PlanID || record.Event.Seq <= 0 {
			return fmt.Errorf("invalid versioned event record")
		}
		if r.tagged[record.PlanID] == nil {
			r.tagged[record.PlanID] = map[string][]models.PlanEvent{}
		}
		previous := r.tagged[record.PlanID][record.Generation]
		if len(previous) > 0 && previous[len(previous)-1].Seq >= record.Event.Seq {
			return fmt.Errorf("non-monotonic event sequence")
		}
		r.tagged[record.PlanID][record.Generation] = append(previous, *record.Event)
		if record.Event.Seq > r.historicalMax[record.PlanID] {
			r.historicalMax[record.PlanID] = record.Event.Seq
		}
		return nil
	}
	if record.Generation != "" {
		return fmt.Errorf("event generation without format")
	}
	switch record.Kind {
	case "plan":
		if record.Plan == nil || record.Plan.ID == "" {
			return fmt.Errorf("invalid legacy plan record")
		}
		r.migrate = true
		r.legacyPlans[record.Plan.ID] = record.Plan
		delete(r.legacyDeleted, record.Plan.ID)
	case "plan_saved", "plan_updated":
		if record.PlanID == "" {
			return fmt.Errorf("empty legacy lifecycle plan id")
		}
		if plan, ok := r.legacySnapshots[record.PlanID]; ok {
			r.legacyPlans[record.PlanID] = plan
		}
		delete(r.legacyDeleted, record.PlanID)
	case "plan_deleted":
		if record.PlanID == "" {
			return fmt.Errorf("empty legacy deletion plan id")
		}
		delete(r.legacyPlans, record.PlanID)
		delete(r.legacyEvents, record.PlanID)
		r.legacyDeleted[record.PlanID] = true
	case "event":
		if record.Event == nil {
			return fmt.Errorf("missing legacy event")
		}
		if record.PlanID != "" && record.PlanID != record.Event.PlanID {
			return fmt.Errorf("legacy event plan id mismatch")
		}
		return r.addLegacyEvent(*record.Event)
	default:
		return fmt.Errorf("unknown plan log kind: %s", record.Kind)
	}
	return nil
}

func (s *FilePlanStore) assembleRecovered(r *recoveryState) error {
	for id, plan := range r.legacyPlans {
		s.plans[id] = plan
		s.envelopes[id] = planEnvelope{PlanID: id, Generation: legacyGeneration(id), EventSeqFloor: r.historicalMax[id], State: "active", Plan: plan}
	}
	for id := range r.legacyDeleted {
		s.envelopes[id] = planEnvelope{PlanID: id, Generation: legacyGeneration(id), EventSeqFloor: r.historicalMax[id], State: "deleted"}
	}
	for id, meta := range r.v2 {
		s.envelopes[id] = meta
		if meta.State == "deleted" {
			delete(s.plans, id)
		} else {
			s.plans[id] = meta.Plan
		}
	}
	for id, plan := range s.plans {
		meta := s.envelopes[id]
		var events []models.PlanEvent
		if meta.Generation == legacyGeneration(id) {
			events = append(events, r.legacyEvents[id]...)
		}
		events = append(events, r.tagged[id][meta.Generation]...)
		var previous int64
		for _, event := range events {
			if event.Seq == 0 {
				continue
			} // Legacy-only events have no SSE cursor.
			if event.Seq <= previous {
				return fmt.Errorf("non-monotonic recovered events for %s", id)
			}
			previous = event.Seq
		}
		if _, err := clonePlanGraph(plan); err != nil {
			return fmt.Errorf("invalid recovered plan %s: %w", id, err)
		}
		s.events[id] = events
	}
	return nil
}

func (s *FilePlanStore) migrateRecovered(r *recoveryState, original []byte) error {
	// Each envelope is independently recoverable while the original log remains.
	ids := make([]string, 0, len(s.envelopes))
	for id := range s.envelopes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		meta := s.envelopes[id]
		if meta.Format == 2 {
			continue
		}
		meta.Format = 2
		if err := s.writeEnvelopeLocked(meta, "migrate_plan"); err != nil {
			return err
		}
		s.envelopes[id] = meta
	}
	var compact bytes.Buffer
	for _, id := range ids {
		meta := s.envelopes[id]
		for _, event := range s.events[id] {
			record := fileLogRecord{Format: 2, Kind: "event", PlanID: id, Generation: meta.Generation, Event: &event}
			if meta.Generation == legacyGeneration(id) {
				// Preserve the complete legacy stream's order, including zero-Seq
				// events mixed with positive cursors. The authoritative envelope
				// maps these untagged records to the stable legacy generation.
				record = fileLogRecord{Kind: "event", Event: &event}
			}
			raw, err := json.Marshal(record)
			if err != nil {
				return err
			}
			compact.Write(raw)
			compact.WriteByte('\n')
		}
	}
	// Keep a closed, durable original before activating the complete replacement.
	archive := fmt.Sprintf("%s.legacy-%s", s.path, time.Now().UTC().Format("20060102T150405.000000000"))
	if err := writeLegacyArchive(archive, original); err != nil {
		return err
	}
	if err := s.replaceBytesLocked(s.path, compact.Bytes(), "migrate_log", "", "", 0); err != nil {
		return err
	}
	s.logOffset = int64(compact.Len())
	if !r.wholeSnapshot {
		if err := gzipFile(archive); err != nil {
			log.Printf("plan store: archive compression after committed migration: %v", err)
		}
	}
	return nil
}

func writeLegacyArchive(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create legacy archive: %w", err)
	}
	if err := writeComplete(file, raw); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncPlanStoreDirectory(filepath.Dir(path))
}

func gzipFile(path string) error {
	source, err := os.Open(path)
	if err != nil {
		return err
	}
	target, err := os.OpenFile(path+".gz", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		_ = source.Close()
		return err
	}
	zip := gzip.NewWriter(target)
	_, copyErr := io.Copy(zip, source)
	zipErr := zip.Close()
	sourceErr := source.Close()
	if copyErr != nil || zipErr != nil || sourceErr != nil {
		_ = target.Close()
		return fmt.Errorf("compress legacy archive: %v %v %v", copyErr, zipErr, sourceErr)
	}
	if err := target.Sync(); err != nil {
		_ = target.Close()
		return err
	}
	if err := target.Close(); err != nil {
		return err
	}
	if err := syncPlanStoreDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	return os.Remove(path)
}
