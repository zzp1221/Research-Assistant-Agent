package models

import (
	"encoding/json"
	"time"
)

// Artifact is a structured output produced by a task and consumed by downstream tasks.
// Type/Value/Location/Metadata are retained for compatibility with old plans and
// clients; Kind/SchemaVersion/Payload/BlobRef/EvidenceClass are the typed layer.
type Artifact struct {
	Key            string          `json:"key"`
	Type           string          `json:"type,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	SchemaVersion  string          `json:"schema_version,omitempty"`
	Value          string          `json:"value,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	BlobRef        string          `json:"blob_ref,omitempty"`
	Location       string          `json:"location,omitempty"`
	Metadata       map[string]any  `json:"metadata,omitempty"`
	ProducerTaskID string          `json:"producer_task_id,omitempty"`
	EvidenceClass  string          `json:"evidence_class,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// EffectiveValue returns the typed payload when present, otherwise the legacy string value.
func (a Artifact) EffectiveValue() string {
	if len(a.Payload) > 0 {
		return string(a.Payload)
	}
	return a.Value
}
