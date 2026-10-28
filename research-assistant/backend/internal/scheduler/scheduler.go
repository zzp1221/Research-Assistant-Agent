package scheduler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"research-assistant-backend/internal/agentctx"
	"research-assistant-backend/internal/blobstore"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
	"research-assistant-backend/internal/store"
	"research-assistant-backend/internal/usage"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrStaleExecution = errors.New("stale task execution")
var ErrPlanCanceled = errors.New("plan canceled")
var ErrPlanStateChanged = errors.New("plan state changed")

// The task context and the lease both start at dispatch time. Give the
// executor a short grace period to observe context cancellation first so a
// timeout is reported as "timed out" rather than a synthetic lease expiry.
const taskLeaseGrace = 2 * time.Second

// EventPublisher emits incremental execution events.
type EventPublisher interface {
	Publish(planID string, event models.PlanEvent)
}

// PlanFinalizer computes the semantic plan outcome before the scheduler writes
// a terminal status. It must not change TaskStatus.
type PlanFinalizer interface {
	Finalize(ctx context.Context, plan *models.PlanGraph) error
}

// NoopEventPublisher is used until the SSE/event bus layer is wired in.
type NoopEventPublisher struct{}

func (p *NoopEventPublisher) Publish(planID string, event models.PlanEvent) {
	_ = planID
	_ = event
}

// Scheduler executes one plan graph by continuously promoting and running ready nodes.
type Scheduler struct {
	store         store.PlanStore
	executor      TaskExecutor
	publisher     EventPublisher
	maxConcurrent int
	onTerminal    func(context.Context, *models.PlanGraph)
	finalizer     PlanFinalizer
	// sandboxCleanup releases plan-scoped runtime resources when a plan reaches
	// a terminal state (completed/failed/canceled) or is canceled.
	sandboxCleanup func(context.Context, *models.PlanGraph)
	// blobs externalises large artifact values (images/reports) so PlanGraph
	// snapshots stay small and identical images are stored exactly once.
	blobs *blobstore.Store
	// notify wakes the execution loop immediately after a task transition so the
	// scheduler no longer burns CPU polling every 150-200ms.
	notify chan struct{}
	// globalSlots bounds concurrent task execution across all plans.
	globalSlots chan struct{}
	// activity tracks the last observed progress per plan for the stall watchdog.
	activityMu    sync.Mutex
	lastActivity  map[string]time.Time
	stallNotified map[string]time.Time
	terminalMu    sync.Mutex
	terminals     map[string]time.Time
}

// PollInterval is the idle fallback for the event-driven loop. Task completion
// signals the loop immediately; this only bounds detection of external updates.
const pollInterval = 1 * time.Second

func NewScheduler(planStore store.PlanStore, executor TaskExecutor, publisher EventPublisher, maxConcurrent int) *Scheduler {
	if executor == nil {
		executor = NewDefaultTaskExecutor()
	}
	if publisher == nil {
		publisher = &NoopEventPublisher{}
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	if configured := schedulerEnvInt("PLAN_MAX_CONCURRENT_TASKS", 0); configured > 0 {
		maxConcurrent = configured
	}
	globalLimit := schedulerEnvInt("PLAN_GLOBAL_MAX_TASKS", 8)
	if globalLimit <= 0 {
		globalLimit = 8
	}

	return &Scheduler{
		store:         planStore,
		executor:      executor,
		publisher:     publisher,
		maxConcurrent: maxConcurrent,
		notify:        make(chan struct{}, 1),
		globalSlots:   make(chan struct{}, globalLimit),
		lastActivity:  map[string]time.Time{},
		stallNotified: map[string]time.Time{},
		terminals:     map[string]time.Time{},
	}
}

func schedulerEnvInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func (s *Scheduler) notifyProgress() {
	if s == nil || s.notify == nil {
		return
	}
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Scheduler) touchActivity(planID string) {
	if s == nil || strings.TrimSpace(planID) == "" {
		return
	}
	s.activityMu.Lock()
	defer s.activityMu.Unlock()
	if s.lastActivity == nil {
		s.lastActivity = map[string]time.Time{}
	}
	s.lastActivity[planID] = time.Now()
}

// wait blocks until the next wake-up, the fallback interval, or cancellation.
func (s *Scheduler) wait(ctx context.Context, fallback time.Duration) bool {
	if fallback <= 0 {
		fallback = pollInterval
	}
	timer := time.NewTimer(fallback)
	defer timer.Stop()
	if s.notify == nil {
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	select {
	case <-ctx.Done():
		return false
	case <-s.notify:
		return true
	case <-timer.C:
		return true
	}
}

func (s *Scheduler) acquireGlobalSlot() bool {
	if s.globalSlots == nil {
		return true
	}
	select {
	case s.globalSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Scheduler) releaseGlobalSlot() {
	if s.globalSlots == nil {
		return
	}
	select {
	case <-s.globalSlots:
	default:
	}
}

// recordTerminalMetrics records one terminal transition per execution. It is
// idempotent so the different terminal paths cannot double count.
func (s *Scheduler) recordTerminalMetrics(planID string) {
	if s == nil || strings.TrimSpace(planID) == "" {
		return
	}
	s.terminalMu.Lock()
	if s.terminals == nil {
		s.terminals = map[string]time.Time{}
	}
	if _, seen := s.terminals[planID]; seen {
		s.terminalMu.Unlock()
		return
	}
	s.terminals[planID] = time.Now()
	s.terminalMu.Unlock()

	plan, err := s.store.GetPlan(planID)
	if err != nil || plan == nil {
		return
	}
	metrics.Default().AddGauge("active_plans", -1, nil)
	var duration time.Duration
	if plan.Usage.StartedAt != nil && plan.Usage.FinishedAt != nil {
		duration = plan.Usage.FinishedAt.Sub(*plan.Usage.StartedAt)
	}
	metrics.RecordPlanTerminal(string(plan.Status), string(plan.Outcome), duration)
}

func (s *Scheduler) clearTerminalMarker(planID string) {
	s.terminalMu.Lock()
	defer s.terminalMu.Unlock()
	delete(s.terminals, planID)
}

// checkPlanStall publishes plan_stalled when an in-progress plan has no task
// progress for PLAN_STALL_SECONDS (default 10 minutes). It never kills a plan:
// long legitimately silent commands are common, so the event is advisory.
func (s *Scheduler) checkPlanStall(ctx context.Context, plan *models.PlanGraph) {
	_ = ctx
	if plan == nil || plan.Status != models.StatusInProgress || plan.Paused {
		return
	}
	if countInProgress(plan) == 0 {
		return
	}
	threshold := time.Duration(schedulerEnvInt("PLAN_STALL_SECONDS", 600)) * time.Second
	if threshold <= 0 {
		return
	}
	now := time.Now()
	s.activityMu.Lock()
	last, ok := s.lastActivity[plan.ID]
	if !ok {
		last = plan.UpdatedAt
		if last.IsZero() {
			last = now
		}
		if s.lastActivity == nil {
			s.lastActivity = map[string]time.Time{}
		}
		s.lastActivity[plan.ID] = last
	}
	s.activityMu.Unlock()
	if now.Sub(last) < threshold {
		return
	}
	s.activityMu.Lock()
	if previous, notified := s.stallNotified[plan.ID]; notified && now.Sub(previous) < threshold {
		s.activityMu.Unlock()
		return
	}
	s.stallNotified[plan.ID] = now
	s.activityMu.Unlock()

	running := make([]string, 0, 4)
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusInProgress {
			running = append(running, node.ID)
		}
	}
	s.publishAndStore(plan.ID, models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_stalled",
		Payload: map[string]any{
			"seconds_since_activity": int(now.Sub(last).Seconds()),
			"threshold_seconds":      int(threshold.Seconds()),
			"running_tasks":          running,
		},
		Timestamp: now,
	})
	metrics.Default().Inc("plan_stalled_total", nil)
}

func (s *Scheduler) SetFinalizer(finalizer PlanFinalizer) {
	s.finalizer = finalizer
}

// SetSandboxCleanup registers the plan-level sandbox cleanup hook invoked on
// every terminal transition. It is best-effort and never blocks the transition.
func (s *Scheduler) SetSandboxCleanup(fn func(context.Context, *models.PlanGraph)) {
	s.sandboxCleanup = fn
}

// SetBlobStore enables content-addressed externalisation of large artifacts.
func (s *Scheduler) SetBlobStore(store *blobstore.Store) {
	s.blobs = store
}

// blobThreshold is the value size above which an artifact is externalised.
const blobThreshold = 16 * 1024

// looksLikeBase64Image detects an inline base64 image payload.
func looksLikeBase64Image(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "data:image/") {
		return true
	}
	return strings.HasPrefix(value, "iVBORw0KGgo") || strings.HasPrefix(value, "/9j/")
}

