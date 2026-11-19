package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"
)

type requestFaultPlanStore struct {
	store.PlanStore
	healthErr error
	getErr    error
	getAfter  int
	getCalls  int
	listErr   error
	eventsErr error
	deleteErr error
	updateErr error
	appendErr error
}

func (s *requestFaultPlanStore) Health() error { return s.healthErr }
func (s *requestFaultPlanStore) GetPlan(id string) (*models.PlanGraph, error) {
	s.getCalls++
	if s.getErr != nil && s.getCalls > s.getAfter {
		return nil, s.getErr
	}
	return s.PlanStore.GetPlan(id)
}
func (s *requestFaultPlanStore) ListPlans() ([]*models.PlanGraph, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.PlanStore.ListPlans()
}
func (s *requestFaultPlanStore) ListEventsAfter(id string, after int64) ([]models.PlanEvent, error) {
	if s.eventsErr != nil {
		return nil, s.eventsErr
	}
	return s.PlanStore.ListEventsAfter(id, after)
}
func (s *requestFaultPlanStore) DeletePlan(id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.PlanStore.DeletePlan(id)
}
func (s *requestFaultPlanStore) UpdatePlan(id string, update func(*models.PlanGraph) error) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	return s.PlanStore.UpdatePlan(id, update)
}
func (s *requestFaultPlanStore) AppendEventReturning(id string, event models.PlanEvent) (models.PlanEvent, error) {
	if s.appendErr != nil {
		return models.PlanEvent{}, s.appendErr
	}
	return s.PlanStore.AppendEventReturning(id, event)
}

func TestPlanStoreRequestErrorsAreUnavailableInsteadOfNotFound(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		configure                func(*requestFaultPlanStore)
	}{
		{"read", "GET", "/api/plans/storage-boundary", "", func(s *requestFaultPlanStore) { s.getErr = errors.New("disk unavailable") }},
		{"list", "GET", "/api/plans", "", func(s *requestFaultPlanStore) { s.listErr = errors.New("disk unavailable") }},
		{"sessions", "GET", "/api/sessions", "", func(s *requestFaultPlanStore) { s.listErr = errors.New("disk unavailable") }},
		{"events", "GET", "/api/plans/storage-boundary/events", "", func(s *requestFaultPlanStore) { s.eventsErr = errors.New("disk unavailable") }},
		{"stream-before-first-event", "GET", "/api/plans/storage-boundary/stream", "", func(s *requestFaultPlanStore) { s.eventsErr = store.ErrStoreRecoveryRequired }},
		{"delete", "DELETE", "/api/plans/storage-boundary", "", func(s *requestFaultPlanStore) { s.deleteErr = store.ErrNotCommitted }},
		{"second-locked-read", "POST", "/api/plans/storage-boundary/execute", "{}", func(s *requestFaultPlanStore) { s.getErr, s.getAfter = store.ErrStoreRecoveryRequired, 1 }},
		{"export-history", "GET", "/api/plans/storage-boundary/export?format=md", "", func(s *requestFaultPlanStore) { s.eventsErr = store.ErrStoreRecoveryRequired }},
		{"mutation", "POST", "/api/plans/storage-boundary/pause", "{}", func(s *requestFaultPlanStore) { s.updateErr = fmt.Errorf("%w: disk full", store.ErrNotCommitted) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runtime, router := newHardeningRuntime(t)
			plan := &models.PlanGraph{ID: "storage-boundary", OwnerID: "storage-owner", Status: models.StatusPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := runtime.store.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			fault := &requestFaultPlanStore{PlanStore: runtime.store}
			tc.configure(fault)
			runtime.store = fault
			runtime.scheduler = scheduler.NewScheduler(fault, nil, runtime.events, 1)
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set(userIDHeaderName, plan.OwnerID)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "store_") {
				t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Header().Get("Content-Type"), "text/event-stream") {
				t.Fatal("failed initial history returned SSE instead of 503 JSON")
			}
		})
	}
}

func TestPlanStoreMissingPlanRemainsNotFound(t *testing.T) {
	_, router := newHardeningRuntime(t)
	req := httptest.NewRequest("GET", "/api/plans/absent", nil)
	req.Header.Set(userIDHeaderName, "storage-owner")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "plan_not_found") {
		t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
	}
}

func TestPlanStoreSealedInstanceRejectsBeforeHandler(t *testing.T) {
	runtime, router := newHardeningRuntime(t)
	fault := &requestFaultPlanStore{PlanStore: runtime.store, healthErr: store.ErrStoreRecoveryRequired}
	runtime.store = fault
	for _, path := range []string{"/api/plans", "/api/plans/absent", "/api/plans/absent/events"} {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "store_recovery_required") {
			t.Fatalf("response=%d %s", rec.Code, rec.Body.String())
		}
	}
	if fault.getCalls != 0 {
		t.Fatal("sealed instance reached the plan handler")
	}
}

func TestPlanStoreEventErrorKeepsTypedCommitCause(t *testing.T) {
	runtime, _ := newHardeningRuntime(t)
	fault := &requestFaultPlanStore{PlanStore: runtime.store, appendErr: &store.CommitError{Operation: "append", PlanID: "p", Cause: errors.New("rollback failed")}}
	runtime.store = fault
	err := runtime.persistAndPublishPlanEvent("p", models.PlanEvent{PlanID: "p", EventType: "test"})
	if !errors.Is(err, store.ErrCommitUnknown) || !errors.Is(err, errEventPersistence) {
		t.Fatalf("typed cause was lost: %v", err)
	}
}

func TestPlanStoreCreatedPlanEventFailureKeepsIdempotencyKey(t *testing.T) {
	for _, cause := range []error{store.ErrNotCommitted, store.ErrCommitUnknown} {
		t.Run(cause.Error(), func(t *testing.T) {
			_, router, builder, saved := concurrentRuntime(t)
			builder.release = nil
			saved.PlanStore = &requestFaultPlanStore{PlanStore: saved.PlanStore, appendErr: cause}
			first := signedPost(router, `{"intent":"hello"}`, "event-failure-key", "event-owner")
			second := signedPost(router, `{"intent":"hello"}`, "event-failure-key", "event-owner")
			if first.Code != 503 || second.Code != 503 || !strings.Contains(first.Body.String(), "plan_id") {
				t.Fatalf("first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
			}
			if first.Body.String() != second.Body.String() {
				t.Fatal("committed plan's failure was not replayed")
			}
			if builder.calls.Load() != 1 || saved.saves.Load() != 1 {
				t.Fatal("event failure recreated an already committed plan")
			}
		})
	}
}
