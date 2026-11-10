package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"research-assistant-backend/internal/events"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/revision"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
)

// switchingPlanStore is a store.PlanStore that flips the plan state inside
// UpdatePlan *before* delegating to the wrapped store, simulating the TOCTOU
// window between authorizedPlan and the revision mutation. It makes the
// scheduler/other request that wins the race deterministic instead of timing
// dependent.
type switchingPlanStore struct {
	store.PlanStore
	mutate func(plan *models.PlanGraph)
	calls  int
}

func (s *switchingPlanStore) UpdatePlan(planID string, update func(*models.PlanGraph) error) error {
	s.calls++
	if s.calls == 1 && s.mutate != nil {
		if err := s.PlanStore.UpdatePlan(planID, func(plan *models.PlanGraph) error {
			s.mutate(plan)
			return nil
		}); err != nil {
			return err
		}
	}
	return s.PlanStore.UpdatePlan(planID, update)
}

// failingEventStore fails every event append, so a caller that ignores the
// error is visibly wrong.
type failingEventStore struct {
	store.PlanStore
	mu      sync.Mutex
	appends int
}

var errFixtureEventAppend = errors.New("fixture: event log unavailable")

func (s *failingEventStore) AppendEvent(planID string, event models.PlanEvent) error {
	s.mu.Lock()
	s.appends++
	s.mu.Unlock()
	return errFixtureEventAppend
}

func (s *failingEventStore) AppendEventReturning(planID string, event models.PlanEvent) (models.PlanEvent, error) {
	s.mu.Lock()
	s.appends++
	s.mu.Unlock()
	return models.PlanEvent{}, errFixtureEventAppend
}

func (s *failingEventStore) appendCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appends
}

type revisionLockFixture struct {
	router   *gin.Engine
	planBase store.PlanStore
	runtime  *planRuntime
	bus      *events.Bus
}

func newRevisionLockFixture(t *testing.T) *revisionLockFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	bus := events.NewBus()
	runner := scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), bus, 1)
	runtime := newPlanRuntime(planner.NewPlanner(), planStore, runner, bus)
	runtime.revisions = revision.NewMemoryStore()
	router := gin.New()
	runtime.register(router.Group("/api"))
	return &revisionLockFixture{router: router, planBase: planStore, runtime: runtime, bus: bus}
}

func (f *revisionLockFixture) useStore(t *testing.T, wrapped store.PlanStore) {
	t.Helper()
	f.runtime.store = wrapped
}

// seedRevisionedPlan stores a plan and materialises revision 1, the state the
// review panel is in when it starts editing.
func seedRevisionedPlan(t *testing.T, f *revisionLockFixture, owner, id string) *models.PlanGraph {
	t.Helper()
	plan := seedRevisionPlan(t, f.planBase, owner, id)
	response := revisionRequest(f.router, http.MethodGet, "/api/plans/"+id+"/revisions", owner, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("seed revisions status=%d body=%s", response.Code, response.Body.String())
	}
	return plan
}

func revisionCount(t *testing.T, runtime *planRuntime, planID string) int {
	t.Helper()
	list, err := runtime.revisionStore().List(planID, false)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	return len(list)
}

func TestCreateRevisionRequiresPositiveBaseRevision(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionedPlan(t, f, "owner-a", "plan-base")

	cases := []struct {
		name string
		body map[string]any
	}{
		{name: "missing", body: map[string]any{"graph": revisionPlanBody(plan)}},
		{name: "zero", body: map[string]any{"base_revision": 0, "graph": revisionPlanBody(plan)}},
		{name: "negative", body: map[string]any{"base_revision": -3, "graph": revisionPlanBody(plan)}},
	}
	for _, tc := range cases {
		response := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-base/revisions", "owner-a", tc.body)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400, got %d body=%s", tc.name, response.Code, response.Body.String())
		}
		var payload struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &payload)
		if payload.Code != "invalid_base_revision" {
			t.Fatalf("%s: code=%q, want invalid_base_revision", tc.name, payload.Code)
		}
	}
	if got := revisionCount(t, f.runtime, plan.ID); got != 1 {
		t.Fatalf("rejected bases created revisions: %d", got)
	}
	stored, _ := f.planBase.GetPlan(plan.ID)
	if stored.Nodes[0].Inputs["prompt"] != "v1" {
		t.Fatalf("rejected bases changed the live graph: %v", stored.Nodes[0].Inputs["prompt"])
	}
}

