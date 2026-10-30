package scheduler

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"research-assistant-backend/internal/blobstore"
	"research-assistant-backend/internal/budget"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
	"strconv"
	"strings"
	"sync"
	"time"
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
	// budget is the owner cost ledger. It is optional; nil disables user-budget
	// reservations while plan-level RunBudget limits still apply.
	budget       *budget.Manager
	budgetMu     sync.Mutex
	reservations map[string]budget.Reservation
	// toolGate decides whether a tool_call node needs a human approval before
	// dispatch. It is injected by the API layer so the scheduler does not need
	// to know the tool registry.
	toolGate ToolGateFunc
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
		reservations:  map[string]budget.Reservation{},
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