// isImageArtifact reports whether an artifact carries base64 image bytes.
func isImageArtifact(artifact *models.Artifact) bool {
	if artifact == nil {
		return false
	}
	lowerType := strings.ToLower(artifact.Type)
	lowerKey := strings.ToLower(artifact.Key)
	return strings.Contains(lowerType, "image") || strings.Contains(lowerKey, "image") || strings.Contains(lowerKey, "plot")
}

// externalizeResult moves large artifact payloads and task images into the blob
// store before the plan snapshot is written.
func (s *Scheduler) externalizeResult(result *models.TaskExecutionResult) {
	if s.blobs == nil || result == nil {
		return
	}
	for i := range result.Artifacts {
		artifact := &result.Artifacts[i]
		if artifact.BlobRef != "" || len(artifact.Value) <= blobThreshold {
			continue
		}
		payload := []byte(artifact.Value)
		if isImageArtifact(artifact) && looksLikeBase64Image(artifact.Value) {
			// Image artifacts carry base64 text; store the decoded PNG/JPEG so
			// the blob is the real binary and hydration re-encodes exactly once.
			if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(artifact.Value)); err == nil {
				payload = decoded
			}
		}
		ref, size, err := s.blobs.Put(payload)
		if err != nil {
			continue
		}
		if artifact.Metadata == nil {
			artifact.Metadata = map[string]any{}
		}
		artifact.Metadata["size_bytes"] = size
		artifact.Metadata["preview"] = previewValue(artifact.Value)
		artifact.BlobRef = ref
		artifact.Location = "/api/artifacts/" + ref
		artifact.Value = ""
	}
	if result.ImageBase64 != "" && result.ImageBlobRef == "" {
		payload := []byte(result.ImageBase64)
		if looksLikeBase64Image(result.ImageBase64) {
			if decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(result.ImageBase64)); err == nil {
				payload = decoded
			}
		}
		if ref, _, err := s.blobs.Put(payload); err == nil {
			result.ImageBlobRef = ref
			// Drop the inline copy: the PlanGraph keeps only the reference and
			// API responses rehydrate it on read.
			result.ImageBase64 = ""
		}
	}
}

// CleanupPlanSandboxes releases the plan-scoped sandbox containers. It is used
// when a plan becomes terminal outside the execution loop (for example a
// rejected runtime action).
func (s *Scheduler) CleanupPlanSandboxes(ctx context.Context, planID string) {
	s.cleanupPlanSandboxes(ctx, planID)
}

func (s *Scheduler) cleanupPlanSandboxes(ctx context.Context, planID string) {
	if s.sandboxCleanup == nil || strings.TrimSpace(planID) == "" {
		return
	}
	plan, err := s.store.GetPlan(planID)
	if err != nil || plan == nil {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	_ = ctx
	s.sandboxCleanup(cleanupCtx, plan)
}

func (s *Scheduler) CancelPlan(planID, reason string) error {
	return s.CancelPlanWithActor(planID, reason, "")
}

// CancelPlanWithActor cancels a plan, cancels every pending runtime action so a
// stale approval can never resurrect it, and releases plan-scoped sandboxes.
func (s *Scheduler) CancelPlanWithActor(planID, reason, actor string) error {
	now := time.Now()
	if reason == "" {
		reason = "canceled by user"
	}
	canceledActions := 0
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status == models.StatusCompleted || plan.Status == models.StatusFailed || plan.Status == models.StatusCanceled {
			return fmt.Errorf("plan cannot be canceled from status %s", plan.Status)
		}
		plan.Status = models.StatusCanceled
		plan.Usage.FinishedAt = &now
		plan.UpdatedAt = now
		for _, task := range plan.Nodes {
			if task == nil {
				continue
			}
			switch task.Status {
			case models.StatusPending, models.StatusReady, models.StatusInProgress, models.StatusBlocked, models.StatusAwaitingApproval:
				task.Status = models.StatusCanceled
				task.Error = reason
				task.ExecutionID = ""
				task.LeaseOwner = ""
				task.LeaseExpiresAt = nil
				task.FinishedAt = &now
				task.UpdatedAt = now
			}
		}
		for i := range plan.RuntimeActions {
			if plan.RuntimeActions[i].Status != models.RuntimeActionPending {
				continue
			}
			plan.RuntimeActions[i].Status = models.RuntimeActionCanceled
			plan.RuntimeActions[i].DecidedAt = &now
			plan.RuntimeActions[i].DecidedBy = actor
			plan.RuntimeActions[i].DecisionNote = "plan canceled"
			canceledActions++
		}
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}
	payload := map[string]any{"reason": reason}
	if actor != "" {
		payload["actor"] = actor
	}
	if canceledActions > 0 {
		payload["canceled_runtime_actions"] = canceledActions
	}
	s.publishAndStore(planID, models.PlanEvent{PlanID: planID, EventType: "plan_canceled", TaskStatus: string(models.StatusCanceled), Payload: payload, Timestamp: now})
	s.cleanupPlanSandboxes(context.Background(), planID)
	s.recordTerminalMetrics(planID)
	s.notifyProgress()
	return nil
}

func (s *Scheduler) RetryTask(planID, taskID string) error {
	now := time.Now()
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status == models.StatusInProgress {
			return fmt.Errorf("cannot retry a task while the plan is running")
		}
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if task.Status == models.StatusInProgress {
			return fmt.Errorf("cannot retry a task that is currently running")
		}
		if task.Status == models.StatusAwaitingApproval {
			return fmt.Errorf("task is awaiting runtime approval; decide or cancel the action first")
		}
		if task.Status != models.StatusFailed && task.Status != models.StatusBlocked && task.Status != models.StatusCanceled {
			return fmt.Errorf("task cannot be retried from status %s", task.Status)
		}
		resetTaskForRetry(task, now)
		resetBlockedDependents(plan, taskID, now)
		plan.Status = models.StatusPending
		plan.Usage.FinishedAt = nil
		plan.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}
	s.publishAndStore(planID, models.PlanEvent{PlanID: planID, EventType: "task_retry_requested", TaskID: taskID, TaskStatus: string(models.StatusPending), Timestamp: now})
	return nil
}

