package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestReviewGateCostThresholdTrigger(t *testing.T) {
	t.Setenv("REVIEW_COST_THRESHOLD_USD", "0.01")
	router, _, _ := newRevisionRouter(t)
	response := revisionRequest(router, http.MethodPost, "/api/plan", "owner-a", map[string]any{"intent": "用 Python 画一个正弦函数图像"})
	if response.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if !payload.PlanGraph.Review.Required || payload.PlanGraph.Status != models.StatusAwaitingReview {
		t.Fatalf("cost threshold must gate the plan: review=%+v status=%s", payload.PlanGraph.Review, payload.PlanGraph.Status)
	}
	if payload.PlanGraph.Review.EstimatedUSD <= 0.01 {
		t.Fatalf("estimated USD must be recorded: %+v", payload.PlanGraph.Review)
	}
}

func TestReviewGateAlwaysMode(t *testing.T) {
	t.Setenv("PLAN_REVIEW_MODE", "always")
	router, _, _ := newRevisionRouter(t)
	response := revisionRequest(router, http.MethodPost, "/api/plan", "owner-a", map[string]any{"intent": "总结量子计算的基本概念"})
	var payload struct {
		PlanGraph models.PlanGraph `json:"plan_graph"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	if !payload.PlanGraph.Review.Required || payload.PlanGraph.Review.Status != models.ReviewPending {
		t.Fatalf("always mode must gate every plan: %+v", payload.PlanGraph.Review)
	}
}

func TestReviewGateHighRiskTaskTriggers(t *testing.T) {
	plan := &models.PlanGraph{Nodes: []*models.TaskNode{{ID: "n1", Type: "paper_code_execute"}}}
	state := planReviewDecision(plan, false)
	if !state.Required || state.Risk != "high" {
		t.Fatalf("high-risk node must require review: %+v", state)
	}
	low := &models.PlanGraph{Nodes: []*models.TaskNode{{ID: "n1", Type: "generate_code"}}}
	if planReviewDecision(low, false).Required {
		t.Fatal("plain code generation must not trigger review")
	}
}

func TestValidationReportsActionableErrors(t *testing.T) {
	plan := &models.PlanGraph{
		Nodes: []*models.TaskNode{
			{ID: "a", Type: "generate_code", AssignedTo: "coder_agent", Dependencies: []string{"missing"}},
			{ID: "b", Type: "execute_code", AssignedTo: "sandbox_agent", Dependencies: []string{"a"}},
		},
		Edges:     []*models.TaskEdge{{ID: "e1", From: "a", To: "ghost", Type: "control"}},
		Artifacts: map[string]models.Artifact{},
	}
	result := validatePlanGraph(plan)
	if result.Valid {
		t.Fatal("expected invalid plan")
	}
	fields := map[string]bool{}
	for _, issue := range result.Errors {
		fields[issue.Field] = true
		if issue.Message == "" || issue.Remediation == "" {
			t.Fatalf("validation error must be actionable: %+v", issue)
		}
	}
	if !fields["dependencies"] || !fields["edges"] {
		t.Fatalf("missing expected error fields: %+v", result.Errors)
	}
}

func TestValidationAcceptsValidGraph(t *testing.T) {
	plan := &models.PlanGraph{
		Nodes: []*models.TaskNode{
			{ID: "a", Type: "generate_code", AssignedTo: "coder_agent", OutputArtifacts: []string{"code"}},
			{ID: "b", Type: "execute_code", AssignedTo: "sandbox_agent", Dependencies: []string{"a"}, RequiredArtifacts: []string{"code"}},
		},
		Edges:     []*models.TaskEdge{{ID: "e1", From: "a", To: "b", Type: "control"}},
		Artifacts: map[string]models.Artifact{},
		Budget:    models.RunBudget{MaxTaskAttempts: 10, MaxDurationSec: 600},
	}
	result := validatePlanGraph(plan)
	if !result.Valid {
		t.Fatalf("valid graph rejected: %+v", result.Errors)
	}
}

func TestQuestionAnswerRejectsForeignOption(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-opt")
	created := revisionRequest(router, http.MethodPost, "/api/plans/plan-opt/questions", "owner-a", map[string]any{
		"task_id": "plan-opt-a", "prompt": "pick", "options": []map[string]any{{"id": "only", "label": "only"}},
	})
	var payload struct {
		Question models.PlanQuestion `json:"question"`
	}
	_ = json.Unmarshal(created.Body.Bytes(), &payload)
	bad := revisionRequest(router, http.MethodPost, "/api/plans/plan-opt/questions/"+payload.Question.ID+"/answer", "owner-a", map[string]any{"answer_option": "other"})
	if bad.Code != http.StatusConflict {
		t.Fatalf("foreign option must be rejected, got %d body=%s", bad.Code, bad.Body.String())
	}
	if plan.ID == "" {
		t.Fatal("unreachable")
	}
}

func TestQuestionsScopedByOwner(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	seedRevisionPlan(t, planStore, "owner-a", "plan-scope")
	response := revisionRequest(router, http.MethodPost, "/api/plans/plan-scope/questions", "owner-b", map[string]any{"prompt": "should fail"})
	if response.Code != http.StatusForbidden {
		t.Fatalf("foreign owner must not create questions, got %d", response.Code)
	}
}

func TestRejectedReviewBlocksExecution(t *testing.T) {
	router, planStore, _ := newRevisionRouter(t)
	plan := seedRevisionPlan(t, planStore, "owner-a", "plan-rejected")
	_ = revisionRequest(router, http.MethodGet, "/api/plans/plan-rejected/revisions", "owner-a", nil)
	_ = planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.Review = models.ReviewState{Required: true, Status: models.ReviewPending}
		current.Status = models.StatusAwaitingReview
		return nil
	})
	rejected := revisionRequest(router, http.MethodPost, "/api/plans/plan-rejected/revisions/1/reject", "owner-a", map[string]any{})
	if rejected.Code != http.StatusOK {
		t.Fatalf("reject status=%d body=%s", rejected.Code, rejected.Body.String())
	}
	exec := revisionRequest(router, http.MethodPost, "/api/plans/plan-rejected/revisions/1/execute", "owner-a", map[string]any{})
	if exec.Code != http.StatusConflict {
		t.Fatalf("rejected review must block execution, got %d body=%s", exec.Code, exec.Body.String())
	}
	var payload struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(exec.Body.Bytes(), &payload)
	if payload.Code != "review_rejected" && payload.Code != "review_pending" {
		t.Fatalf("unexpected code: %s", payload.Code)
	}
}
