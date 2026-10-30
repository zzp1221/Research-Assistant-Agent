package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"research-assistant-backend/internal/events"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/project"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
)

func seedLibraryPlan(t *testing.T, planStore store.PlanStore, owner, id, title, status, intentType, projectID string, archived bool, updatedAt time.Time, cost float64) {
	t.Helper()
	nodes := []*models.TaskNode{
		{ID: id + "-n1", Name: "node1", Type: "generate_code", Status: models.StatusCompleted},
		{ID: id + "-n2", Name: "node2", Type: "verify_result", Status: models.StatusPending},
	}
	plan := &models.PlanGraph{
		ID:         id,
		Title:      title,
		UserIntent: "intent for " + title,
		IntentType: intentType,
		Status:     models.TaskStatus(status),
		OwnerID:    owner,
		ProjectID:  projectID,
		Archived:   archived,
		Nodes:      nodes,
		Edges:      []*models.TaskEdge{},
		Artifacts:  map[string]models.Artifact{},
		Usage:      models.RunUsage{LLMCostUSD: cost},
		CreatedAt:  updatedAt.Add(-time.Hour),
		UpdatedAt:  updatedAt,
		Meta:       models.GraphMeta{TotalNodes: 2, CompletedNodes: 1},
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatalf("seed plan %s: %v", id, err)
	}
}

func newLibraryRouter(t *testing.T) (*gin.Engine, store.PlanStore, project.Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	projectStore := project.NewMemoryStore()
	eventBus := events.NewBus()
	runtime := newPlanRuntimeWithProjects(
		planner.NewPlanner(),
		planStore,
		projectStore,
		scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), eventBus, 1),
		eventBus,
	)
	router := gin.New()
	runtime.register(router.Group("/api"))
	return router, planStore, projectStore
}