// PausePlan stops dispatching new tasks. Running tasks finish normally so a
// pause never leaves a half-written task result.
func (s *Scheduler) PausePlan(planID, actor string) error {
	now := time.Now()
	changed := false
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		switch plan.Status {
		case models.StatusCompleted, models.StatusFailed, models.StatusCanceled:
			return fmt.Errorf("plan cannot be paused from status %s", plan.Status)
		}
		if plan.Paused {
			return nil
		}
		plan.Paused = true
		plan.PausedAt = &now
		plan.UpdatedAt = now
		changed = true
		return nil
	}); err != nil {
		return err
	}
	if changed {
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "plan_paused",
			Payload:   map[string]any{"actor": actor},
			Timestamp: now,
		})
		metrics.Default().Inc("plan_paused_total", nil)
	}
	s.notifyProgress()
	return nil
}

// ResumePlan clears the pause flag and wakes the execution loop.
func (s *Scheduler) ResumePlan(planID, actor string) error {
	now := time.Now()
	changed := false
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		switch plan.Status {
		case models.StatusCompleted, models.StatusFailed, models.StatusCanceled:
			return fmt.Errorf("plan cannot be resumed from status %s", plan.Status)
		}
		if !plan.Paused {
			return nil
		}
		plan.Paused = false
		plan.PausedAt = nil
		plan.UpdatedAt = now
		changed = true
		return nil
	}); err != nil {
		return err
	}
	if changed {
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "plan_resumed",
			Payload:   map[string]any{"actor": actor},
			Timestamp: now,
		})
	}
	s.notifyProgress()
	return nil
}

// ResetFromNode rewinds one task and every descendant so a failed or
// unsatisfactory node can be re-run without rebuilding the whole plan.
func (s *Scheduler) ResetFromNode(planID, taskID string) error {
	now := time.Now()
	resetIDs := []string{}
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		target := getNodeByID(plan, taskID)
		if target == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		for _, node := range plan.Nodes {
			if node != nil && node.Status == models.StatusInProgress {
				return fmt.Errorf("plan has running tasks; cancel or wait before rewinding")
			}
		}
		affected := map[string]bool{target.ID: true}
		queue := []string{target.ID}
		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			for _, next := range getOutgoingNodes(plan, current) {
				if next == nil || affected[next.ID] {
					continue
				}
				affected[next.ID] = true
				queue = append(queue, next.ID)
			}
		}
		for _, node := range plan.Nodes {
			if node == nil || !affected[node.ID] {
				continue
			}
			resetTaskForRetry(node, now)
			node.Usage = nil
			resetIDs = append(resetIDs, node.ID)
		}
		for key, artifact := range plan.Artifacts {
			if affected[artifact.ProducerTaskID] {
				delete(plan.Artifacts, key)
			}
		}
		for i := range plan.RuntimeActions {
			action := &plan.RuntimeActions[i]
			if affected[action.TaskID] && action.Status == models.RuntimeActionPending {
				action.Status = models.RuntimeActionCanceled
				action.DecidedAt = &now
				action.DecidedBy = "rewind"
				action.DecisionNote = "plan rewound from node " + taskID
			}
		}
		plan.Status = models.StatusPending
		plan.Outcome = ""
		plan.OutcomeReason = ""
		plan.MissingRequirements = nil
		plan.Paused = false
		plan.PausedAt = nil
		plan.Usage.FinishedAt = nil
		plan.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}
	s.clearTerminalMarker(planID)
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: "plan_rewound",
		TaskID:    taskID,
		Payload:   map[string]any{"reset_tasks": resetIDs},
		Timestamp: now,
	})
	s.notifyProgress()
	return nil
}

func (s *Scheduler) ReassignTask(planID, taskID, assignedTo string) error {
	now := time.Now()
	if assignedTo == "" {
		return fmt.Errorf("assigned agent is required")
	}
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if task.Status == models.StatusCompleted || task.Status == models.StatusSkipped {
			return fmt.Errorf("completed task cannot be reassigned")
		}
		task.AssignedTo = assignedTo
		resetTaskForRetry(task, now)
		resetBlockedDependents(plan, taskID, now)
		plan.Status = models.StatusPending
		plan.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}
	s.publishAndStore(planID, models.PlanEvent{PlanID: planID, EventType: "task_reassigned", TaskID: taskID, TaskStatus: string(models.StatusPending), Payload: map[string]any{"assigned_to": assignedTo}, Timestamp: now})
	return nil
}

func (s *Scheduler) ExecutePlan(ctx context.Context, planID string) error {
	if s.store == nil {
		return fmt.Errorf("plan store is nil")
	}

	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status == models.StatusInProgress {
			return fmt.Errorf("plan is already running")
		}
		if plan.Approval.Required && plan.Approval.Status != "approved" {
			return fmt.Errorf("plan requires approval")
		}
		if plan.Status != models.StatusPending && plan.Status != models.StatusReady {
			return fmt.Errorf("plan cannot run from status %s", plan.Status)
		}
		now := time.Now()
		plan.Status = models.StatusInProgress
		plan.Usage.StartedAt = &now
		plan.Usage.FinishedAt = nil
		plan.UpdatedAt = now
		return nil
	}); err != nil {
		return err
	}
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: "plan_started",
		Timestamp: time.Now(),
	})
	s.clearTerminalMarker(planID)
	metrics.Default().AddGauge("active_plans", 1, nil)
	s.touchActivity(planID)

