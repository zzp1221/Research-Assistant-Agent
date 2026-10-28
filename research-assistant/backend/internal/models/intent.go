package models

// IntentContext is the normalized planner input produced by intent recognition.
type IntentContext struct {
	RawIntent       string         `json:"raw_intent"`
	RewrittenIntent string         `json:"rewritten_intent,omitempty"`
	IntentType      string         `json:"intent_type"`
	Entities        map[string]any `json:"entities"`
	Constraints     map[string]any `json:"constraints"`
	Metadata        map[string]any `json:"metadata"`
	Confidence      float64        `json:"confidence,omitempty"`
	Reasoning       string         `json:"reasoning,omitempty"`
	Source          string         `json:"source,omitempty"` // "llm" or "rule_fallback"
	// Complexity routing fields (design: intent-complexity-routing.md). All are
	// optional so old intent services and old plan JSON keep working.
	Complexity           string   `json:"complexity,omitempty"` // L0 | L1 | L2 | L3 | L4
	Capabilities         []string `json:"capabilities,omitempty"`
	RouteHint            string   `json:"route_hint,omitempty"` // fast_path | standard_template | agent_planner
	ComplexityConfidence float64  `json:"complexity_confidence,omitempty"`
	ComplexityReason     string   `json:"complexity_reason,omitempty"`
}
