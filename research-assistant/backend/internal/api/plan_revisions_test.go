package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

func newRevisionRouter(t *testing.T) (*gin.Engine, store.PlanStore, *planRuntime) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	eventBus := events.NewBus()
	runner := scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), eventBus, 1)
	runtime := newPlanRuntime(planner.NewPlanner(), planStore, runner, eventBus)
	runtime.revisions = revision.NewMemoryStore()
	router := gin.New()
	runtime.register(router.Group("/api"))
	return router, planStore, runtime
}

func revisionRequest(router *gin.Engine, method, path, userID string, body any) *httptest.ResponseRecorder {
	var reader *bytes.Buffer
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewBuffer(raw)
	} else {
		reader = bytes.NewBuffer(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(userIDHeaderName, userID)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func seedRevisionPlan(t *testing.T, planStore store.PlanStore, owner, id string) *models.PlanGraph {
	t.Helper()
	now := time.Now().UTC()
	plan := &models.PlanGraph{
		ID:         id,
		UserIntent: "intent " + id,
		IntentType: "Code_Execution",
		Status:     models.StatusPending,
		OwnerID:    owner,
		Nodes: []*models.TaskNode{
			{ID: id + "-a", Name: "node a", Type: "generate_code", AssignedTo: "coder_agent", Status: models.StatusPending, Inputs: map[string]any{"prompt": "v1"}, CreatedAt: now, UpdatedAt: now},
			{ID: id + "-b", Name: "node b", Type: "execute_code", AssignedTo: "sandbox_agent", Status: models.StatusPending, Dependencies: []string{id + "-a"}, CreatedAt: now, UpdatedAt: now},
		},
		Edges:     []*models.TaskEdge{{ID: "e1", From: id + "-a", To: id + "-b", Type: "control"}},
		Artifacts: map[string]models.Artifact{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func revisionPlanBody(plan *models.PlanGraph) map[string]any {
	nodes := make([]map[string]any, 0, len(plan.Nodes))
	for _, node := range plan.Nodes {
		nodes = append(nodes, map[string]any{
			"id": node.ID, "name": node.Name, "type": node.Type, "assigned_to": node.AssignedTo,
			"description": node.Description, "status": "pending", "dependencies": node.Dependencies,
			"inputs": node.Inputs, "timeout_seconds": node.TimeoutSeconds, "retry_limit": node.RetryLimit,
			"required_artifacts": []string{}, "output_artifacts": []string{},
		})
	}
	edges := make([]map[string]any, 0, len(plan.Edges))
	for _, edge := range plan.Edges {
		edges = append(edges, map[string]any{"id": edge.ID, "from": edge.From, "to": edge.To, "type": edge.Type})
	}
	return map[string]any{
		"id": plan.ID, "user_intent": plan.UserIntent, "intent_type": plan.IntentType,
		"nodes": nodes, "edges": edges, "artifacts": map[string]any{},
		"budget": map[string]any{"max_task_attempts": 10, "max_duration_seconds": 600},
	}
}

func TestRevisionEditCreatesSnapshotAndPreservesOld(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-rev")

	// Editing requires the initial revision to exist; the list endpoint lazily
	// creates revision 1.
	list := revisionRequest(router, http.MethodGet, "/api/plans/plan-rev/revisions", "owner-a", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list revisions status=%d body=%s", list.Code, list.Body.String())
	}
	var listPayload struct {
		Revisions []revisionSummary `json:"revisions"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listPayload); err != nil {
		t.Fatal(err)
	}
	if len(listPayload.Revisions) != 1 || listPayload.Revisions[0].Revision != 1 {
		t.Fatalf("expected revision 1, got %+v", listPayload.Revisions)
	}

	body := revisionPlanBody(plan)
	body["nodes"].([]map[string]any)[0]["inputs"] = map[string]any{"prompt": "v2"}
	created := revisionRequest(router, http.MethodPost, "/api/plans/plan-rev/revisions", "owner-a", map[string]any{
		"base_revision": 1, "reason": "change prompt", "graph": body,
	})
	if created.Code != http.StatusOK {
		t.Fatalf("create revision status=%d body=%s", created.Code, created.Body.String())
	}
	var createdPayload struct {
		Revision revisionSummary `json:"revision"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &createdPayload); err != nil {
		t.Fatal(err)
	}
	if createdPayload.Revision.Revision != 2 || createdPayload.Revision.ParentRevision != 1 {
		t.Fatalf("unexpected revision payload: %+v", createdPayload.Revision)
	}

	// Revision 1 must be unchanged.
	first := revisionRequest(router, http.MethodGet, "/api/plans/plan-rev/revisions/1", "owner-a", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("get revision 1 status=%d", first.Code)
	}
	var firstPayload struct {
		Revision models.PlanRevision `json:"revision"`
	}
	_ = json.Unmarshal(first.Body.Bytes(), &firstPayload)
	if got := firstPayload.Revision.Graph.Nodes[0].Inputs["prompt"]; got != "v1" {
		t.Fatalf("revision 1 mutated: %v", got)
	}
	// Current plan reflects the new draft.
	current := revisionRequest(router, http.MethodGet, "/api/plans/plan-rev", "owner-a", nil)
	var currentPayload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	_ = json.Unmarshal(current.Body.Bytes(), &currentPayload)
	if got := currentPayload.PlanGraph.Nodes[0].Inputs["prompt"]; got != "v2" {
		t.Fatalf("plan did not apply draft: %v", got)
	}
}

func TestRevisionStaleBaseReturnsConflict(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-conflict")
	_ = revisionRequest(router, http.MethodGet, "/api/plans/plan-conflict/revisions", "owner-a", nil)

	body := revisionPlanBody(plan)
	first := revisionRequest(router, http.MethodPost, "/api/plans/plan-conflict/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})
	if first.Code != http.StatusOK {
		t.Fatalf("first create status=%d body=%s", first.Code, first.Body.String())
	}
	second := revisionRequest(router, http.MethodPost, "/api/plans/plan-conflict/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})
	if second.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", second.Code, second.Body.String())
	}
	var payload struct {
		Code            string `json:"code"`
		CurrentRevision int    `json:"current_revision"`
	}
	_ = json.Unmarshal(second.Body.Bytes(), &payload)
	if payload.Code != "revision_conflict" || payload.CurrentRevision != 2 {
		t.Fatalf("unexpected conflict payload: %+v", payload)
	}
}

func TestRevisionRejectsEditedGraphOfOtherOwner(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-owner")
	_ = revisionRequest(router, http.MethodGet, "/api/plans/plan-owner/revisions", "owner-a", nil)
	body := revisionPlanBody(plan)
	response := revisionRequest(router, http.MethodPost, "/api/plans/plan-owner/revisions", "owner-b", map[string]any{"base_revision": 1, "graph": body})
	if response.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", response.Code, response.Body.String())
	}
}

func TestRevisionRunningPlanCannotBeEdited(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-running")
	_ = revisionRequest(router, http.MethodGet, "/api/plans/plan-running/revisions", "owner-a", nil)
	_ = planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.Status = models.StatusInProgress
		return nil
	})
	body := revisionPlanBody(plan)
	response := revisionRequest(router, http.MethodPost, "/api/plans/plan-running/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})
	if response.Code != http.StatusConflict {
		t.Fatalf("want 409 while running, got %d body=%s", response.Code, response.Body.String())
	}
	// Cancelling releases the lock.
	_ = planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.Status = models.StatusCanceled
		return nil
	})
	response = revisionRequest(router, http.MethodPost, "/api/plans/plan-running/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})
	if response.Code != http.StatusOK {
		t.Fatalf("canceled plan must be editable, got %d body=%s", response.Code, response.Body.String())
	}
}

func TestRevisionValidationRejectsCycleAndUnknownType(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-validate")
	_ = revisionRequest(router, http.MethodGet, "/api/plans/plan-validate/revisions", "owner-a", nil)
	body := revisionPlanBody(plan)
	nodes := body["nodes"].([]map[string]any)
	nodes[0]["dependencies"] = []string{"plan-validate-b"}
	nodes[1]["dependencies"] = []string{"plan-validate-a"}
	response := revisionRequest(router, http.MethodPost, "/api/plans/plan-validate/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for cycle, got %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Code             string            `json:"code"`
		ValidationErrors []validationError `json:"validation_errors"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if payload.Code != "validation_failed" || len(payload.ValidationErrors) == 0 {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestRevisionDiffReturnsFieldEdgeAndBudgetChanges(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-diff")
	_ = revisionRequest(router, http.MethodGet, "/api/plans/plan-diff/revisions", "owner-a", nil)
	body := revisionPlanBody(plan)
	nodes := body["nodes"].([]map[string]any)
	nodes[0]["name"] = "renamed node"
	body["budget"] = map[string]any{"max_task_attempts": 10, "max_duration_seconds": 1200, "max_cost_usd": 2.5}
	body["edges"] = []map[string]any{}
	_ = revisionRequest(router, http.MethodPost, "/api/plans/plan-diff/revisions", "owner-a", map[string]any{"base_revision": 1, "graph": body})

	response := revisionRequest(router, http.MethodGet, "/api/plans/plan-diff/diff?from=1&to=2", "owner-a", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("diff status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Diff []diffEntry `json:"diff"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	kinds := map[string]bool{}
	fields := map[string]bool{}
	for _, entry := range payload.Diff {
		kinds[entry.Kind] = true
		fields[entry.Field] = true
	}
	if !kinds["node_modified"] || !kinds["edge_removed"] || !kinds["budget_changed"] {
		t.Fatalf("missing diff kinds: %+v", payload.Diff)
	}
	if !fields["name"] || !fields["max_duration_seconds"] || !fields["max_cost_usd"] {
		t.Fatalf("missing diff fields: %+v", payload.Diff)
	}
}

func TestRevisionApproveAndExecuteBindsRevision(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-exec")
	_ = revisionRequest(router, http.MethodGet, "/api/plans/plan-exec/revisions", "owner-a", nil)
	_ = planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.Review = models.ReviewState{Required: true, Status: models.ReviewPending}
		current.Status = models.StatusAwaitingReview
		return nil
	})
	// Executing before approval must fail.
	blocked := revisionRequest(router, http.MethodPost, "/api/plans/plan-exec/revisions/1/execute", "owner-a", map[string]any{})
	if blocked.Code != http.StatusConflict {
		t.Fatalf("want 409 before approval, got %d body=%s", blocked.Code, blocked.Body.String())
	}
	approved := revisionRequest(router, http.MethodPost, "/api/plans/plan-exec/revisions/1/approve", "owner-a", map[string]any{})
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body.String())
	}
	executed := revisionRequest(router, http.MethodPost, "/api/plans/plan-exec/revisions/1/execute", "owner-a", map[string]any{})
	if executed.Code != http.StatusAccepted {
		t.Fatalf("execute status=%d body=%s", executed.Code, executed.Body.String())
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.CurrentRevision != 1 {
		t.Fatalf("current revision=%d, want 1", stored.CurrentRevision)
	}
	if stored.Review.Status != models.ReviewApproved {
		t.Fatalf("review status=%s", stored.Review.Status)
	}
}

func TestReviewGateTriggersAndSimplePlanSkips(t *testing.T) {
	router, _, _ := newRevisionRouter(t)
	// A simple two-node plan is below every default trigger.
	simple := revisionRequest(router, http.MethodPost, "/api/plan", "owner-a", map[string]any{"intent": "用 Python 画一个正弦函数图像"})
	if simple.Code != http.StatusOK {
		t.Fatalf("create simple plan status=%d body=%s", simple.Code, simple.Body.String())
	}
	var simplePayload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	_ = json.Unmarshal(simple.Body.Bytes(), &simplePayload)
	if simplePayload.PlanGraph.Review.Required || simplePayload.PlanGraph.Status == models.StatusAwaitingReview {
		t.Fatalf("simple plan must not require review: %+v", simplePayload.PlanGraph.Review)
	}
	// Explicit require_review must gate it.
	forced := revisionRequest(router, http.MethodPost, "/api/plan", "owner-a", map[string]any{"intent": "总结量子计算的基本概念", "require_review": true})
	if forced.Code != http.StatusOK {
		t.Fatalf("create forced plan status=%d body=%s", forced.Code, forced.Body.String())
	}
	var forcedPayload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	_ = json.Unmarshal(forced.Body.Bytes(), &forcedPayload)
	if !forcedPayload.PlanGraph.Review.Required || forcedPayload.PlanGraph.Review.Status != models.ReviewPending || forcedPayload.PlanGraph.Status != models.StatusAwaitingReview {
		t.Fatalf("require_review plan not gated: %+v status=%s", forcedPayload.PlanGraph.Review, forcedPayload.PlanGraph.Status)
	}
	// Direct execution must refuse the review-gated plan.
	blocked := revisionRequest(router, http.MethodPost, "/api/plans/"+forcedPayload.PlanGraph.ID+"/execute", "owner-a", map[string]any{})
	if blocked.Code != http.StatusConflict {
		t.Fatalf("want 409 for awaiting review, got %d body=%s", blocked.Code, blocked.Body.String())
	}
}

func TestQuestionCreateAnswerAndInbox(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-q")
	created := revisionRequest(router, http.MethodPost, "/api/plans/plan-q/questions", "owner-a", map[string]any{
		"task_id": "plan-q-a",
		"header":  "选择数据集",
		"prompt":  "使用哪个版本的训练集？",
		"options": []map[string]any{
			{"id": "v1", "label": "v1（原始）"},
			{"id": "v2", "label": "v2（清洗后）"},
		},
		"default_option": "v1",
	})
	if created.Code != http.StatusOK {
		t.Fatalf("create question status=%d body=%s", created.Code, created.Body.String())
	}
	var createdPayload struct {
		Question models.PlanQuestion `json:"question"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &createdPayload)
	if createdPayload.Question.ID == "" || createdPayload.Question.Status != models.QuestionPending {
		t.Fatalf("unexpected question: %+v", createdPayload.Question)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Status != models.StatusAwaitingInput {
		t.Fatalf("plan status=%s, want awaiting_input", stored.Status)
	}
	// Inbox sees the question.
	inbox := revisionRequest(router, http.MethodGet, "/api/approvals?kind=question", "owner-a", nil)
	if inbox.Code != http.StatusOK {
		t.Fatalf("inbox status=%d body=%s", inbox.Code, inbox.Body.String())
	}
	var inboxPayload struct {
		Items []inboxItem `json:"items"`
	}
	_ = json.Unmarshal(inbox.Body.Bytes(), &inboxPayload)
	if len(inboxPayload.Items) != 1 || inboxPayload.Items[0].Kind != "question" {
		t.Fatalf("unexpected inbox: %+v", inboxPayload.Items)
	}
	// Answer resumes the task.
	answer := revisionRequest(router, http.MethodPost, "/api/plans/plan-q/questions/"+createdPayload.Question.ID+"/answer", "owner-a", map[string]any{"answer_option": "v2"})
	if answer.Code != http.StatusOK {
		t.Fatalf("answer status=%d body=%s", answer.Code, answer.Body.String())
	}
	stored, _ = planStore.GetPlan(plan.ID)
	task := findPlanTask(stored, "plan-q-a")
	if task == nil || task.Status != models.StatusReady {
		t.Fatalf("task not resumed: %+v", task)
	}
	if stored.Status != models.StatusPending {
		t.Fatalf("plan status=%s, want pending", stored.Status)
	}
	if got := task.Inputs["question_answer"]; got != "v2（清洗后）" {
		t.Fatalf("answer input=%v", got)
	}
}

func TestQuestionTimeoutUsesDefaultOrBlocks(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-timeout")
	created := revisionRequest(router, http.MethodPost, "/api/plans/plan-timeout/questions", "owner-a", map[string]any{
		"task_id":         "plan-timeout-a",
		"prompt":          "continue?",
		"options":         []map[string]any{{"id": "yes", "label": "yes"}},
		"default_option":  "yes",
		"timeout_seconds": 1,
	})
	if created.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	// Force the deadline into the past; the read path performs the lazy sweep.
	_ = planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		past := time.Now().UTC().Add(-time.Minute)
		for i := range current.Questions {
			current.Questions[i].ExpiresAt = &past
		}
		return nil
	})
	list := revisionRequest(router, http.MethodGet, "/api/plans/plan-timeout/questions", "owner-a", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if len(stored.Questions) != 1 || stored.Questions[0].Status != models.QuestionAnswered || !stored.Questions[0].AutoAnswered {
		t.Fatalf("default answer not applied: %+v", stored.Questions)
	}
	if task := findPlanTask(stored, "plan-timeout-a"); task == nil || task.Status != models.StatusReady {
		t.Fatalf("task not resumed after default: %+v", task)
	}

	// No default -> block.
	plan2 := seedRevisionPlan(t, planStore, "owner-a", "plan-timeout2")
	_ = revisionRequest(router, http.MethodPost, "/api/plans/plan-timeout2/questions", "owner-a", map[string]any{
		"task_id": "plan-timeout2-a", "prompt": "no default", "timeout_seconds": 1,
	})
	_ = planStore.UpdatePlan(plan2.ID, func(current *models.PlanGraph) error {
		past := time.Now().UTC().Add(-time.Minute)
		for i := range current.Questions {
			current.Questions[i].ExpiresAt = &past
		}
		return nil
	})
	_ = revisionRequest(router, http.MethodGet, "/api/plans/plan-timeout2/questions", "owner-a", nil)
	stored2, _ := planStore.GetPlan(plan2.ID)
	if stored2.Status != models.StatusFailed || stored2.Outcome != models.OutcomeBlocked {
		t.Fatalf("plan should block: status=%s outcome=%s", stored2.Status, stored2.Outcome)
	}
}

func TestBulkApprovalRejectsHighRisk(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-bulk")
	low := models.RuntimeActionRequest{ID: "ra-low", PlanID: plan.ID, TaskID: "plan-bulk-a", Kind: models.RuntimeActionNetworkAccess, Title: "network", Risk: "low", Status: models.RuntimeActionPending, RequestedAt: time.Now().UTC()}
	high := models.RuntimeActionRequest{ID: "ra-high", PlanID: plan.ID, TaskID: "plan-bulk-a", Kind: models.RuntimeActionGPUUse, Title: "gpu", Risk: "high", Status: models.RuntimeActionPending, RequestedAt: time.Now().UTC()}
	_ = planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.RuntimeActions = append(current.RuntimeActions, low, high)
		current.Status = models.StatusAwaitingApproval
		return nil
	})
	response := revisionRequest(router, http.MethodPost, "/api/approvals/bulk", "owner-a", map[string]any{"ids": []string{"ra-high"}, "decision": "approve"})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for high risk, got %d body=%s", response.Code, response.Body.String())
	}
	ok := revisionRequest(router, http.MethodPost, "/api/approvals/bulk", "owner-a", map[string]any{"ids": []string{"ra-low"}, "decision": "approve"})
	if ok.Code != http.StatusOK {
		t.Fatalf("bulk low risk status=%d body=%s", ok.Code, ok.Body.String())
	}
	stored, _ := planStore.GetPlan(plan.ID)
	action := models.FindRuntimeActionByID(stored, "ra-low")
	if action == nil || action.Status != models.RuntimeActionApproved {
		t.Fatalf("low risk not approved: %+v", action)
	}
	highAction := models.FindRuntimeActionByID(stored, "ra-high")
	if highAction == nil || highAction.Status != models.RuntimeActionPending {
		t.Fatalf("high risk must stay pending: %+v", highAction)
	}
}

func TestInboxAggregatesAcrossPlans(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	seedRevisionPlan(t, planStore, "owner-a", "plan-inbox-a")
	seedRevisionPlan(t, planStore, "owner-a", "plan-inbox-b")
	_ = revisionRequest(router, http.MethodPost, "/api/plans/plan-inbox-a/questions", "owner-a", map[string]any{"task_id": "plan-inbox-a-a", "prompt": "first", "timeout_seconds": 600})
	_ = revisionRequest(router, http.MethodPost, "/api/plans/plan-inbox-b/questions", "owner-a", map[string]any{"task_id": "plan-inbox-b-a", "prompt": "second", "timeout_seconds": 600})
	response := revisionRequest(router, http.MethodGet, "/api/approvals?kind=all&limit=10", "owner-a", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("inbox status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Items []inboxItem `json:"items"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	plans := map[string]bool{}
	for _, item := range payload.Items {
		plans[item.PlanID] = true
	}
	if len(payload.Items) != 2 || !plans["plan-inbox-a"] || !plans["plan-inbox-b"] {
		fmt.Println(response.Body.String())
		t.Fatalf("inbox did not aggregate: %+v", payload.Items)
	}
}

func createPlanViaAPI(t *testing.T, router *gin.Engine, user string, body map[string]any) models.PlanGraph {
	t.Helper()
	response := revisionRequest(router, http.MethodPost, "/api/plan", user, body)
	if response.Code != http.StatusOK {
		t.Fatalf("create plan status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.PlanGraph
}

func TestRevisionEditCannotClearReviewGate(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := createPlanViaAPI(t, router, "owner-a", map[string]any{"intent": "总结量子计算的基本概念", "require_review": true})
	if plan.Status != models.StatusAwaitingReview || !plan.Review.Required {
		t.Fatalf("plan not gated: status=%s review=%+v", plan.Status, plan.Review)
	}
	// The client payload deliberately omits the review field, mirroring the
	// existing E2E graph builder. The server must not let that clear the gate.
	body := revisionPlanBody(&plan)
	edit := revisionRequest(router, http.MethodPost, "/api/plans/"+plan.ID+"/revisions", "owner-a", map[string]any{
		"base_revision": 1,
		"reason":        "attempt to clear review",
		"graph":         body,
	})
	if edit.Code != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", edit.Code, edit.Body.String())
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if !stored.Review.Required || stored.Review.Status != models.ReviewPending {
		t.Fatalf("review gate was cleared: %+v", stored.Review)
	}
	if stored.Status != models.StatusAwaitingReview {
		t.Fatalf("status=%s, want awaiting_review", stored.Status)
	}
	exec := revisionRequest(router, http.MethodPost, "/api/plans/"+plan.ID+"/revisions/2/execute", "owner-a", map[string]any{})
	if exec.Code != http.StatusConflict || !strings.Contains(exec.Body.String(), "review_pending") {
		t.Fatalf("unapproved revision executed: status=%d body=%s", exec.Code, exec.Body.String())
	}
}

func TestPlanExecutionBindsLatestRevision(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := createPlanViaAPI(t, router, "owner-a", map[string]any{"intent": "用 Python 计算 1+1 并打印结果"})
	body := revisionPlanBody(&plan)
	body["nodes"].([]map[string]any)[0]["name"] = "renamed by draft"
	edit := revisionRequest(router, http.MethodPost, "/api/plans/"+plan.ID+"/revisions", "owner-a", map[string]any{
		"base_revision": 1,
		"reason":        "rename",
		"graph":         body,
	})
	if edit.Code != http.StatusOK {
		t.Fatalf("edit status=%d body=%s", edit.Code, edit.Body.String())
	}
	exec := revisionRequest(router, http.MethodPost, "/api/plans/"+plan.ID+"/execute", "owner-a", map[string]any{})
	if exec.Code != http.StatusAccepted {
		t.Fatalf("execute status=%d body=%s", exec.Code, exec.Body.String())
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.CurrentRevision != 2 {
		t.Fatalf("current revision=%d, want 2", stored.CurrentRevision)
	}
	if len(stored.Nodes) == 0 || stored.Nodes[0].Revision != 2 {
		t.Fatalf("node revision not stamped: %+v", stored.Nodes)
	}
	_ = revisionRequest(router, http.MethodPost, "/api/plans/"+plan.ID+"/cancel", "owner-a", map[string]any{})
}

func TestDuplicatePlanKeepsReviewGate(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := createPlanViaAPI(t, router, "owner-a", map[string]any{"intent": "总结机器学习的基本概念", "require_review": true})
	duplicated := revisionRequest(router, http.MethodPost, "/api/plans/"+plan.ID+"/duplicate", "owner-a", map[string]any{})
	if duplicated.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d body=%s", duplicated.Code, duplicated.Body.String())
	}
	var payload struct {
		Plan planSummary `json:"plan"`
	}
	if err := json.Unmarshal(duplicated.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	copyPlan, err := planStore.GetPlan(payload.Plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !copyPlan.Review.Required || copyPlan.Review.Status != models.ReviewPending {
		t.Fatalf("copy lost review gate: %+v", copyPlan.Review)
	}
	if copyPlan.Status != models.StatusAwaitingReview {
		t.Fatalf("copy status=%s, want awaiting_review", copyPlan.Status)
	}
}

func TestApprovePlanOrdersRuntimeApprovalBeforeReview(t *testing.T) {
	t.Setenv("REQUIRE_PLAN_APPROVAL", "true")
	router, planStore, _ := newRevisionRouter(t)
	plan := createPlanViaAPI(t, router, "owner-a", map[string]any{"intent": "总结数据库系统的基本概念", "require_review": true})
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Status != models.StatusAwaitingApproval || !stored.Approval.Required || !stored.Review.Required {
		t.Fatalf("expected both gates: status=%s approval=%+v review=%+v", stored.Status, stored.Approval, stored.Review)
	}
	first := revisionRequest(router, http.MethodPost, "/api/plans/"+plan.ID+"/approve", "owner-a", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first approve status=%d body=%s", first.Code, first.Body.String())
	}
	stored, _ = planStore.GetPlan(plan.ID)
	if stored.Approval.Status != "approved" || stored.Status != models.StatusAwaitingReview {
		t.Fatalf("runtime approval skipped review: approval=%+v status=%s", stored.Approval, stored.Status)
	}
	second := revisionRequest(router, http.MethodPost, "/api/plans/"+plan.ID+"/approve", "owner-a", nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second approve status=%d body=%s", second.Code, second.Body.String())
	}
	stored, _ = planStore.GetPlan(plan.ID)
	if stored.Review.Status != models.ReviewApproved || stored.Status != models.StatusPending {
		t.Fatalf("review not approved: review=%+v status=%s", stored.Review, stored.Status)
	}
}