executionLoop:
	for {
		select {
		case <-ctx.Done():
			if plan, err := s.store.GetPlan(planID); err == nil && plan.Status == models.StatusCanceled {
				return nil
			}
			s.finishPlanTerminal(planID, models.StatusFailed, "execution context ended: "+ctx.Err().Error())
			return ctx.Err()
		default:
		}

		if err := s.expireStaleLeases(planID); err != nil {
			return err
		}
		// Approval waits use an independent budget and never consume the plan
		// execution duration budget.
		s.syncApprovalWaitWindow(planID)
		if expired, expireErr := s.expireApprovalActions(planID); expireErr != nil {
			return expireErr
		} else if expired {
			return nil
		}
		plan, err := s.store.GetPlan(planID)
		if err != nil {
			return err
		}
		if plan.Status == models.StatusCanceled {
			return nil
		}
		if plan.Status == models.StatusFailed && plan.Outcome == models.OutcomeBlocked {
			return nil
		}
		// A paused plan keeps its running tasks but dispatches nothing new. The
		// wait is interruptible so resume wakes the loop immediately.
		if plan.Paused && !allTerminal(plan) {
			s.wait(ctx, pollInterval)
			continue
		}
		s.checkPlanStall(ctx, plan)
		if budgetExceeded(plan) {
			_ = s.CancelPlan(planID, "run budget exceeded")
			return fmt.Errorf("run budget exceeded")
		}

		readyEvents := []models.PlanEvent{}
		if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
			if current.Status == models.StatusCanceled {
				return ErrPlanCanceled
			}
			readyEvents = s.promotePendingNodesToReady(current)
			fillMeta(current)
			return nil
		}); err != nil {
			if errors.Is(err, ErrPlanCanceled) {
				return nil
			}
			return err
		}
		for _, event := range readyEvents {
			s.publishAndStore(planID, event)
		}
		plan, err = s.store.GetPlan(planID)
		if err != nil {
			return err
		}

		availableSlots := s.maxConcurrent - countInProgress(plan)
		ready := s.findReadyNodes(plan, availableSlots)
		if len(ready) == 0 {
			if hasAwaitingApproval(plan) {
				s.wait(ctx, 500*time.Millisecond)
				continue
			}
			if allTerminal(plan) {
				finalStatus := models.StatusCompleted
				if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
					if current.Status == models.StatusCanceled {
						return ErrPlanCanceled
					}
					if !allTerminal(current) {
						return ErrPlanStateChanged
					}
					outcome := models.OutcomeCompleted
					if hasFailure(current) {
						finalStatus = models.StatusFailed
						if hasBlockedOnly(current) {
							outcome = models.OutcomeBlocked
						} else {
							outcome = models.OutcomeFailed
						}
					}
					if s.finalizer != nil && finalStatus == models.StatusCompleted {
						if finalizeErr := s.finalizer.Finalize(ctx, current); finalizeErr != nil {
							finalStatus = models.StatusFailed
							outcome = models.OutcomeFailed
							current.OutcomeReason = finalizeErr.Error()
						} else {
							outcome = current.Outcome
							if outcome == "" {
								outcome = models.OutcomeCompleted
							}
						}
					}
					if finalStatus == models.StatusFailed && outcome == "" {
						outcome = models.OutcomeFailed
					}
					now := time.Now()
					current.Status = finalStatus
					current.Outcome = outcome
					current.Usage.FinishedAt = &now
					current.UpdatedAt = now
					fillMeta(current)
					return nil
				}); err != nil {
					if errors.Is(err, ErrPlanCanceled) {
						return nil
					}
					if errors.Is(err, ErrPlanStateChanged) {
						continue executionLoop
					}
					return err
				}

				eventType := "plan_completed"
				if finalStatus == models.StatusFailed {
					eventType = "plan_failed"
				}
				latest, _ := s.store.GetPlan(planID)
				outcome := models.OutcomeCompleted
				if latest != nil && latest.Outcome != "" {
					outcome = latest.Outcome
				}
				s.publishAndStore(planID, models.PlanEvent{
					PlanID:    planID,
					EventType: eventType,
					Payload:   map[string]any{"outcome": string(outcome)},
					Timestamp: time.Now(),
				})
				s.cleanupPlanSandboxes(context.Background(), planID)
				s.recordTerminalMetrics(planID)
				if s.onTerminal != nil {
					if latest, getErr := s.store.GetPlan(planID); getErr == nil && latest != nil {
						s.onTerminal(ctx, latest)
					} else {
						s.onTerminal(ctx, plan)
					}
				}
				return nil
			}

			if noRunning(plan) {
				// Re-check the attempt budget against the refreshed plan: a task may
				// have failed between the loop-top check and this deadlock check.
				if budgetExceeded(plan) {
					_ = s.CancelPlan(planID, "run budget exceeded")
					return fmt.Errorf("run budget exceeded")
				}
				if err := s.store.UpdatePlan(planID, func(current *models.PlanGraph) error {
					if current.Status == models.StatusCanceled {
						return ErrPlanCanceled
					}
					if !noRunning(current) || len(s.promotePendingNodesToReady(current)) > 0 {
						return ErrPlanStateChanged
					}
					current.Status = models.StatusFailed
					current.UpdatedAt = time.Now()
					fillMeta(current)
					return nil
				}); err != nil {
					if errors.Is(err, ErrPlanCanceled) {
						return nil
					}
					if errors.Is(err, ErrPlanStateChanged) {
						continue executionLoop
					}
					return err
				}
				s.publishAndStore(planID, models.PlanEvent{
					PlanID:    planID,
					EventType: "plan_failed",
					Payload: map[string]any{
						"reason": "deadlock_or_unsatisfied_dependencies",
					},
					Timestamp: time.Now(),
				})
				s.cleanupPlanSandboxes(context.Background(), planID)
				s.recordTerminalMetrics(planID)
				if s.onTerminal != nil {
					if latest, getErr := s.store.GetPlan(planID); getErr == nil && latest != nil {
						s.onTerminal(ctx, latest)
					} else {
						s.onTerminal(ctx, plan)
					}
				}
				return nil
			}

			s.wait(ctx, pollInterval)
			continue
		}

		if err := s.runReadyNodes(ctx, planID, ready); err != nil {
			if errors.Is(err, ErrPlanCanceled) {
				return nil
			}
			s.finishPlanTerminal(planID, models.StatusFailed, err.Error())
			return err
		}

		s.wait(ctx, 250*time.Millisecond)
	}
}

// ExecuteTask runs one task from a stored plan without mutating the plan
// lifecycle. It is used by the owner-scoped "run this node" endpoint, where the
// task inputs and artifacts must come from the persisted plan rather than from
// an arbitrary client payload.
func (s *Scheduler) ExecuteTask(ctx context.Context, planID, taskID string) (*models.TaskExecutionResult, error) {
	if s == nil || s.store == nil {
		return nil, fmt.Errorf("scheduler store is nil")
	}
	if s.executor == nil {
		return nil, fmt.Errorf("scheduler executor is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plan, err := s.store.GetPlan(planID)
	if err != nil {
		return nil, err
	}
	task := getNodeByID(plan, taskID)
	if task == nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if task.Status == models.StatusInProgress {
		return nil, fmt.Errorf("task is already running")
	}
	executionID, leaseOwner, executionEpoch, err := s.markTaskStarted(planID, taskID)
	if err != nil {
		return nil, err
	}

	timeout := time.Duration(task.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	taskCtx, cancel := context.WithTimeout(modelresolver.WithOwner(ctx, plan.OwnerID), timeout)
	defer cancel()

	result, execErr := s.executor.ExecuteTask(taskCtx, plan, task)
	if execErr != nil {
		result = &models.TaskExecutionResult{
			Status: models.StatusFailed,
			Error:  execErr.Error(),
		}
	}
	if result == nil {
		result = &models.TaskExecutionResult{
			Status: models.StatusFailed,
			Error:  "task execution returned nil result",
		}
	}
	if result.Status == "" {
		result.Status = models.StatusCompleted
	}

	if result.Status != models.StatusCompleted {
		if result.Error == "" {
			result.Error = fmt.Sprintf("task returned non-completed status %q", result.Status)
		}
		if _, markErr := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, result, false); markErr != nil {
			return result, markErr
		}
		return result, nil
	}

	if err := s.markTaskCompleted(planID, taskID, executionID, leaseOwner, executionEpoch, result); err != nil {
		failed := &models.TaskExecutionResult{
			Status: models.StatusFailed,
			Error:  err.Error(),
		}
		if _, markErr := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, failed, false); markErr != nil {
			return result, markErr
		}
		return result, err
	}
	return result, nil
}

func (s *Scheduler) promotePendingNodesToReady(plan *models.PlanGraph) []models.PlanEvent {
	events := []models.PlanEvent{}
	for _, node := range plan.Nodes {
		if node == nil || node.Status != models.StatusPending {
			continue
		}
		if allDependenciesCompleted(plan, node) && artifactsSatisfied(plan, node) {
			node.Status = models.StatusReady
			node.UpdatedAt = time.Now()
			events = append(events, models.PlanEvent{
				PlanID:     plan.ID,
				EventType:  "task_ready",
				TaskID:     node.ID,
				TaskStatus: string(node.Status),
				Payload: map[string]any{
					"name":   node.Name,
					"inputs": summarizeTaskInputs(plan, node),
				},
				Timestamp: time.Now(),
			})
		}
	}
	if len(events) > 0 {
		plan.UpdatedAt = time.Now()
	}
	return events
}

func (s *Scheduler) findReadyNodes(plan *models.PlanGraph, availableSlots int) []*models.TaskNode {
	if availableSlots <= 0 {
		return nil
	}

	ready := []*models.TaskNode{}
	for _, node := range plan.Nodes {
		if node != nil && node.Status == models.StatusReady {
			ready = append(ready, node)
		}
	}

	if len(ready) == 0 {
		return nil
	}

	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority > ready[j].Priority
		}
		return ready[i].CreatedAt.Before(ready[j].CreatedAt)
	})

	serial := make([]*models.TaskNode, 0, len(ready))
	parallel := make([]*models.TaskNode, 0, len(ready))
	for _, node := range ready {
		if node.Parallelizable {
			parallel = append(parallel, node)
		} else {
			serial = append(serial, node)
		}
	}

	if len(serial) > 0 {
		return serial[:1]
	}
	if len(parallel) > availableSlots {
		return parallel[:availableSlots]
	}
	return parallel
}

