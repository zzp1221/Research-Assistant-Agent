package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"research-assistant-backend/internal/events"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
)

// The single-node execute endpoint must refuse a node the runtime is still
// waiting on, exactly like the scheduler refuses it at lease time.
func TestExecuteTaskRejectsPendingWaits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name       string
		status     models.TaskStatus
		action     bool
		question   bool
		wantCode   string
		wantStatus int
	}{
		{name: "awaiting approval", status: models.StatusAwaitingApproval, action: true, wantCode: "task_awaiting_approval"},
		{name: "awaiting input", status: models.StatusAwaitingInput, question: true, wantCode: "task_awaiting_input"},
		{name: "pending action on a pending node", status: models.StatusPending, action: true, wantCode: "task_approval_pending"},
		{name: "pending question on a pending node", status: models.StatusPending, question: true, wantCode: "task_question_pending"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			planStore := store.NewMemoryPlanStore()
			now := time.Now()
			plan := &models.PlanGraph{
				ID: "plan-wait-entry", OwnerID: "owner", Status: models.StatusPending, IntentType: "Code_Execution",
				Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
				Nodes: []*models.TaskNode{{
					ID: "node-1", Name: "node", Type: "generate_code", AssignedTo: "coder_agent",
					Status: test.status, CreatedAt: now, UpdatedAt: now,
				}},
			}
			if test.action {
				plan.RuntimeActions = []models.RuntimeActionRequest{{
					ID: "ra-1", TaskID: "node-1", Kind: models.RuntimeActionToolCall,
					Status: models.RuntimeActionPending, RequestedAt: now,
				}}
			}
			if test.question {
				plan.Questions = []models.PlanQuestion{{
					ID: "q-1", PlanID: plan.ID, TaskID: "node-1", Prompt: "continue?",
					Status: models.QuestionPending, RequestedAt: now,
				}}
			}
			if err := planStore.SavePlan(plan); err != nil {
				t.Fatal(err)
			}
			eventBus := events.NewBus()
			planScheduler := scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), eventBus, 1)
			runtime := newPlanRuntime(planner.NewPlanner(), planStore, planScheduler, eventBus)
			router := gin.New()
			runtime.register(router.Group("/api"))

			request := httptest.NewRequest(http.MethodPost, "/api/plans/"+plan.ID+"/tasks/node-1/execute", nil)
			request.AddCookie(&http.Cookie{Name: anonUserCookieName, Value: signIdentityCookie("owner")})
			request.AddCookie(&http.Cookie{Name: sessionCookieName, Value: signIdentityCookie("session")})
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s, want 409", recorder.Code, recorder.Body.String())
			}
			if test.wantCode != "" && !jsonHasCode(recorder.Body.String(), test.wantCode) {
				t.Fatalf("body=%s does not carry code %q", recorder.Body.String(), test.wantCode)
			}
			stored, _ := planStore.GetPlan(plan.ID)
			if stored.Nodes[0].Status != test.status {
				t.Fatalf("node status changed to %s", stored.Nodes[0].Status)
			}
			if stored.Nodes[0].ExecutionID != "" {
				t.Fatalf("refused execution still took a lease: %q", stored.Nodes[0].ExecutionID)
			}
		})
	}
}

func jsonHasCode(body, code string) bool {
	for i := 0; i+len(code) <= len(body); i++ {
		if body[i:i+len(code)] == code {
			return true
		}
	}
	return false
}
