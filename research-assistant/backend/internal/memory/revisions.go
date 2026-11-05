package memory

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// recordIdentity distinguishes known scientific coordinates from an ambiguous
// manual note. A matching title alone is never permission to overwrite a fact.
func recordIdentity(rec Record) (string, bool) {
	if rec.Kind != KindFact || rec.Attributes["paper_id"] != nil {
		return "", false
	}
	coordinates := map[string]any{}
	for _, key := range []string{"fact_key", "experiment_id", "dataset_version", "dataset", "split", "method", "configuration_hash", "metric_name", "aggregation", "seed"} {
		if value, ok := rec.Attributes[key]; ok && value != nil && fmt.Sprint(value) != "" {
			coordinates[key] = value
		}
	}
	base := string(rec.Kind) + "|" + rec.Scope.OwnerID + "|" + rec.Scope.ProjectID + "|" + rec.Scope.SessionID + "|" + rec.Scope.PlanID + "|" + rec.Scope.AgentID
	if len(coordinates) > 0 {
		ambiguous := false
		if _, explicit := coordinates["fact_key"]; !explicit {
			for _, required := range []string{"experiment_id", "dataset_version", "split", "method", "configuration_hash", "metric_name", "aggregation"} {
				if coordinates[required] == nil {
					ambiguous = true
				}
			}
		}
		// Partial coordinates detect a possible conflict, not authorization to
		// overwrite. Complete coordinates or a caller-supplied fact key identify
		// an observation independently of its human-readable title.
		if ambiguous {
			coordinates["subject"] = NormalizeText(rec.Title)
		}
		raw, _ := json.Marshal(coordinates)
		return base + "|" + string(raw), ambiguous
	}
	if rec.Source.Type == "user" && strings.TrimSpace(rec.Title) != "" {
		return base + "|title:" + NormalizeText(rec.Title), true
	}
	return "", false
}

func revisionID(id string, version int) string {
	return fmt.Sprintf("%s@v%d", id, version)
}

func identifyRevision(rec *Record) {
	if rec.FactID == "" {
		rec.FactID = rec.ID
	}
	if rec.RevisionID == "" {
		rec.RevisionID = revisionID(rec.FactID, rec.Version)
	}
	if rec.Validity.ValidFrom == nil {
		from := rec.CreatedAt
		rec.Validity.ValidFrom = &from
	}
}

// closeRevision makes a separately addressable immutable historical copy. The
// stable current ID remains compatible with existing clients and source refs.
func closeRevision(previous Record, next *Record, now time.Time) Record {
	identifyRevision(&previous)
	previous.ID = previous.RevisionID
	previous.Status = StatusSuperseded
	previous.Validity.ValidTo = &now
	previous.Validity.SupersededBy = next.RevisionID
	next.Validity.ValidFrom = &now
	next.Validity.ValidTo = nil
	next.Validity.Supersedes = []string{previous.RevisionID}
	return previous
}

func cloneRecord(rec Record) Record {
	// Record contains mutable maps, slices and validity pointers. Callers must
	// never receive aliases to the store or to an earlier revision.
	raw, _ := json.Marshal(rec)
	var copied Record
	_ = json.Unmarshal(raw, &copied)
	return copied
}