func (s *Scheduler) runReadyNodes(ctx context.Context, planID string, ready []*models.TaskNode) error {
	for _, task := range ready {
		// The global slot bounds concurrent task execution across plans so one
		// user cannot occupy every CPU/sandbox slot. When saturated the caller
		// simply loops again after a wake-up.
		if !s.acquireGlobalSlot() {
			return nil
		}
		action, pending, err := s.prepareRuntimeGate(planID, task.ID)
		if err != nil {
			s.releaseGlobalSlot()
			return err
		}
		if pending {
			s.releaseGlobalSlot()
			return nil
		}
		if action != nil && action.Status == models.RuntimeActionRejected {
			s.releaseGlobalSlot()
			return nil
		}
		executionID, leaseOwner, executionEpoch, err := s.markTaskStarted(planID, task.ID)
		if err != nil {
			s.releaseGlobalSlot()
			return err
		}

		taskID := task.ID
		taskType := task.Type
		go func() {
			// Wake the scheduler and free the global slot on every exit path.
			defer s.notifyProgress()
			defer s.releaseGlobalSlot()
			defer s.touchActivity(planID)

			plan, err := s.store.GetPlan(planID)
			if err != nil {
				log.Printf("scheduler: failed to load plan %s for task %s: %v", planID, taskID, err)
				return
			}
			currentTask := getNodeByID(plan, taskID)
			if currentTask == nil {
				log.Printf("scheduler: task not found during execution: %s", taskID)
				return
			}

			logChannel := make(chan string, 64)
			recorder := usage.NewRecorder()
			// Bind the plan owner so runtime model profiles resolve per user.
			execCtx := modelresolver.WithOwner(usage.WithRecorder(agentctx.WithLogChannel(ctx, logChannel), recorder), plan.OwnerID)
			taskTimeout := time.Duration(currentTask.TimeoutSeconds) * time.Second
			if taskTimeout <= 0 {
				taskTimeout = 10 * time.Minute
			}
			taskCtx, cancelTask := context.WithTimeout(execCtx, taskTimeout)
			forwardDone := make(chan struct{})
			go s.forwardTaskLogs(planID, taskID, logChannel, forwardDone)
			startedAt := time.Now()

			result, execErr := s.executor.ExecuteTask(taskCtx, plan, currentTask)
			if errors.Is(taskCtx.Err(), context.DeadlineExceeded) {
				execErr = fmt.Errorf("task execution timed out after %s: %w", taskTimeout, taskCtx.Err())
			} else if errors.Is(taskCtx.Err(), context.Canceled) {
				execErr = context.Canceled
			}
			cancelTask()
			close(logChannel)
			<-forwardDone

			if execErr != nil {
				result = &models.TaskExecutionResult{
					Status: models.StatusFailed,
					Error:  execErr.Error(),
				}
			}
			if result == nil {
				result = &models.TaskExecutionResult{
					Status: models.StatusFailed,
					Error:  "task execution returned nil result",
				}
			}
			result.Usage = taskUsageFromSnapshot(recorder.Snapshot(), time.Since(startedAt))
			metrics.RecordTask(taskType, string(result.Status), time.Since(startedAt))

			if result.Status != models.StatusCompleted {
				if result.Error == "" {
					result.Error = fmt.Sprintf("task returned non-completed status %q", result.Status)
				}
				retrying, err := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, result, true)
				if errors.Is(err, ErrStaleExecution) {
					s.publishDiscardedResult(planID, taskID, executionID, leaseOwner)
					return
				}
				if err != nil {
					log.Printf("scheduler: markTaskFailed failed for %s: %v", taskID, err)
					return
				}
				if !retrying && shouldBlockOnFailure(currentTask) {
					if err := s.blockDependents(planID, taskID); err != nil {
						log.Printf("scheduler: blockDependents failed for %s: %v", taskID, err)
						return
					}
				}
				return
			}

			if err := s.markTaskCompleted(planID, taskID, executionID, leaseOwner, executionEpoch, result); errors.Is(err, ErrStaleExecution) {
				s.publishDiscardedResult(planID, taskID, executionID, leaseOwner)
				return
			} else if err != nil {
				log.Printf("scheduler: markTaskCompleted failed for %s: %v", taskID, err)
				failed := &models.TaskExecutionResult{Status: models.StatusFailed, Error: err.Error()}
				if _, markErr := s.markTaskFailed(planID, taskID, executionID, leaseOwner, executionEpoch, failed, true); markErr != nil {
					log.Printf("scheduler: markTaskFailed after completion validation failed for %s: %v", taskID, markErr)
				}
				return
			}
		}()
	}
	return nil
}

// taskUsageFromSnapshot converts the usage recorder output into the persisted
// task usage shape. Duration is measured by the scheduler so it includes agent
// overhead beyond the individual LLM calls.
func taskUsageFromSnapshot(snapshot usage.Snapshot, duration time.Duration) *models.TaskUsage {
	taskUsage := &models.TaskUsage{
		LLMPromptTokens:     snapshot.PromptTokens,
		LLMCompletionTokens: snapshot.CompletionTokens,
		LLMCalls:            snapshot.LLMCalls,
		LLMCostUSD:          snapshot.CostUSD,
		LLMEstimated:        snapshot.Estimated,
		SandboxSeconds:      snapshot.SandboxSeconds,
		DurationSeconds:     duration.Seconds(),
		Attempts:            1,
	}
	if len(snapshot.ByModel) > 0 {
		taskUsage.Models = map[string]int{}
		for model, entry := range snapshot.ByModel {
			taskUsage.Models[model] = entry.Calls
		}
	}
	return taskUsage
}

func applyTaskUsage(task *models.TaskNode, plan *models.PlanGraph, taskUsage *models.TaskUsage) {
	if task == nil || taskUsage == nil {
		return
	}
	if task.Usage == nil {
		task.Usage = &models.TaskUsage{}
	}
	task.Usage.Add(taskUsage)
	if plan != nil {
		plan.Usage.AddTaskUsage(taskUsage)
	}
	// Prompt-budget diagnostics live in task metadata; surface them in usage
	// and metrics so truncation is observable.
	if task.Metadata != nil {
		if report, ok := task.Metadata["prompt_budget"].(prompts.PromptReport); ok {
			task.Usage.PromptBudgetLimit = report.Budget
			task.Usage.PromptTruncatedSections = len(report.TruncatedSections())
			for _, section := range report.Sections {
				if section.Truncated || section.Dropped {
					metrics.RecordPromptTruncatedSection(section.Name)
				}
			}
		}
	}
}

func (s *Scheduler) forwardTaskLogs(planID, taskID string, logChannel <-chan string, done chan<- struct{}) {
	defer close(done)
	for line := range logChannel {
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:     planID,
			EventType:  "task_log",
			TaskID:     taskID,
			TaskStatus: string(models.StatusInProgress),
			Payload: map[string]any{
				"message": line,
			},
			Timestamp: time.Now(),
		})
	}
}