func TestCreateRevisionStaleBaseKeepsLiveGraphAndRevisions(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionedPlan(t, f, "owner-a", "plan-stale")

	body := revisionPlanBody(plan)
	body["nodes"].([]map[string]any)[0]["inputs"] = map[string]any{"prompt": "v2"}
	first := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-stale/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})
	if first.Code != http.StatusOK {
		t.Fatalf("first create status=%d body=%s", first.Code, first.Body.String())
	}

	stale := revisionPlanBody(plan)
	stale["nodes"].([]map[string]any)[0]["inputs"] = map[string]any{"prompt": "stale-overwrite"}
	conflict := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-stale/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": stale})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", conflict.Code, conflict.Body.String())
	}
	var payload struct {
		Code            string `json:"code"`
		CurrentRevision int    `json:"current_revision"`
	}
	_ = json.Unmarshal(conflict.Body.Bytes(), &payload)
	if payload.Code != "revision_conflict" || payload.CurrentRevision != 2 {
		t.Fatalf("unexpected conflict payload: %+v", payload)
	}
	if got := revisionCount(t, f.runtime, plan.ID); got != 2 {
		t.Fatalf("stale base created a revision: count=%d", got)
	}
	stored, _ := f.planBase.GetPlan(plan.ID)
	if got := stored.Nodes[0].Inputs["prompt"]; got != "v2" {
		t.Fatalf("stale base overwrote the live graph: %v", got)
	}
}

// TestCreateRevisionTOCTOUStateRejectedBeforeRevisionIsCreated flips the plan
// into a non-editable state after authorizedPlan but before the mutation, then
// asserts no revision and no live graph change happened.
func TestCreateRevisionTOCTOUStateRejectedBeforeRevisionIsCreated(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(plan *models.PlanGraph)
		wantStatus int
		wantCode   string
	}{
		{name: "running", mutate: func(p *models.PlanGraph) { p.Status = models.StatusInProgress }, wantStatus: http.StatusConflict, wantCode: "plan_running"},
		{name: "awaiting_approval", mutate: func(p *models.PlanGraph) { p.Status = models.StatusAwaitingApproval }, wantStatus: http.StatusConflict, wantCode: "plan_running"},
		{name: "awaiting_input", mutate: func(p *models.PlanGraph) { p.Status = models.StatusAwaitingInput }, wantStatus: http.StatusConflict, wantCode: "plan_running"},
		{name: "paused", mutate: func(p *models.PlanGraph) { p.Paused = true }, wantStatus: http.StatusConflict, wantCode: "plan_paused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRevisionLockFixture(t)
			plan := seedRevisionedPlan(t, f, "owner-a", "plan-toctou-"+tc.name)
			wrapper := &switchingPlanStore{PlanStore: f.planBase, mutate: tc.mutate}
			f.useStore(t, wrapper)

			body := revisionPlanBody(plan)
			body["nodes"].([]map[string]any)[0]["inputs"] = map[string]any{"prompt": "should-not-apply"}
			response := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-toctou-"+tc.name+"/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})
			if response.Code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%s", tc.wantStatus, response.Code, response.Body.String())
			}
			var payload struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(response.Body.Bytes(), &payload)
			if payload.Code != tc.wantCode {
				t.Fatalf("code=%q, want %q", payload.Code, tc.wantCode)
			}
			if got := revisionCount(t, f.runtime, plan.ID); got != 1 {
				t.Fatalf("rejected edit created a revision: count=%d", got)
			}
			stored, _ := f.planBase.GetPlan(plan.ID)
			if got := stored.Nodes[0].Inputs["prompt"]; got != "v1" {
				t.Fatalf("rejected edit changed the live graph: %v", got)
			}
		})
	}
}

