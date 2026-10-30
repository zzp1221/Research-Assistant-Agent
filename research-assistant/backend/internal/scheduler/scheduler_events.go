package scheduler

import (
	"encoding/base64"
	"research-assistant-backend/internal/models"
	"strings"
	"time"
)

func (s *Scheduler) publishAndStore(planID string, event models.PlanEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	// The persisted event only carries a blob reference. For live SSE consumers
	// we hydrate the image inline so the existing UI keeps working, while the
	// event log on disk stays small.
	published := event
	if s.blobs != nil && event.Payload != nil {
		if ref, ok := event.Payload["image_blob_ref"].(string); ok && ref != "" {
			if existing, _ := event.Payload["image_base64"].(string); strings.TrimSpace(existing) == "" {
				if data, err := s.blobs.Get(ref); err == nil {
					hydrated := make(map[string]any, len(event.Payload)+1)
					for key, value := range event.Payload {
						hydrated[key] = value
					}
					hydrated["image_base64"] = base64.StdEncoding.EncodeToString(data)
					published.Payload = hydrated
				}
			}
		}
	}
	if event.TraceID == "" || event.Revision == 0 {
		if plan, err := s.store.GetPlan(planID); err == nil && plan != nil {
			if event.TraceID == "" {
				event.TraceID = plan.TraceID
			}
			// Stamp task/artifact events with the revision the plan is bound to.
			// Plan-level events created by the API already carry an explicit
			// revision; zero here means "inherit the running revision".
			if event.Revision == 0 {
				event.Revision = plan.CurrentRevision
			}
		}
	}
	if event.SpanID == "" && event.TaskID != "" {
		event.SpanID = "task_" + event.TaskID
	}
	stored, storeErr := s.store.AppendEventReturning(planID, event)
	if storeErr == nil {
		// Publish the stored copy so live SSE gets the assigned Seq (and thus an
		// id: line). Without this, reconnects replay events already applied by the
		// client because lastSeq never advances on live events.
		published.Seq = stored.Seq
		published.TraceID = stored.TraceID
		published.SpanID = stored.SpanID
	}
	s.publisher.Publish(planID, published)
	// Every persisted event is evidence of forward progress for the stall
	// watchdog, including task logs from long-running commands.
	s.touchActivity(planID)
}

func allSucceeded(plan *models.PlanGraph) bool {
	if plan == nil || len(plan.Nodes) == 0 {
		return false
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case models.StatusCompleted, models.StatusSkipped:
		default:
			return false
		}
	}
	return true
}

func allTerminal(plan *models.PlanGraph) bool {
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case models.StatusPending, models.StatusReady, models.StatusInProgress, models.StatusAwaitingApproval, models.StatusAwaitingInput:
			return false
		}
	}
	return true
}

// hasBlockedOnly reports whether the plan failed solely because tasks were
// blocked by actionable missing requirements (not by real task failures).
func hasBlockedOnly(plan *models.PlanGraph) bool {
	blocked := false
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case models.StatusFailed, models.StatusCanceled:
			return false
		case models.StatusBlocked:
			blocked = true
		}
	}
	return blocked
}

func appendMissingRequirement(list []models.MissingRequirement, item models.MissingRequirement) []models.MissingRequirement {
	for _, existing := range list {
		if existing.Kind == item.Kind && existing.Name == item.Name && existing.Detail == item.Detail {
			return list
		}
	}
	return append(list, item)
}

func hasFailure(plan *models.PlanGraph) bool {
	for _, node := range plan.Nodes {
		if node != nil && (node.Status == models.StatusFailed || node.Status == models.StatusBlocked || node.Status == models.StatusCanceled) {
			return true
		}
	}
	return false
}

func noRunning(plan *models.PlanGraph) bool {
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusInProgress {
			return false
		}
	}
	return true
}

func countInProgress(plan *models.PlanGraph) int {
	count := 0
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusInProgress {
			count++
		}
	}
	return count
}

func fillMeta(plan *models.PlanGraph) {
	meta := models.GraphMeta{
		TotalNodes:            len(plan.Nodes),
		PlannerSource:         plan.Meta.PlannerSource,
		PlannerFallbackReason: plan.Meta.PlannerFallbackReason,
	}
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case models.StatusCompleted:
			meta.CompletedNodes++
		case models.StatusFailed:
			meta.FailedNodes++
		case models.StatusBlocked:
			meta.BlockedNodes++
		case models.StatusInProgress:
			meta.InProgressNodes++
		case models.StatusReady:
			meta.ReadyNodes++
		}
	}
	plan.Meta = meta
}