func (s *Scheduler) markTaskStarted(planID, taskID string) (string, string, int, error) {
	now := time.Now()
	executionID := uuid.NewString()
	leaseOwner := ""
	executionEpoch := 0
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status == models.StatusCanceled {
			return ErrPlanCanceled
		}
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if plan.Budget.MaxTaskAttempts > 0 && plan.Usage.TaskAttempts >= plan.Budget.MaxTaskAttempts {
			return fmt.Errorf("task attempt budget exceeded")
		}
		leaseOwner = task.AssignedTo
		timeout := time.Duration(task.TimeoutSeconds) * time.Second
		if timeout <= 0 {
			timeout = 10 * time.Minute
		}
		leaseExpiresAt := now.Add(timeout)
		task.Status = models.StatusInProgress
		task.ExecutionID = executionID
		task.ExecutionEpoch++
		executionEpoch = task.ExecutionEpoch
		task.LeaseOwner = leaseOwner
		task.LeaseExpiresAt = &leaseExpiresAt
		task.RunCount++
		task.StartedAt = &now
		task.FinishedAt = nil
		task.UpdatedAt = now
		plan.Usage.TaskAttempts++
		fillMeta(plan)
		return nil
	}); err != nil {
		return "", "", 0, err
	}

	s.publishAndStore(planID, models.PlanEvent{
		PlanID:      planID,
		EventType:   "task_started",
		TaskID:      taskID,
		TaskStatus:  string(models.StatusInProgress),
		ExecutionID: executionID,
		Payload:     map[string]any{"lease_owner": leaseOwner, "execution_epoch": executionEpoch},
		Timestamp:   now,
	})
	return executionID, leaseOwner, executionEpoch, nil
}

func (s *Scheduler) markTaskCompleted(planID, taskID, executionID, leaseOwner string, executionEpoch int, result *models.TaskExecutionResult) error {
	now := time.Now()
	s.externalizeResult(result)
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if !executionMatches(task, executionID, leaseOwner, executionEpoch) {
			return ErrStaleExecution
		}
		task.Status = models.StatusCompleted
		task.Result = result.Result
		task.Code = result.Code
		task.StructuredData = result.StructuredData
		task.ImageBase64 = result.ImageBase64
		if result.ImageBlobRef != "" {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["image_blob_ref"] = result.ImageBlobRef
		}
		task.Error = ""
		task.ExecutionID = ""
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		task.FinishedAt = &now
		task.UpdatedAt = now
		if len(result.Metadata) > 0 {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			for key, value := range result.Metadata {
				task.Metadata[key] = value
			}
		}
		applyTaskUsage(task, plan, result.Usage)
		if result.Verification != nil {
			task.Verification = result.Verification
			plan.Verification = result.Verification
			// Stamp every image artifact the plan has produced so the UI can show a
			// per-figure badge without joining the verification record.
			for key, artifact := range plan.Artifacts {
				annotateArtifactVerification(&artifact, result.Verification)
				plan.Artifacts[key] = artifact
			}
		}

		seenArtifacts := map[string]struct{}{}
		for _, artifact := range result.Artifacts {
			key := strings.TrimSpace(artifact.Key)
			if key == "" {
				return fmt.Errorf("task %s produced an artifact with an empty key", taskID)
			}
			if strings.TrimSpace(artifact.Value) == "" && strings.TrimSpace(artifact.Location) == "" && strings.TrimSpace(artifact.BlobRef) == "" {
				// Optional image artifacts (execution_image) may legitimately be
				// empty when a step produced no figure; downstream binding
				// treats the empty value as "no upstream image".
				if !strings.Contains(strings.ToLower(key), "image") {
					return fmt.Errorf("task %s produced empty artifact %q", taskID, key)
				}
			}
			if _, exists := seenArtifacts[key]; exists {
				return fmt.Errorf("task %s produced duplicate artifact %q", taskID, key)
			}
			if existing, exists := plan.Artifacts[key]; exists &&
				strings.TrimSpace(existing.ProducerTaskID) != "" &&
				existing.ProducerTaskID != taskID {
				return fmt.Errorf("artifact %q was already produced by task %s", key, existing.ProducerTaskID)
			}
			if result.Verification != nil {
				annotateArtifactVerification(&artifact, result.Verification)
			}
			seenArtifacts[key] = struct{}{}
			plan.Artifacts[key] = artifact
		}

		fillMeta(plan)
		return nil
	}); err != nil {
		return err
	}

	if len(result.Artifacts) > 0 {
		keys := make([]string, 0, len(result.Artifacts))
		for _, artifact := range result.Artifacts {
			keys = append(keys, artifact.Key)
		}
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "artifact_created",
			TaskID:    taskID,
			Payload: map[string]any{
				"artifact_keys": keys,
				"artifacts":     summarizeArtifacts(result.Artifacts),
			},
			Timestamp: now,
		})
	}

	s.publishAndStore(planID, models.PlanEvent{
		PlanID:      planID,
		EventType:   "task_completed",
		TaskID:      taskID,
		TaskStatus:  string(models.StatusCompleted),
		ExecutionID: executionID,
		Payload: map[string]any{
			"result_summary":  result.Result,
			"result":          result.Result,
			"code":            result.Code,
			"structured_data": result.StructuredData,
			"image_base64":    result.ImageBase64,
			"image_blob_ref":  result.ImageBlobRef,
		},
		Timestamp: now,
	})
	if result.Verification != nil {
		eventType := "verification_completed"
		if result.Verification.Status != models.VerificationPass && result.Verification.Status != models.VerificationSkipped {
			eventType = "verification_failed"
		}
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:     planID,
			EventType:  eventType,
			TaskID:     taskID,
			TaskStatus: string(models.StatusCompleted),
			Payload:    verificationEventPayload(result.Verification),
			Timestamp:  now,
		})
	}
	return nil
}

// annotateArtifactVerification stamps the per-artifact verdict onto image
// artifacts so the UI can show a corner badge without joining the plan.
func annotateArtifactVerification(artifact *models.Artifact, verification *models.Verification) {
	if artifact == nil || verification == nil {
		return
	}
	key := strings.ToLower(artifact.Key)
	if !strings.Contains(key, "image") && !strings.Contains(key, "plot") {
		return
	}
	if artifact.Metadata == nil {
		artifact.Metadata = map[string]any{}
	}
	artifact.Metadata["verification"] = map[string]any{
		"status":   verification.Status,
		"level":    verification.Level,
		"verified": verification.Verified,
		"reason":   verification.Reason,
		"issues":   verification.Issues,
	}
}

func verificationEventPayload(verification *models.Verification) map[string]any {
	if verification == nil {
		return nil
	}
	return map[string]any{
		"verification": map[string]any{
			"status":      verification.Status,
			"level":       verification.Level,
			"verified":    verification.Verified,
			"reason":      verification.Reason,
			"issues":      verification.Issues,
			"confidence":  verification.Confidence,
			"verified_by": verification.VerifiedBy,
			"checked_at":  verification.CheckedAt,
		},
	}
}

