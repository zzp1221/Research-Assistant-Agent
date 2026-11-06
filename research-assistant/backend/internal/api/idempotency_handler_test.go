package api

import (
	"bytes"
	"context"
	"errors"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/store"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type countingPlanBuilder struct {
	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	build   func(context.Context, models.IntentContext, planner.RouteDecision) (*models.PlanGraph, error)
}

func (b *countingPlanBuilder) BuildPlanWithRoute(ctx context.Context, i models.IntentContext, r planner.RouteDecision) (*models.PlanGraph, error) {
	if b.calls.Add(1) == 1 && b.started != nil {
		close(b.started)
	}
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return b.build(ctx, i, r)
}

type countingPlanStore struct {
	store.PlanStore
	saves     atomic.Int32
	saveError error
	savePanic bool
}

func (s *countingPlanStore) SavePlan(p *models.PlanGraph) error {
	s.saves.Add(1)
	if s.savePanic {
		panic("save interrupted")
	}
	if s.saveError != nil {
		return s.saveError
	}
	return s.PlanStore.SavePlan(p)
}
func signedPlanRequest(ctx context.Context, body, key, owner, session string) *http.Request {
	req := httptest.NewRequest("POST", "/api/plan", bytes.NewBufferString(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	req.AddCookie(&http.Cookie{Name: anonUserCookieName, Value: signIdentityCookie(owner)})
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: signIdentityCookie(session)})
	return req
}
func signedPost(router *gin.Engine, body, key, owner string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	router.ServeHTTP(w, signedPlanRequest(context.Background(), body, key, owner, "session-1"))
	return w
}
func concurrentRuntime(t *testing.T) (*planRuntime, *gin.Engine, *countingPlanBuilder, *countingPlanStore) {
	t.Helper()
	t.Setenv("PLANNER_AGENT_ENABLED", "false")
	r, router := newHardeningRuntime(t)
	actual := r.planner
	builder := &countingPlanBuilder{started: make(chan struct{}), release: make(chan struct{}), build: actual.BuildPlanWithRoute}
	saved := &countingPlanStore{PlanStore: r.store}
	r.planner = builder
	r.store = saved
	return r, router, builder, saved
}
func TestCreatePlanConcurrentSignedIdentity(t *testing.T) {
	r, router, builder, saved := concurrentRuntime(t)
	r.planLimiter = newRateLimiter(1, 1)
	const body = `{"intent":"hello","require_review":true}`
	responses := make(chan *httptest.ResponseRecorder, 16)
	for range 16 {
		go func() { responses <- signedPost(router, body, "concurrent", "stable-owner") }()
	}
	<-builder.started
	changes := []string{`"intent":"changed"`, `"attachments":["upload-1"]`, `"runtime_permission_mode":"full_access","runtime_permission_acknowledged":true`, `"max_cost_usd":1`, `"max_tokens":1`, `"max_sandbox_seconds":1`, `"max_download_bytes":1`, `"budget_policy":"hard"`, `"cost_policy":"budget_first"`, `"project_id":"other"`, `"session_id":"other"`, `"require_review":false`}
	for _, extra := range changes {
		changed := `{"intent":"hello","require_review":true,` + extra + `}`
		w := signedPost(router, changed, "concurrent", "stable-owner")
		if w.Code != 409 {
			t.Fatalf("in-progress conflict %s: %d %s", extra, w.Code, w.Body.String())
		}
	}
	close(builder.release)
	var first []byte
	for range 16 {
		w := <-responses
		if w.Code != 200 {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
		if first == nil {
			first = bytes.Clone(w.Body.Bytes())
		} else if !bytes.Equal(first, w.Body.Bytes()) {
			t.Fatal("different response snapshots")
		}
	}
	if builder.calls.Load() != 1 || saved.saves.Load() != 1 {
		t.Fatal("duplicate work", builder.calls.Load(), saved.saves.Load())
	}
	plans, _ := saved.ListPlans()
	if len(plans) != 1 {
		t.Fatal("duplicate stored plans", len(plans))
	}
	events, _ := saved.ListEvents(plans[0].ID)
	if len(events) != 1 || events[0].EventType != "plan_created" {
		t.Fatal("side effects", events)
	}
	replay := signedPost(router, body, "concurrent", "stable-owner")
	if replay.Code != 200 || replay.Header().Get("Idempotency-Replayed") != "true" || !bytes.Equal(first, replay.Body.Bytes()) {
		t.Fatal("replay used creation quota")
	}
	if w := signedPost(router, body, "new-key", "stable-owner"); w.Code != 429 {
		t.Fatal("new creation not limited", w.Code)
	}
	if w := signedPost(router, `{"intent":"different"}`, "concurrent", "stable-owner"); w.Code != 409 {
		t.Fatal("completed conflict", w.Code)
	}
	for _, extra := range changes {
		w := signedPost(router, `{"intent":"hello","require_review":true,`+extra+`}`, "concurrent", "stable-owner")
		if w.Code != 409 {
			t.Fatalf("completed conflict %s: %d", extra, w.Code)
		}
	}
	// Omitted session remains stable even after the browser changes its cookie.
	req := signedPlanRequest(t.Context(), body, "concurrent", "stable-owner", "changed-session")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if !bytes.Equal(first, w.Body.Bytes()) {
		t.Fatal("session cookie changed original snapshot")
	}
}
func TestCreatePlanIndependentOwnersAndKeys(t *testing.T) {
	r, router, builder, saved := concurrentRuntime(t)
	r.planLimiter = newRateLimiter(100, 100)
	release := builder.release
	builder.release = nil
	var wg sync.WaitGroup
	for _, pair := range [][2]string{{"a", "one"}, {"a", "two"}, {"b", "one"}} {
		wg.Add(1)
		go func(owner, key string) {
			defer wg.Done()
			w := signedPost(router, `{"intent":"hello"}`, key, owner)
			if w.Code != 200 {
				t.Errorf("status=%d", w.Code)
			}
		}(pair[0], pair[1])
	}
	wg.Wait()
	close(release)
	if saved.saves.Load() != 3 {
		t.Fatal("cross owner/key replay")
	}
}
func TestCreatePlanWaitCancellationAndTimeout(t *testing.T) {
	r, router, builder, saved := concurrentRuntime(t)
	r.idempotency.waitTimeout = time.Millisecond
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- signedPost(router, `{"intent":"hello"}`, "key", "owner") }()
	<-builder.started
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, signedPlanRequest(ctx, `{"intent":"hello"}`, "key", "owner", "session"))
	w = signedPost(router, `{"intent":"hello"}`, "key", "owner")
	if w.Code != 503 || w.Header().Get("Retry-After") != "1" || !bytes.Contains(w.Body.Bytes(), []byte("idempotency_in_progress")) {
		t.Fatal(w.Code, w.Body.String())
	}
	close(builder.release)
	if w := <-done; w.Code != 200 {
		t.Fatal("waiter canceled executor")
	}
	if saved.saves.Load() != 1 {
		t.Fatal("duplicate save")
	}
}
func TestCreatePlanFailureRetryAndPanic(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "failure", true: "panic"}[panics], func(t *testing.T) {
			r, router, builder, saved := concurrentRuntime(t)
			builder.release = nil
			actual := builder.build
			builder.build = func(context.Context, models.IntentContext, planner.RouteDecision) (*models.PlanGraph, error) {
				if panics {
					panic("planner panic")
				}
				return nil, errors.New("planner failed")
			}
			if panics {
				func() {
					defer func() {
						if recover() == nil {
							t.Error("missing panic")
						}
					}()
					signedPost(router, `{"intent":"hello"}`, "key", "owner")
				}()
			} else {
				if w := signedPost(router, `{"intent":"hello"}`, "key", "owner"); w.Code != 500 {
					t.Fatal(w.Code)
				}
			}
			r.idempotency.mu.Lock()
			n := len(r.idempotency.entries)
			r.idempotency.mu.Unlock()
			if n != 0 {
				t.Fatal("failed handle leaked")
			}
			builder.build = actual
			if w := signedPost(router, `{"intent":"hello"}`, "key", "owner"); w.Code != 200 {
				t.Fatal("retry failed", w.Code, w.Body.String())
			}
			if saved.saves.Load() != 1 {
				t.Fatal("unexpected saves")
			}
		})
	}
}
func TestCreatePlanCommitUnknownAndSavePanic(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "error", true: "panic"}[panics], func(t *testing.T) {
			_, router, builder, saved := concurrentRuntime(t)
			builder.release = nil
			saved.saveError = errors.New("disk error")
			saved.savePanic = panics
			if panics {
				func() { defer func() { recover() }(); signedPost(router, `{"intent":"hello"}`, "key", "owner") }()
			} else {
				w := signedPost(router, `{"intent":"hello"}`, "key", "owner")
				if w.Code != 503 {
					t.Fatal(w.Code)
				}
			}
			w := signedPost(router, `{"intent":"hello"}`, "key", "owner")
			if w.Code != 503 || !bytes.Contains(w.Body.Bytes(), []byte("idempotency_commit_unknown")) || !bytes.Contains(w.Body.Bytes(), []byte("plan_id")) {
				t.Fatal(w.Code, w.Body.String())
			}
			if builder.calls.Load() != 1 || saved.saves.Load() != 1 {
				t.Fatal("retried uncertain save")
			}
		})
	}
}

