package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"research-assistant-backend/internal/budget"
	"research-assistant-backend/internal/events"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/project"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
)

func newUsageRouter(t *testing.T) (*gin.Engine, store.PlanStore, *budget.Manager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	manager := budget.NewManager(budget.NewMemoryStore())
	eventBus := events.NewBus()
	runtimeInstance := newPlanRuntimeWithStores(
		planner.NewPlanner(),
		planStore,
		project.NewMemoryStore(),
		manager,
		scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), eventBus, 1),
		eventBus,
	)
	router := gin.New()
	runtimeInstance.register(router.Group("/api"))
	return router, planStore, manager
}

func TestPlanUsageEndpointAggregatesNodesAndModels(t *testing.T) {
	router, planStore, _ := newUsageRouter(t)
	plan := &models.PlanGraph{
		ID: "usage-plan", OwnerID: "owner-a", Status: models.StatusCompleted, UserIntent: "cost", IntentType: "General",
		Artifacts: map[string]models.Artifact{}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Usage: models.RunUsage{LLMPromptTokens: 300, LLMCompletionTokens: 120, LLMCalls: 3, LLMCostUSD: 0.42, CostSource: "profile"},
		Nodes: []*models.TaskNode{
			{ID: "n1", Name: "first", Type: "generate_code", Status: models.StatusCompleted, Usage: &models.TaskUsage{
				LLMPromptTokens: 200, LLMCompletionTokens: 80, LLMCalls: 2, LLMCostUSD: 0.3, CostSource: "profile",
				ModelBreakdown: []models.ModelUsage{{Model: "deepseek-chat", Calls: 2, PromptTokens: 200, CompletionTokens: 80, CostUSD: 0.3, CostSource: "profile"}},
			}},
			{ID: "n2", Name: "second", Type: "verify_result", Status: models.StatusCompleted, Usage: &models.TaskUsage{
				LLMPromptTokens: 100, LLMCompletionTokens: 40, LLMCalls: 1, LLMCostUSD: 0.12, CostSource: "env",
				ModelBreakdown: []models.ModelUsage{{Model: "deepseek-reasoner", Calls: 1, PromptTokens: 100, CompletionTokens: 40, CostUSD: 0.12, CostSource: "env"}},
			}},
		},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	response := libraryRequest(router, http.MethodGet, "/api/plans/usage-plan/usage", "owner-a", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Usage  models.RunUsage  `json:"usage"`
		Nodes  []planUsageNode  `json:"nodes"`
		Models []planUsageModel `json:"models"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Usage.LLMCostUSD != 0.42 || len(payload.Nodes) != 2 || len(payload.Models) != 2 {
		t.Fatalf("bad usage payload: %#v", payload)
	}
	if payload.Models[0].Model != "deepseek-chat" || payload.Models[0].CostUSD != 0.3 {
		t.Fatalf("models not sorted: %#v", payload.Models)
	}
}

func TestUsageSummaryAndBudgetEndpoints(t *testing.T) {
	router, _, manager := newUsageRouter(t)
	if _, err := manager.SetBudget("owner-a", "day", 5, "soft"); err != nil {
		t.Fatal(err)
	}
	manager.Record(budget.Entry{OwnerID: "owner-a", PlanID: "p1", Model: "m1", Prompt: 100, Completion: 50, CostUSD: 0.25, At: time.Now().UTC()})
	summary := libraryRequest(router, http.MethodGet, "/api/usage/summary?period=day", "owner-a", nil)
	if summary.Code != http.StatusOK {
		t.Fatalf("summary status=%d body=%s", summary.Code, summary.Body.String())
	}
	var summaryBody struct {
		Summary budget.Summary `json:"summary"`
	}
	if err := json.Unmarshal(summary.Body.Bytes(), &summaryBody); err != nil {
		t.Fatal(err)
	}
	if summaryBody.Summary.SpentUSD != 0.25 || summaryBody.Summary.LimitUSD != 5 || summaryBody.Summary.RemainingUSD != 4.75 {
		t.Fatalf("summary=%#v", summaryBody.Summary)
	}
	budgetResponse := libraryRequest(router, http.MethodGet, "/api/budgets", "owner-a", nil)
	if budgetResponse.Code != http.StatusOK || !bytes.Contains(budgetResponse.Body.Bytes(), []byte("\"limit_usd\":5")) {
		t.Fatalf("get budget status=%d body=%s", budgetResponse.Code, budgetResponse.Body.String())
	}
	put := libraryRequest(router, http.MethodPut, "/api/budgets", "owner-a", []byte(`{"period":"month","limit_usd":20,"policy":"hard"}`))
	if put.Code != http.StatusOK {
		t.Fatalf("put budget status=%d body=%s", put.Code, put.Body.String())
	}
	timeseries := libraryRequest(router, http.MethodGet, "/api/usage/timeseries?group=model", "owner-a", nil)
	if timeseries.Code != http.StatusOK || !bytes.Contains(timeseries.Body.Bytes(), []byte("m1")) {
		t.Fatalf("timeseries status=%d body=%s", timeseries.Code, timeseries.Body.String())
	}
	alerts := libraryRequest(router, http.MethodGet, "/api/budgets/alerts", "owner-a", nil)
	if alerts.Code != http.StatusOK {
		t.Fatalf("alerts status=%d body=%s", alerts.Code, alerts.Body.String())
	}
}

func TestUsageSummaryIsOwnerScoped(t *testing.T) {
	router, _, manager := newUsageRouter(t)
	manager.Record(budget.Entry{OwnerID: "owner-a", PlanID: "p1", CostUSD: 1, At: time.Now().UTC()})
	manager.Record(budget.Entry{OwnerID: "owner-b", PlanID: "p2", CostUSD: 9, At: time.Now().UTC()})
	response := libraryRequest(router, http.MethodGet, "/api/usage/summary", "owner-a", nil)
	if bytes.Contains(response.Body.Bytes(), []byte("p2")) {
		t.Fatalf("usage leaked another owner's plan: %s", response.Body.String())
	}
}