func (s *Scheduler) markTaskFailed(planID, taskID, executionID, leaseOwner string, executionEpoch int, result *models.TaskExecutionResult, allowRetry bool) (bool, error) {
	now := time.Now()
	s.externalizeResult(result)
	retrying := false
	blockedStatus := false
	if err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		task := getNodeByID(plan, taskID)
		if task == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		if !executionMatches(task, executionID, leaseOwner, executionEpoch) {
			return ErrStaleExecution
		}
		task.Error = result.Error
		// Keep whatever the agent produced before failing: partial code, metrics
		// or structured evidence is still useful for debugging and the UI.
		if strings.TrimSpace(result.Result) != "" {
			task.Result = result.Result
		}
		if strings.TrimSpace(result.Code) != "" {
			task.Code = result.Code
		}
		if strings.TrimSpace(result.StructuredData) != "" {
			task.StructuredData = result.StructuredData
		}
		if strings.TrimSpace(result.ImageBase64) != "" {
			task.ImageBase64 = result.ImageBase64
		}
		if result.ImageBlobRef != "" {
			if task.Metadata == nil {
				task.Metadata = map[string]any{}
			}
			task.Metadata["image_blob_ref"] = result.ImageBlobRef
		}
		for _, artifact := range result.Artifacts {
			key := strings.TrimSpace(artifact.Key)
			if key == "" || (strings.TrimSpace(artifact.Value) == "" && strings.TrimSpace(artifact.Location) == "") {
				continue
			}
			plan.Artifacts[key] = artifact
		}
		for _, missing := range result.MissingRequirements {
			if missing.Kind == "" {
				continue
			}
			plan.MissingRequirements = appendMissingRequirement(plan.MissingRequirements, missing)
		}
		task.ExecutionID = ""
		task.LeaseOwner = ""
		task.LeaseExpiresAt = nil
		applyTaskUsage(task, plan, result.Usage)
		blocked := result.Status == models.StatusBlocked
		if blocked {
			task.Status = models.StatusBlocked
			blockedStatus = true
			task.FinishedAt = &now
		} else if allowRetry && task.RunCount <= task.RetryLimit {
			task.Status = models.StatusPending
			task.FinishedAt = nil
			retrying = true
		} else {
			task.Status = models.StatusFailed
			task.FinishedAt = &now
		}
		task.UpdatedAt = now
		fillMeta(plan)
		return nil
	}); err != nil {
		return false, err
	}

	if retrying {
		s.publishAndStore(planID, models.PlanEvent{PlanID: planID, EventType: "task_retrying", TaskID: taskID, TaskStatus: string(models.StatusPending), ExecutionID: executionID, Payload: map[string]any{"error": result.Error}, Timestamp: now})
		return true, nil
	}
	eventType := "task_failed"
	taskStatus := models.StatusFailed
	if blockedStatus {
		eventType = "task_blocked"
		taskStatus = models.StatusBlocked
	}
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:      planID,
		EventType:   eventType,
		TaskID:      taskID,
		TaskStatus:  string(taskStatus),
		ExecutionID: executionID,
		Payload: map[string]any{
			"error":                result.Error,
			"missing_requirements": result.MissingRequirements,
		},
		Timestamp: now,
	})
	return false, nil
}

func summarizeTaskInputs(plan *models.PlanGraph, node *models.TaskNode) []map[string]any {
	if plan == nil || node == nil {
		return nil
	}

	inputs := []map[string]any{}
	for _, key := range node.RequiredArtifacts {
		artifact, ok := plan.Artifacts[key]
		if !ok {
			continue
		}
		inputs = append(inputs, map[string]any{
			"key":              key,
			"type":             artifact.Type,
			"producer_task_id": artifact.ProducerTaskID,
			"value_preview":    previewValue(artifact.Value),
		})
	}
	return inputs
}

func summarizeArtifacts(artifacts []models.Artifact) []map[string]any {
	out := make([]map[string]any, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, map[string]any{
			"key":              artifact.Key,
			"type":             artifact.Type,
			"producer_task_id": artifact.ProducerTaskID,
			"blob_ref":         artifact.BlobRef,
			"location":         artifact.Location,
			"value_preview":    previewValue(artifact.Value),
		})
	}
	return out
}

func previewValue(value string) string {
	const limit = 1000
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "...[truncated]"
}

func executionMatches(task *models.TaskNode, executionID, leaseOwner string, executionEpoch int) bool {
	return task != nil &&
		task.Status == models.StatusInProgress &&
		task.ExecutionID == executionID &&
		task.ExecutionEpoch == executionEpoch &&
		task.LeaseOwner == leaseOwner &&
		task.AssignedTo == leaseOwner &&
		(task.LeaseExpiresAt == nil || time.Now().Before(task.LeaseExpiresAt.Add(taskLeaseGrace)))
}

func (s *Scheduler) finishPlanTerminal(planID string, status models.TaskStatus, reason string) {
	now := time.Now()
	changed := false
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		switch plan.Status {
		case models.StatusCompleted, models.StatusFailed, models.StatusCanceled:
			return nil
		}
		changed = true
		plan.Status = status
		if status == models.StatusFailed {
			plan.Outcome = models.OutcomeFailed
		}
		plan.Usage.FinishedAt = &now
		plan.UpdatedAt = now
		for _, task := range plan.Nodes {
			if task == nil {
				continue
			}
			switch task.Status {
			case models.StatusPending, models.StatusReady, models.StatusInProgress, models.StatusBlocked, models.StatusAwaitingApproval:
				task.Status = models.StatusCanceled
				task.Error = reason
				task.ExecutionID = ""
				task.LeaseOwner = ""
				task.LeaseExpiresAt = nil
				task.FinishedAt = &now
				task.UpdatedAt = now
			}
		}
		fillMeta(plan)
		return nil
	})
	if err != nil {
		log.Printf("scheduler: finalize plan %s failed: %v", planID, err)
		return
	}
	if !changed {
		return
	}
	eventType := "plan_failed"
	if status == models.StatusCanceled {
		eventType = "plan_canceled"
	}
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: eventType,
		Payload:   map[string]any{"reason": reason},
		Timestamp: now,
	})
	s.cleanupPlanSandboxes(context.Background(), planID)
	s.recordTerminalMetrics(planID)
	s.notifyProgress()
}

func (s *Scheduler) expireStaleLeases(planID string) error {
	now := time.Now()
	current, err := s.store.GetPlan(planID)
	if err != nil {
		return err
	}
	if current.Status != models.StatusInProgress {
		return nil
	}
	hasExpired := false
	for _, task := range current.Nodes {
		if task != nil && task.Status == models.StatusInProgress && task.LeaseExpiresAt != nil && !now.Before(task.LeaseExpiresAt.Add(taskLeaseGrace)) {
			hasExpired = true
			break
		}
	}
	if !hasExpired {
		return nil
	}
	events := []models.PlanEvent{}
	err = s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		if plan.Status != models.StatusInProgress {
			return nil
		}
		for _, task := range plan.Nodes {
			if task == nil || task.Status != models.StatusInProgress || task.LeaseExpiresAt == nil {
				continue
			}
			if now.Before(task.LeaseExpiresAt.Add(taskLeaseGrace)) {
				continue
			}
			task.Status = models.StatusFailed
			task.Error = "execution lease expired"
			task.ExecutionID = ""
			task.LeaseOwner = ""
			task.LeaseExpiresAt = nil
			task.FinishedAt = &now
			task.UpdatedAt = now
			events = append(events, models.PlanEvent{
				PlanID:     planID,
				EventType:  "task_failed",
				TaskID:     task.ID,
				TaskStatus: string(task.Status),
				Payload:    map[string]any{"error": task.Error, "reason": "lease_expired"},
				Timestamp:  now,
			})
		}
		fillMeta(plan)
		return nil
	})
	if err != nil {
		return err
	}
	for _, event := range events {
		s.publishAndStore(planID, event)
	}
	return nil
}

func resetTaskForRetry(task *models.TaskNode, now time.Time) {
	task.Status = models.StatusPending
	task.Result = ""
	task.Error = ""
	task.ExecutionID = ""
	task.LeaseOwner = ""
	task.LeaseExpiresAt = nil
	task.RunCount = 0
	task.StartedAt = nil
	task.FinishedAt = nil
	task.UpdatedAt = now
}

func resetBlockedDependents(plan *models.PlanGraph, taskID string, now time.Time) {
	visited := map[string]bool{}
	var visit func(string)
	visit = func(currentID string) {
		for _, next := range getOutgoingNodes(plan, currentID) {
			if next == nil || visited[next.ID] {
				continue
			}
			visited[next.ID] = true
			if next.Status == models.StatusBlocked || next.Status == models.StatusCanceled {
				resetTaskForRetry(next, now)
			}
			visit(next.ID)
		}
	}
	visit(taskID)
}