func TestReviewRevisionTOCTOUStateRejectedWithoutApprovingRevision(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(plan *models.PlanGraph)
		wantStatus int
		wantCode   string
	}{
		{name: "running", mutate: func(p *models.PlanGraph) { p.Status = models.StatusInProgress }, wantStatus: http.StatusConflict, wantCode: "plan_running"},
		{name: "awaiting_input", mutate: func(p *models.PlanGraph) { p.Status = models.StatusAwaitingInput }, wantStatus: http.StatusConflict, wantCode: "plan_running"},
		{name: "paused", mutate: func(p *models.PlanGraph) { p.Paused = true }, wantStatus: http.StatusConflict, wantCode: "plan_paused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRevisionLockFixture(t)
			plan := seedRevisionedPlan(t, f, "owner-a", "plan-review-"+tc.name)
			wrapper := &switchingPlanStore{PlanStore: f.planBase, mutate: tc.mutate}
			f.useStore(t, wrapper)

			response := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-review-"+tc.name+"/revisions/1/approve", "owner-a", map[string]any{})
			if response.Code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%s", tc.wantStatus, response.Code, response.Body.String())
			}
			var payload struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(response.Body.Bytes(), &payload)
			if payload.Code != tc.wantCode {
				t.Fatalf("code=%q, want %q", payload.Code, tc.wantCode)
			}
			rev, err := f.runtime.revisionStore().Get(plan.ID, 1)
			if err != nil {
				t.Fatal(err)
			}
			if rev.Status != models.RevisionDraft {
				t.Fatalf("rejected review changed the revision: %s", rev.Status)
			}
			stored, _ := f.planBase.GetPlan(plan.ID)
			if stored.Review.Status == models.ReviewApproved {
				t.Fatal("rejected review approved the live review gate")
			}
		})
	}
}

