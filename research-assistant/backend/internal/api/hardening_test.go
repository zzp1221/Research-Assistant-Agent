package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"research-assistant-backend/internal/events"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
)

func newHardeningRuntime(t *testing.T) (*planRuntime, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	eventBus := events.NewBus()
	agentRunner := &completingAgentRunner{}
	executor := scheduler.NewRoutedTaskExecutor(agentRunner, agentRunner, agentRunner)
	planScheduler := scheduler.NewScheduler(planStore, executor, eventBus, 2)
	runtime := newPlanRuntime(planner.NewPlanner(), planStore, planScheduler, eventBus)
	router := gin.New()
	router.Use(metricsMiddleware())
	runtime.register(router.Group("/api"))
	return runtime, router
}

func postPlan(router *gin.Engine, body, user, idempotencyKey string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/plan", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(userIDHeaderName, user)
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestCreatePlanRejectsOversizedIntent(t *testing.T) {
	_, router := newHardeningRuntime(t)
	huge := strings.Repeat("a", 300000)
	response := postPlan(router, `{"intent":"`+huge+`"}`, "oversize-user", "")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "intent_too_long") {
		t.Fatalf("expected intent_too_long, body=%s", response.Body.String())
	}
}

func TestCreatePlanRateLimited(t *testing.T) {
	runtime, router := newHardeningRuntime(t)
	runtime.planLimiter = newRateLimiter(1, 1)
	first := postPlan(router, `{"intent":"你好，介绍一下你能做什么"}`, "rate-user", "")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	second := postPlan(router, `{"intent":"你好，介绍一下你能做什么"}`, "rate-user", "")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if second.Header().Get("Retry-After") == "" {
		t.Fatal("429 must include Retry-After")
	}
}

func TestCreatePlanIdempotencyKeyReturnsSamePlan(t *testing.T) {
	_, router := newHardeningRuntime(t)
	body := `{"intent":"你好，简单介绍一下你能做什么"}`
	first := postPlan(router, body, "idem-user", "key-1")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var firstPayload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstPayload); err != nil {
		t.Fatal(err)
	}
	second := postPlan(router, body, "idem-user", "key-1")
	if second.Code != http.StatusOK {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatal("replayed response must be marked")
	}
	var secondPayload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondPayload); err != nil {
		t.Fatal(err)
	}
	if firstPayload.PlanGraph.ID == "" || firstPayload.PlanGraph.ID != secondPayload.PlanGraph.ID {
		t.Fatalf("idempotency returned different plans: %q vs %q", firstPayload.PlanGraph.ID, secondPayload.PlanGraph.ID)
	}
}

func TestCreatePlanRejectsWhenActiveQuotaExceeded(t *testing.T) {
	t.Setenv("PLAN_MAX_ACTIVE_PER_USER", "1")
	runtime, router := newHardeningRuntime(t)
	first := postPlan(router, `{"intent":"你好，简单介绍一下你能做什么"}`, "quota-user", "")
	if first.Code != http.StatusOK {
		t.Fatalf("first status=%d body=%s", first.Code, first.Body.String())
	}
	var payload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if err := runtime.store.UpdatePlan(payload.PlanGraph.ID, func(plan *models.PlanGraph) error {
		plan.Status = models.StatusInProgress
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	second := postPlan(router, `{"intent":"再问一个问题"}`, "quota-user", "")
	if second.Code != http.StatusTooManyRequests || !strings.Contains(second.Body.String(), "active_plan_quota_exceeded") {
		t.Fatalf("status=%d body=%s", second.Code, second.Body.String())
	}
}

func TestHealthHidesInternalURLs(t *testing.T) {
	health := buildServiceHealth(t.Context(), "http://127.0.0.1:1", "")
	if health["version"] == nil || health["version"] == "" {
		t.Fatalf("health must expose a version: %#v", health)
	}
	sandbox, ok := health["sandbox"].(gin.H)
	if !ok {
		t.Fatalf("sandbox health missing: %#v", health["sandbox"])
	}
	if _, exists := sandbox["url"]; exists {
		t.Fatalf("sandbox health must not expose its internal URL: %#v", sandbox)
	}
}

func TestMetricsMiddlewareRecordsRequests(t *testing.T) {
	metrics.Default().Reset()
	_, router := newHardeningRuntime(t)
	request := httptest.NewRequest(http.MethodGet, "/api/plans", nil)
	request.Header.Set(userIDHeaderName, "metrics-user")
	router.ServeHTTP(httptest.NewRecorder(), request)
	value := metrics.Default().CounterValue("http_requests_total", metrics.Labels{
		"method": "GET", "path": "/api/plans", "status": "200",
	})
	if value != 1 {
		t.Fatalf("http_requests_total=%v", value)
	}
}

func TestPauseBlocksExecutionUntilResumed(t *testing.T) {
	runtime, router := newHardeningRuntime(t)
	created := postPlan(router, `{"intent":"你好，简单介绍一下你能做什么"}`, "pause-user", "")
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var payload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	planID := payload.PlanGraph.ID
	pause := httptest.NewRequest(http.MethodPost, "/api/plans/"+planID+"/pause", nil)
	pause.Header.Set(userIDHeaderName, "pause-user")
	pauseResponse := httptest.NewRecorder()
	router.ServeHTTP(pauseResponse, pause)
	if pauseResponse.Code != http.StatusOK {
		t.Fatalf("pause status=%d body=%s", pauseResponse.Code, pauseResponse.Body.String())
	}
	plan, err := runtime.store.GetPlan(planID)
	if err != nil || !plan.Paused {
		t.Fatalf("plan not paused: %#v err=%v", plan, err)
	}
	execute := httptest.NewRequest(http.MethodPost, "/api/plans/"+planID+"/execute", nil)
	execute.Header.Set(userIDHeaderName, "pause-user")
	executeResponse := httptest.NewRecorder()
	router.ServeHTTP(executeResponse, execute)
	if executeResponse.Code != http.StatusConflict || !strings.Contains(executeResponse.Body.String(), "plan_paused") {
		t.Fatalf("execute while paused status=%d body=%s", executeResponse.Code, executeResponse.Body.String())
	}
	resume := httptest.NewRequest(http.MethodPost, "/api/plans/"+planID+"/resume", nil)
	resume.Header.Set(userIDHeaderName, "pause-user")
	resumeResponse := httptest.NewRecorder()
	router.ServeHTTP(resumeResponse, resume)
	if resumeResponse.Code != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", resumeResponse.Code, resumeResponse.Body.String())
	}
	plan, err = runtime.store.GetPlan(planID)
	if err != nil || plan.Paused {
		t.Fatalf("plan still paused: %#v err=%v", plan, err)
	}
}

func TestRunFromNodeRequiresOwnership(t *testing.T) {
	_, router := newHardeningRuntime(t)
	created := postPlan(router, `{"intent":"你好，简单介绍一下你能做什么"}`, "owner-user", "")
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d", created.Code)
	}
	var payload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	taskID := payload.PlanGraph.Nodes[0].ID
	request := httptest.NewRequest(http.MethodPost, "/api/plans/"+payload.PlanGraph.ID+"/tasks/"+taskID+"/run", nil)
	request.Header.Set(userIDHeaderName, "other-user")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("cross-user rewind status=%d body=%s", response.Code, response.Body.String())
	}
}