type disconnectedWriter struct{ *httptest.ResponseRecorder }

func (w disconnectedWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestCreatePlanSavedResponseDisconnect(t *testing.T) {
	_, router, builder, saved := concurrentRuntime(t)
	builder.release = nil
	router.ServeHTTP(disconnectedWriter{httptest.NewRecorder()}, signedPlanRequest(t.Context(), `{"intent":"hello"}`, "key", "owner", "session"))
	if w := signedPost(router, `{"intent":"hello"}`, "key", "owner"); w.Code != 200 || w.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal(w.Code)
	}
	if saved.saves.Load() != 1 {
		t.Fatal("disconnect recreated plan")
	}
}

func TestCreatePlanCanonicalRequestAndCanceledExecutor(t *testing.T) {
	_, router, builder, saved := concurrentRuntime(t)
	builder.release = nil
	first := signedPost(router, `{"intent":" hello ","require_review":true}`, "canonical", "owner")
	second := signedPost(router, ` { "runtime_permission_mode":" confirm_important ","require_review":true,"intent":"hello" }`, "canonical", "owner")
	if first.Code != 200 || second.Code != 200 || !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Fatal("canonical replay", first.Code, second.Code)
	}
	if builder.calls.Load() != 1 || saved.saves.Load() != 1 {
		t.Fatal("canonical request executed twice")
	}

	r, router, builder, saved := concurrentRuntime(t)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, signedPlanRequest(ctx, `{"intent":"hello"}`, "cancel", "owner", "session"))
		done <- w
	}()
	<-builder.started
	cancel()
	<-done
	if saved.saves.Load() != 0 {
		t.Fatal("saved canceled plan")
	}
	r.idempotency.mu.Lock()
	entries := len(r.idempotency.entries)
	r.idempotency.mu.Unlock()
	if entries != 0 {
		t.Fatal("canceled executor leaked handle")
	}
	close(builder.release)
	if w := signedPost(router, `{"intent":"hello"}`, "cancel", "owner"); w.Code != 200 {
		t.Fatal("canceled submission not retryable", w.Code)
	}
}