func TestReviewRevisionStaleLatestIsRejected(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionedPlan(t, f, "owner-a", "plan-review-stale")
	body := revisionPlanBody(plan)
	body["nodes"].([]map[string]any)[0]["inputs"] = map[string]any{"prompt": "v2"}
	if created := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-review-stale/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body}); created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	response := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-review-stale/revisions/1/approve", "owner-a", map[string]any{})
	if response.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code            string `json:"code"`
		CurrentRevision int    `json:"current_revision"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if payload.Code != "revision_not_latest" || payload.CurrentRevision != 2 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	rev, err := f.runtime.revisionStore().Get(plan.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rev.Status != models.RevisionDraft {
		t.Fatalf("non-latest revision was reviewed: %s", rev.Status)
	}
}

func TestExecuteRevisionTOCTOUStateRejectedWithoutResettingGraph(t *testing.T) {
	cases := []struct {
		name       string
		mutate     func(plan *models.PlanGraph)
		wantStatus int
		wantCode   string
	}{
		{name: "running", mutate: func(p *models.PlanGraph) { p.Status = models.StatusInProgress }, wantStatus: http.StatusConflict, wantCode: "plan_running"},
		{name: "awaiting_approval", mutate: func(p *models.PlanGraph) { p.Status = models.StatusAwaitingApproval }, wantStatus: http.StatusConflict, wantCode: "plan_running"},
		{name: "awaiting_input", mutate: func(p *models.PlanGraph) { p.Status = models.StatusAwaitingInput }, wantStatus: http.StatusConflict, wantCode: "plan_running"},
		{name: "paused", mutate: func(p *models.PlanGraph) { p.Paused = true }, wantStatus: http.StatusConflict, wantCode: "plan_paused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRevisionLockFixture(t)
			plan := seedRevisionedPlan(t, f, "owner-a", "plan-exec-"+tc.name)
			// Mark the live graph so a reset is detectable.
			if err := f.planBase.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
				current.Nodes[0].Status = models.StatusCompleted
				current.Nodes[0].Result = "kept result"
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			wrapper := &switchingPlanStore{PlanStore: f.planBase, mutate: tc.mutate}
			f.useStore(t, wrapper)
			before, err := f.planBase.GetPlan(plan.ID)
			if err != nil {
				t.Fatal(err)
			}

			response := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-exec-"+tc.name+"/revisions/1/execute", "owner-a", map[string]any{})
			if response.Code != tc.wantStatus {
				t.Fatalf("want %d, got %d body=%s", tc.wantStatus, response.Code, response.Body.String())
			}
			var payload struct {
				Code string `json:"code"`
			}
			_ = json.Unmarshal(response.Body.Bytes(), &payload)
			if payload.Code != tc.wantCode {
				t.Fatalf("code=%q, want %q", payload.Code, tc.wantCode)
			}
			stored, _ := f.planBase.GetPlan(plan.ID)
			if stored.Nodes[0].Status != models.StatusCompleted || stored.Nodes[0].Result != "kept result" {
				t.Fatalf("rejected execute reset the live graph: %+v", stored.Nodes[0])
			}
			if stored.CurrentRevision != before.CurrentRevision {
				t.Fatalf("rejected execute rebound revision %d -> %d", before.CurrentRevision, stored.CurrentRevision)
			}
			if len(stored.Nodes) != len(before.Nodes) {
				t.Fatalf("rejected execute replaced the node list: %d -> %d", len(before.Nodes), len(stored.Nodes))
			}
			if len(f.runtime.running) != 0 {
				t.Fatalf("rejected execute started a run: %v", f.runtime.running)
			}
		})
	}
}

// TestExecuteRevisionRefusesLaunchAcceptedElsewhere covers the case where the
// plan status is still pending but a scheduler run already owns the plan.
func TestExecuteRevisionRefusesLaunchAcceptedElsewhere(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionedPlan(t, f, "owner-a", "plan-accepted")
	before, err := f.planBase.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.runtime.mu.Lock()
	f.runtime.running[plan.ID] = func() {}
	f.runtime.mu.Unlock()
	defer func() {
		f.runtime.mu.Lock()
		delete(f.runtime.running, plan.ID)
		f.runtime.mu.Unlock()
	}()

	response := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-accepted/revisions/1/execute", "owner-a", map[string]any{})
	if response.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if payload.Code != "plan_running" {
		t.Fatalf("code=%q, want plan_running", payload.Code)
	}
	stored, _ := f.planBase.GetPlan(plan.ID)
	if stored.CurrentRevision != before.CurrentRevision || stored.Status != before.Status {
		t.Fatalf("refused execute changed the plan: %+v", stored)
	}
	for i, node := range stored.Nodes {
		if node.Status != before.Nodes[i].Status || node.Revision != before.Nodes[i].Revision ||
			node.ExecutionEpoch != before.Nodes[i].ExecutionEpoch || node.StartedAt != nil {
			t.Fatalf("refused execute touched node %d: %+v", i, node)
		}
	}
}

func TestCreateRevisionConcurrentSameBaseSavesOnlyOne(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionedPlan(t, f, "owner-a", "plan-concurrent")

	const attempts = 6
	codes := make(chan int, attempts)
	var wg sync.WaitGroup
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			body := revisionPlanBody(plan)
			body["nodes"].([]map[string]any)[0]["inputs"] = map[string]any{"prompt": fmt.Sprintf("race-%d", index)}
			response := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-concurrent/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})
			codes <- response.Code
		}(i)
	}
	wg.Wait()
	close(codes)

	ok, conflicts := 0, 0
	for code := range codes {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflicts++
		default:
			t.Fatalf("unexpected status %d", code)
		}
	}
	if ok != 1 || conflicts != attempts-1 {
		t.Fatalf("ok=%d conflicts=%d, want 1 and %d", ok, conflicts, attempts-1)
	}
	if got := revisionCount(t, f.runtime, plan.ID); got != 2 {
		t.Fatalf("concurrent saves produced %d revisions, want 2", got)
	}
}

func TestRevisionEventsCarrySeqAndMatchListEvents(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionedPlan(t, f, "owner-a", "plan-seq")

	body := revisionPlanBody(plan)
	body["nodes"].([]map[string]any)[0]["inputs"] = map[string]any{"prompt": "v2"}
	if created := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-seq/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body}); created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	if approved := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-seq/revisions/2/approve", "owner-a", map[string]any{}); approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}

	history, err := f.planBase.ListEvents(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int64{}
	for _, event := range history {
		if event.Seq <= 0 {
			t.Fatalf("persisted event without seq: %+v", event)
		}
		seen[event.EventType] = event.Seq
	}
	for _, want := range []string{"plan_revision_created", "plan_reviewed"} {
		if seen[want] <= 0 {
			t.Fatalf("missing event %q in %+v", want, history)
		}
	}
	// after_seq must never return an already-delivered event.
	afterCreated, err := f.planBase.ListEventsAfter(plan.ID, seen["plan_revision_created"])
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range afterCreated {
		if event.Seq <= seen["plan_revision_created"] {
			t.Fatalf("after_seq repeated seq=%d", event.Seq)
		}
	}
	if len(afterCreated) != 1 || afterCreated[0].EventType != "plan_reviewed" {
		t.Fatalf("after_seq replay=%+v, want only plan_reviewed", afterCreated)
	}
}

func TestRevisionEventPersistFailureIsReportedAndNotPublished(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionedPlan(t, f, "owner-a", "plan-eventfail")
	subscription := f.bus.Subscribe(plan.ID)
	defer f.bus.Unsubscribe(plan.ID, subscription)

	failing := &failingEventStore{PlanStore: f.planBase}
	f.useStore(t, failing)

	body := revisionPlanBody(plan)
	body["nodes"].([]map[string]any)[0]["inputs"] = map[string]any{"prompt": "v2"}
	response := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-eventfail/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 on event persist failure, got %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if payload.Code != "event_persist_failed" {
		t.Fatalf("code=%q, want event_persist_failed", payload.Code)
	}
	if failing.appendCount() == 0 {
		t.Fatal("event append was never attempted")
	}
	select {
	case event := <-subscription:
		t.Fatalf("failed append was published: %+v", event)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestPersistedEventPathsCarrySeqAndMatchHistory covers the other
// AppendEvent+Publish call sites (questions, runtime actions, bulk approvals):
// each must persist a numbered event that ListEvents agrees with.
func TestPersistedEventPathsCarrySeqAndMatchHistory(t *testing.T) {
	f := newRevisionLockFixture(t)
	plan := seedRevisionPlan(t, f.planBase, "owner-a", "plan-paths")

	created := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-paths/questions", "owner-a", map[string]any{
		"task_id": "plan-paths-a", "prompt": "continue?", "timeout_seconds": 600,
	})
	if created.Code != http.StatusOK {
		t.Fatalf("create question status=%d body=%s", created.Code, created.Body.String())
	}
	var question struct {
		Question models.PlanQuestion `json:"question"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &question)
	answered := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-paths/questions/"+question.Question.ID+"/answer", "owner-a", map[string]any{"answer": "yes"})
	if answered.Code != http.StatusOK {
		t.Fatalf("answer status=%d body=%s", answered.Code, answered.Body.String())
	}

	action := models.RuntimeActionRequest{ID: "ra-paths", PlanID: plan.ID, TaskID: "plan-paths-a", Kind: models.RuntimeActionNetworkAccess, Title: "network", Risk: "low", Status: models.RuntimeActionPending, RequestedAt: time.Now().UTC()}
	if err := f.planBase.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.RuntimeActions = append(current.RuntimeActions, action)
		current.Status = models.StatusAwaitingApproval
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	bulk := revisionRequest(f.router, http.MethodPost, "/api/approvals/bulk", "owner-a", map[string]any{"ids": []string{"ra-paths"}, "decision": "approve"})
	if bulk.Code != http.StatusOK {
		t.Fatalf("bulk status=%d body=%s", bulk.Code, bulk.Body.String())
	}
	_ = revisionRequest(f.router, http.MethodPost, "/api/plans/plan-paths/cancel", "owner-a", map[string]any{})

	history, err := f.planBase.ListEvents(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	byType := map[string]models.PlanEvent{}
	lastSeq := int64(0)
	for _, event := range history {
		if event.Seq <= 0 {
			t.Fatalf("persisted event without seq: %+v", event)
		}
		if event.Seq <= lastSeq {
			t.Fatalf("event seq not increasing: %d after %d", event.Seq, lastSeq)
		}
		lastSeq = event.Seq
		byType[event.EventType] = event
	}
	for _, want := range []string{"question_answered", "approval_bulk_decided"} {
		if byType[want].Seq <= 0 {
			t.Fatalf("missing or unnumbered event %q in %+v", want, history)
		}
	}
	// Incremental replay from the first event's seq returns exactly the rest,
	// without repeating the cursor event.
	incremental, err := f.planBase.ListEventsAfter(plan.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range incremental {
		if event.Seq <= 1 {
			t.Fatalf("after_seq=1 repeated seq=%d", event.Seq)
		}
	}
	if len(incremental) != len(history)-1 {
		t.Fatalf("incremental replay returned %d of %d events", len(incremental), len(history)-1)
	}
}

// TestQuestionAnswerEventPersistFailureIsNotReportedAsSuccess guards the
// question path: a lost event must not answer 200.
func TestQuestionAnswerEventPersistFailureIsNotReportedAsSuccess(t *testing.T) {
	f := newRevisionLockFixture(t)
	seedRevisionPlan(t, f.planBase, "owner-a", "plan-qfail")
	created := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-qfail/questions", "owner-a", map[string]any{
		"task_id": "plan-qfail-a", "prompt": "continue?", "timeout_seconds": 600,
	})
	if created.Code != http.StatusOK {
		t.Fatalf("create question status=%d body=%s", created.Code, created.Body.String())
	}
	var question struct {
		Question models.PlanQuestion `json:"question"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &question)

	f.useStore(t, &failingEventStore{PlanStore: f.planBase})
	response := revisionRequest(f.router, http.MethodPost, "/api/plans/plan-qfail/questions/"+question.Question.ID+"/answer", "owner-a", map[string]any{"answer": "yes"})
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if payload.Code != "event_persist_failed" {
		t.Fatalf("code=%q, want event_persist_failed", payload.Code)
	}
}
