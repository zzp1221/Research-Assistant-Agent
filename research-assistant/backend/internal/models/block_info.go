package models

import "time"

// Block codes describe why a node was blocked by a hard upstream dependency.
// They are stable, machine-readable values: the UI, notifications and export
// all read the same code instead of parsing a localized message.
const (
	BlockUpstreamFailed   = "upstream_failed"
	BlockUpstreamBlocked  = "upstream_blocked"
	BlockUpstreamCanceled = "upstream_canceled"
	// BlockUnsatisfiedDependencies marks a node whose hard dependencies can
	// never be satisfied (a real deadlock), not a propagated upstream stop.
	BlockUnsatisfiedDependencies = "unsatisfied_dependencies"
	// BlockMissingArtifacts marks a node whose required artifacts are absent.
	BlockMissingArtifacts = "missing_artifacts"
)

// BlockCause is one effective blocking source of a blocked task. A node keeps
// every cause that still applies, not just the first producer the traversal
// happened to meet, so retrying one root cannot silently release the node.
type BlockCause struct {
	// Code mirrors BlockInfo.Code for the cause's own root status.
	Code string `json:"code"`
	// RootTaskID is the node that actually failed/was blocked/was canceled.
	RootTaskID string `json:"root_task_id"`
	// ViaTaskID is the direct predecessor that propagated the block here.
	ViaTaskID string `json:"via_task_id,omitempty"`
	// Revision and ExecutionEpoch bound the cause to the attempt that produced
	// it, so a retried root cannot keep an old failure blocking the graph.
	Revision       int `json:"revision,omitempty"`
	ExecutionEpoch int `json:"execution_epoch,omitempty"`
	// Status is the real status of the root when the cause was recorded.
	Status TaskStatus `json:"status"`
	// Message is a short human-readable explanation of the root cause.
	Message string `json:"message,omitempty"`
}

// BlockInfo is the structured reason a task is blocked. It is persisted with
// the status transition and is the source of the emitted events.
type BlockInfo struct {
	Code       string       `json:"code"`
	Message    string       `json:"message,omitempty"`
	Causes     []BlockCause `json:"causes,omitempty"`
	OccurredAt time.Time    `json:"occurred_at"`
}

// Clone returns a deep copy so plan copies never share cause slices.
func (b *BlockInfo) Clone() *BlockInfo {
	if b == nil {
		return nil
	}
	cloned := *b
	if b.Causes != nil {
		cloned.Causes = append([]BlockCause(nil), b.Causes...)
	}
	return &cloned
}

// RootTaskIDs lists the distinct roots of the recorded causes.
func (b *BlockInfo) RootTaskIDs() []string {
	if b == nil {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(b.Causes))
	for _, cause := range b.Causes {
		id := cause.RootTaskID
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// removeCausesByRoot drops every cause produced by one root task. It reports
// whether anything changed and whether any cause remains.
func (b *BlockInfo) removeCausesByRoot(rootTaskID string) (changed bool, remaining bool) {
	if b == nil {
		return false, false
	}
	kept := make([]BlockCause, 0, len(b.Causes))
	for _, cause := range b.Causes {
		if cause.RootTaskID == rootTaskID {
			changed = true
			continue
		}
		kept = append(kept, cause)
	}
	b.Causes = kept
	return changed, len(b.Causes) > 0
}