func libraryRequest(router *gin.Engine, method, path, userID string, body []byte) *httptest.ResponseRecorder {
	var request *http.Request
	if body != nil {
		request = httptest.NewRequest(method, path, bytes.NewBuffer(body))
		request.Header.Set("Content-Type", "application/json")
	} else {
		request = httptest.NewRequest(method, path, nil)
	}
	request.Header.Set(userIDHeaderName, userID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestPlanLibraryPaginatesOneThousandPlansWithoutDuplicates(t *testing.T) {
	router, planStore, _ := newLibraryRouter(t)
	base := time.Now().UTC().Add(-1000 * time.Minute)
	for index := 0; index < 1000; index++ {
		id := fmt.Sprintf("plan-%04d", index)
		seedLibraryPlan(t, planStore, "owner-a", id, "plan "+id, string(models.StatusCompleted), "General", "", false, base.Add(time.Duration(index)*time.Minute), float64(index)/100)
	}
	started := time.Now()
	first := libraryRequest(router, http.MethodGet, "/api/plans?limit=20", "owner-a", nil)
	elapsed := time.Since(started)
	if first.Code != http.StatusOK {
		t.Fatalf("first page status=%d body=%s", first.Code, first.Body.String())
	}
	if elapsed > time.Second {
		t.Fatalf("first page took %s, want <1s", elapsed)
	}
	var page struct {
		Plans      []planSummary `json:"plans"`
		Total      int           `json:"total"`
		NextCursor string        `json:"next_cursor"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1000 || len(page.Plans) != 20 || page.NextCursor == "" {
		t.Fatalf("page1 total=%d len=%d cursor=%q", page.Total, len(page.Plans), page.NextCursor)
	}
	seen := map[string]bool{}
	for _, item := range page.Plans {
		seen[item.ID] = true
	}
	cursor := page.NextCursor
	pages := 1
	for cursor != "" {
		response := libraryRequest(router, http.MethodGet, "/api/plans?limit=20&cursor="+cursor, "owner-a", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("page %d status=%d body=%s", pages+1, response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Plans {
			if seen[item.ID] {
				t.Fatalf("duplicate plan %s on page %d", item.ID, pages+1)
			}
			seen[item.ID] = true
		}
		cursor = page.NextCursor
		pages++
		if pages > 60 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) != 1000 {
		t.Fatalf("saw %d plans, want 1000", len(seen))
	}
}

func TestPlanLibraryFiltersAndArchiveVisibility(t *testing.T) {
	router, planStore, projectStore := newLibraryRouter(t)
	createdProject, err := projectStore.Create("owner-a", "Attention Study", "paper reproduction")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	seedLibraryPlan(t, planStore, "owner-a", "p-attention", "Attention reproduction", string(models.StatusFailed), "Paper_Reproduction", createdProject.ID, false, now, 1.5)
	seedLibraryPlan(t, planStore, "owner-a", "p-attention2", "Attention benchmark", string(models.StatusCompleted), "Benchmark", createdProject.ID, false, now.Add(-time.Minute), 0.5)
	seedLibraryPlan(t, planStore, "owner-a", "p-archived", "Attention archived", string(models.StatusCompleted), "General", "", true, now.Add(-2*time.Minute), 0.1)
	seedLibraryPlan(t, planStore, "owner-b", "p-foreign", "Attention foreign", string(models.StatusFailed), "Paper_Reproduction", createdProject.ID, false, now, 9.9)

	get := func(path string) []planSummary {
		response := libraryRequest(router, http.MethodGet, path, "owner-a", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		var payload struct {
			Plans []planSummary `json:"plans"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		return payload.Plans
	}
	if got := get("/api/plans?status=failed"); len(got) != 1 || got[0].ID != "p-attention" {
		t.Fatalf("status filter got %#v", got)
	}
	if got := get("/api/plans?intent_type=Paper_Reproduction"); len(got) != 1 || got[0].ID != "p-attention" {
		t.Fatalf("intent filter got %#v", got)
	}
	if got := get("/api/plans?q=benchmark"); len(got) != 1 || got[0].ID != "p-attention2" {
		t.Fatalf("query filter got %#v", got)
	}
	if got := get("/api/plans?project_id=" + createdProject.ID); len(got) != 2 {
		t.Fatalf("project filter got %d plans", len(got))
	}
	if got := get("/api/plans"); len(got) != 2 {
		t.Fatalf("default list should hide archived, got %#v", got)
	}
	if got := get("/api/plans?archived=true"); len(got) != 1 || got[0].ID != "p-archived" {
		t.Fatalf("archived filter got %#v", got)
	}
	if got := get("/api/plans?archived=all"); len(got) != 3 {
		t.Fatalf("archived=all should include everything, got %d", len(got))
	}
}

func TestPlanLibraryRenameMoveArchiveDuplicateAndDeleteGuard(t *testing.T) {
	router, planStore, projectStore := newLibraryRouter(t)
	createdProject, err := projectStore.Create("owner-a", "Project X", "")
	if err != nil {
		t.Fatal(err)
	}
	seedLibraryPlan(t, planStore, "owner-a", "p-edit", "Original title", string(models.StatusCompleted), "General", "", false, time.Now().UTC(), 0)

	patchBody := []byte(fmt.Sprintf(`{"title":"Renamed","project_id":%q,"archived":true}`, createdProject.ID))
	response := libraryRequest(router, http.MethodPatch, "/api/plans/p-edit", "owner-a", patchBody)
	if response.Code != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", response.Code, response.Body.String())
	}
	updated, err := planStore.GetPlan("p-edit")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "Renamed" || updated.ProjectID != createdProject.ID || !updated.Archived || updated.ArchivedAt == nil {
		t.Fatalf("patch did not stick: %#v", updated)
	}

	duplicate := libraryRequest(router, http.MethodPost, "/api/plans/p-edit/duplicate", "owner-a", nil)
	if duplicate.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	var duplicated struct {
		Plan planSummary `json:"plan"`
	}
	if err := json.Unmarshal(duplicate.Body.Bytes(), &duplicated); err != nil {
		t.Fatal(err)
	}
	if duplicated.Plan.ID == "p-edit" || duplicated.Plan.Title != "Copy of Renamed" || duplicated.Plan.Archived {
		t.Fatalf("unexpected duplicate: %#v", duplicated.Plan)
	}
	copyPlan, err := planStore.GetPlan(duplicated.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range copyPlan.Nodes {
		if node.Status != models.StatusPending || node.Result != "" {
			t.Fatalf("duplicate kept execution state: %#v", node)
		}
	}

	running := &models.PlanGraph{ID: "p-running", OwnerID: "owner-a", Status: models.StatusInProgress, Nodes: []*models.TaskNode{}, Artifacts: map[string]models.Artifact{}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := planStore.SavePlan(running); err != nil {
		t.Fatal(err)
	}
	deleteResponse := libraryRequest(router, http.MethodDelete, "/api/plans/p-running", "owner-a", nil)
	if deleteResponse.Code != http.StatusConflict {
		t.Fatalf("delete running status=%d body=%s", deleteResponse.Code, deleteResponse.Body.String())
	}
}

func TestPlanBudgetPatchRaiseClearsSoftPause(t *testing.T) {
	router, planStore, _ := newLibraryRouter(t)
	plan := &models.PlanGraph{
		ID: "p-budget", OwnerID: "owner-a", Status: models.StatusInProgress, UserIntent: "x", IntentType: "General",
		Nodes: []*models.TaskNode{}, Artifacts: map[string]models.Artifact{}, CreatedAt: time.Now(), UpdatedAt: time.Now(),
		Budget: models.RunBudget{MaxTaskAttempts: 10, MaxDurationSec: 60, MaxCostUSD: 0.01, Policy: "soft"},
		Usage:  models.RunUsage{LLMCostUSD: 0.02, BudgetStatus: "exceeded"},
		Paused: true,
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"budget":{"max_cost_usd":1.0}}`)
	response := libraryRequest(router, http.MethodPatch, "/api/plans/p-budget", "owner-a", body)
	if response.Code != http.StatusOK {
		t.Fatalf("patch budget status=%d body=%s", response.Code, response.Body.String())
	}
	updated, err := planStore.GetPlan("p-budget")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Budget.MaxCostUSD != 1.0 || updated.Paused || updated.Usage.BudgetStatus != "ok" {
		t.Fatalf("budget patch did not clear pause: %#v %#v", updated.Budget, updated.Usage)
	}
}

func TestProjectCRUDAndNonEmptyDeleteGuard(t *testing.T) {
	router, planStore, _ := newLibraryRouter(t)
	create := libraryRequest(router, http.MethodPost, "/api/projects", "owner-a", []byte(`{"name":"Paper A","description":"first"}`))
	if create.Code != http.StatusOK {
		t.Fatalf("create project status=%d body=%s", create.Code, create.Body.String())
	}
	var created struct {
		Project models.Project `json:"project"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Project.ID == "" || created.Project.Name != "Paper A" {
		t.Fatalf("bad project %#v", created.Project)
	}
	list := libraryRequest(router, http.MethodGet, "/api/projects", "owner-a", nil)
	if list.Code != http.StatusOK || !bytes.Contains(list.Body.Bytes(), []byte("Paper A")) {
		t.Fatalf("list projects status=%d body=%s", list.Code, list.Body.String())
	}
	update := libraryRequest(router, http.MethodPatch, "/api/projects/"+created.Project.ID, "owner-a", []byte(`{"name":"Paper A v2"}`))
	if update.Code != http.StatusOK {
		t.Fatalf("update project status=%d body=%s", update.Code, update.Body.String())
	}
	seedLibraryPlan(t, planStore, "owner-a", "p-in-project", "in project", string(models.StatusCompleted), "General", created.Project.ID, false, time.Now().UTC(), 0)
	blocked := libraryRequest(router, http.MethodDelete, "/api/projects/"+created.Project.ID, "owner-a", nil)
	if blocked.Code != http.StatusConflict {
		t.Fatalf("non-empty project delete status=%d body=%s", blocked.Code, blocked.Body.String())
	}
	foreign := libraryRequest(router, http.MethodGet, "/api/projects", "owner-b", nil)
	if foreign.Code != http.StatusOK || bytes.Contains(foreign.Body.Bytes(), []byte("Paper A")) {
		t.Fatalf("project leaked across owners: %s", foreign.Body.String())
	}
}