// approvalTimeoutSeconds is the independent budget for waiting on a human
// decision. It never counts toward the plan execution duration budget.
func approvalTimeoutSeconds() int {
	const fallback = 1800
	raw := strings.TrimSpace(os.Getenv("APPROVAL_TIMEOUT_SECONDS"))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

// effectivePlanElapsed excludes approval wait windows from the execution budget.
func effectivePlanElapsed(plan *models.PlanGraph, now time.Time) time.Duration {
	if plan == nil || plan.Usage.StartedAt == nil {
		return 0
	}
	elapsed := now.Sub(*plan.Usage.StartedAt)
	elapsed -= time.Duration(plan.Usage.ApprovalWaitSeconds) * time.Second
	if plan.Usage.ApprovalWaitingSince != nil {
		if waiting := now.Sub(*plan.Usage.ApprovalWaitingSince); waiting > 0 {
			elapsed -= waiting
		}
	}
	if elapsed < 0 {
		return 0
	}
	return elapsed
}

func budgetExceeded(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	// A plan that finished every task successfully must not be canceled just
	// because it used its final allowed attempt. Any other terminal mix
	// (failure/blocked/canceled) still means the budget was exhausted.
	if plan.Budget.MaxTaskAttempts > 0 && plan.Usage.TaskAttempts >= plan.Budget.MaxTaskAttempts && noRunning(plan) && !allSucceeded(plan) {
		return true
	}
	if plan.Budget.MaxDurationSec <= 0 || plan.Usage.StartedAt == nil {
		return false
	}
	return effectivePlanElapsed(plan, time.Now()) > time.Duration(plan.Budget.MaxDurationSec)*time.Second
}

// syncApprovalWaitWindow keeps RunUsage.ApprovalWaitingSince in sync with the
// actual awaiting-approval state so the execution budget can be paused.
func (s *Scheduler) syncApprovalWaitWindow(planID string) {
	_ = s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		now := time.Now()
		waiting := hasAwaitingApproval(plan)
		if waiting && plan.Usage.ApprovalWaitingSince == nil {
			plan.Usage.ApprovalWaitingSince = &now
			plan.UpdatedAt = now
			return nil
		}
		if !waiting && plan.Usage.ApprovalWaitingSince != nil {
			waited := now.Sub(*plan.Usage.ApprovalWaitingSince)
			if waited > 0 {
				plan.Usage.ApprovalWaitSeconds += int(waited / time.Second)
			}
			plan.Usage.ApprovalWaitingSince = nil
			plan.UpdatedAt = now
		}
		return nil
	})
}

// expireApprovalActions turns every pending runtime action that exceeded
// APPROVAL_TIMEOUT_SECONDS into an expired action, blocks its task and moves the
// plan to a blocked terminal state with an actionable missing requirement.
func (s *Scheduler) expireApprovalActions(planID string) (bool, error) {
	timeout := time.Duration(approvalTimeoutSeconds()) * time.Second
	expired := false
	events := []models.PlanEvent{}
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		now := time.Now()
		for i := range plan.RuntimeActions {
			action := &plan.RuntimeActions[i]
			if action.Status != models.RuntimeActionPending {
				continue
			}
			if action.RequestedAt.IsZero() || now.Sub(action.RequestedAt) < timeout {
				continue
			}
			action.Status = models.RuntimeActionExpired
			action.DecidedAt = &now
			action.DecidedBy = "system:approval_timeout"
			action.DecisionNote = "approval timed out"
			expired = true
			task := getNodeByID(plan, action.TaskID)
			if task != nil {
				task.Status = models.StatusBlocked
				task.Error = "approval timed out after " + strconv.Itoa(approvalTimeoutSeconds()) + "s"
				task.FinishedAt = &now
				task.UpdatedAt = now
			}
			plan.MissingRequirements = appendMissingRequirement(plan.MissingRequirements, models.MissingRequirement{
				Kind:        "approval_timeout",
				Name:        string(action.Kind),
				Detail:      "等待人工审批超过 APPROVAL_TIMEOUT_SECONDS，动作已过期",
				Remediation: "重新创建计划并在审批窗口内确认该操作。",
			})
			events = append(events, models.PlanEvent{
				PlanID:    plan.ID,
				TaskID:    action.TaskID,
				EventType: "runtime_action_expired",
				Payload:   map[string]any{"action": action, "reason": "approval_timeout"},
				Timestamp: now,
			})
		}
		if expired {
			plan.Status = models.StatusFailed
			plan.Outcome = models.OutcomeBlocked
			plan.OutcomeReason = "approval timed out"
			plan.Usage.FinishedAt = &now
			plan.Usage.ApprovalWaitingSince = nil
			plan.UpdatedAt = now
			for _, task := range plan.Nodes {
				if task == nil {
					continue
				}
				switch task.Status {
				case models.StatusPending, models.StatusReady, models.StatusInProgress, models.StatusAwaitingApproval:
					task.Status = models.StatusCanceled
					task.Error = "plan aborted: approval timed out"
					task.ExecutionID = ""
					task.LeaseOwner = ""
					task.LeaseExpiresAt = nil
					task.FinishedAt = &now
					task.UpdatedAt = now
				}
			}
			fillMeta(plan)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	for _, event := range events {
		s.publishAndStore(planID, event)
	}
	if expired {
		s.publishAndStore(planID, models.PlanEvent{
			PlanID:    planID,
			EventType: "plan_failed",
			Payload:   map[string]any{"outcome": string(models.OutcomeBlocked), "reason": "approval_timeout"},
			Timestamp: time.Now(),
		})
		s.cleanupPlanSandboxes(context.Background(), planID)
		s.recordTerminalMetrics(planID)
	}
	s.notifyProgress()
	return expired, nil
}

func (s *Scheduler) publishDiscardedResult(planID, taskID, executionID, leaseOwner string) {
	s.publishAndStore(planID, models.PlanEvent{
		PlanID:      planID,
		EventType:   "task_result_discarded",
		TaskID:      taskID,
		ExecutionID: executionID,
		Payload: map[string]any{
			"reason":      "stale_execution_lease",
			"lease_owner": leaseOwner,
		},
		Timestamp: time.Now(),
	})
}

func (s *Scheduler) blockDependents(planID, taskID string) error {
	events := []models.PlanEvent{}
	err := s.store.UpdatePlan(planID, func(plan *models.PlanGraph) error {
		visited := map[string]bool{}
		var visit func(string)
		visit = func(currentID string) {
			for _, next := range getOutgoingNodes(plan, currentID) {
				if next == nil || visited[next.ID] {
					continue
				}
				visited[next.ID] = true
				if next.Status == models.StatusPending || next.Status == models.StatusReady {
					next.Status = models.StatusBlocked
					next.UpdatedAt = time.Now()
					events = append(events, models.PlanEvent{
						PlanID:     planID,
						EventType:  "task_blocked",
						TaskID:     next.ID,
						TaskStatus: string(models.StatusBlocked),
						Payload: map[string]any{
							"reason":           "upstream_failed",
							"upstream_task_id": currentID,
						},
						Timestamp: time.Now(),
					})
				}
				visit(next.ID)
			}
		}
		visit(taskID)
		fillMeta(plan)
		return nil
	})
	if err != nil {
		return err
	}
	if len(events) > 0 {
		for _, event := range events {
			s.publishAndStore(planID, event)
		}
	}
	return nil
}

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
	if event.TraceID == "" {
		if plan, err := s.store.GetPlan(planID); err == nil && plan != nil {
			event.TraceID = plan.TraceID
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
		case models.StatusPending, models.StatusReady, models.StatusInProgress, models.StatusAwaitingApproval:
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
