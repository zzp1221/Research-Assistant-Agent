package scheduler

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/events"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/store"
)

func seedQuestionPlan(t *testing.T, planStore store.PlanStore, planID string, status models.TaskStatus) *models.PlanGraph {
	t.Helper()
	now := time.Now().UTC()
	plan := &models.PlanGraph{
		ID:         planID,
		UserIntent: "intent",
		IntentType: "General",
		Status:     status,
		OwnerID:    "owner-a",
		Nodes: []*models.TaskNode{
			{ID: planID + "-n1", Name: "ask", Type: "ask_user_question", AssignedTo: "general_agent", Status: models.StatusAwaitingInput, CreatedAt: now, UpdatedAt: now},
		},
		Edges:     []*models.TaskEdge{},
		Artifacts: map[string]models.Artifact{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestApplyQuestionFromTaskPausesPlan(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	bus := events.NewBus()
	scheduler := NewScheduler(planStore, NewDefaultTaskExecutor(), bus, 1)
	plan := seedQuestionPlan(t, planStore, "plan-q", models.StatusInProgress)
	question := &models.PlanQuestion{Header: "选择", Prompt: "选哪个？", AllowFreeText: true}
	if err := scheduler.applyQuestionFromTask(plan.ID, plan.ID+"-n1", question); err != nil {
		t.Fatal(err)
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Status != models.StatusAwaitingInput {
		t.Fatalf("plan status=%s, want awaiting_input", stored.Status)
	}
	if len(stored.Questions) != 1 || stored.Questions[0].Status != models.QuestionPending {
		t.Fatalf("unexpected questions: %+v", stored.Questions)
	}
	task := getNodeByID(stored, plan.ID+"-n1")
	if task == nil || task.Status != models.StatusAwaitingInput {
		t.Fatalf("task status=%v", task)
	}
	if !hasAwaitingInput(stored) {
		t.Fatal("hasAwaitingInput must detect the pending question")
	}
}

func TestExpireQuestionsAppliesDefault(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	bus := events.NewBus()
	scheduler := NewScheduler(planStore, NewDefaultTaskExecutor(), bus, 1)
	plan := seedQuestionPlan(t, planStore, "plan-q2", models.StatusAwaitingInput)
	past := time.Now().UTC().Add(-time.Minute)
	_ = planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.Questions = []models.PlanQuestion{{
			ID: "q1", PlanID: plan.ID, TaskID: plan.ID + "-n1", Prompt: "continue?",
			Options:       []models.QuestionOption{{ID: "yes", Label: "yes"}},
			DefaultOption: "yes", Status: models.QuestionPending, RequestedAt: past.Add(-time.Minute), ExpiresAt: &past,
		}}
		return nil
	})
	terminal, err := scheduler.ExpireQuestionsNow(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminal {
		t.Fatal("default answer must not make the plan terminal")
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Questions[0].Status != models.QuestionAnswered || !stored.Questions[0].AutoAnswered {
		t.Fatalf("question not auto answered: %+v", stored.Questions[0])
	}
	if stored.Status != models.StatusPending {
		t.Fatalf("plan status=%s, want pending", stored.Status)
	}
}

func TestExpireQuestionsWithoutDefaultBlocks(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	bus := events.NewBus()
	scheduler := NewScheduler(planStore, NewDefaultTaskExecutor(), bus, 1)
	plan := seedQuestionPlan(t, planStore, "plan-q3", models.StatusAwaitingInput)
	past := time.Now().UTC().Add(-time.Minute)
	_ = planStore.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		current.Questions = []models.PlanQuestion{{
			ID: "q2", PlanID: plan.ID, TaskID: plan.ID + "-n1", Prompt: "block me",
			Status: models.QuestionPending, RequestedAt: past.Add(-time.Minute), ExpiresAt: &past,
		}}
		return nil
	})
	terminal, err := scheduler.ExpireQuestionsNow(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !terminal {
		t.Fatal("missing default must make the plan terminal")
	}
	stored, _ := planStore.GetPlan(plan.ID)
	if stored.Status != models.StatusFailed || stored.Outcome != models.OutcomeBlocked {
		t.Fatalf("plan status=%s outcome=%s", stored.Status, stored.Outcome)
	}
	if len(stored.MissingRequirements) == 0 || stored.MissingRequirements[0].Kind != "question_timeout" {
		t.Fatalf("missing requirement not recorded: %+v", stored.MissingRequirements)
	}
}

type questionAgent struct{}

func (a *questionAgent) ExecuteTask(_ context.Context, task *models.Task, _ map[string]interface{}) error {
	if task.Type == "ask_user_question" {
		return nil
	}
	task.Status = models.StatusCompleted
	task.Result = "ok"
	return nil
}

func TestSchedulerPausesExecutionOnQuestion(t *testing.T) {
	planStore := store.NewMemoryPlanStore()
	bus := events.NewBus()
	executor := NewRoutedTaskExecutor(&questionAgent{}, &questionAgent{}, &questionAgent{})
	scheduler := NewScheduler(planStore, executor, bus, 1)
	now := time.Now().UTC()
	plan := &models.PlanGraph{
		ID:         "plan-run-q",
		UserIntent: "intent",
		IntentType: "General",
		Status:     models.StatusPending,
		OwnerID:    "owner-a",
		Nodes: []*models.TaskNode{{
			ID: "n1", Name: "ask", Type: "ask_user_question", AssignedTo: "general_agent",
			Status: models.StatusPending, Inputs: map[string]any{"question_prompt": "pick one"}, CreatedAt: now, UpdatedAt: now,
		}},
		Edges: []*models.TaskEdge{}, Artifacts: map[string]models.Artifact{}, CreatedAt: now, UpdatedAt: now,
	}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- scheduler.ExecutePlan(ctx, plan.ID) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored, err := planStore.GetPlan(plan.ID)
		if err == nil && stored.Status == models.StatusAwaitingInput && len(stored.Questions) == 1 {
			cancel()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	t.Fatalf("scheduler never paused on question: %+v", func() any { s, _ := planStore.GetPlan(plan.ID); return s }())
}
