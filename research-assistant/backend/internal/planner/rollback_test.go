package planner

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestComplexityRouterDisabledRestoresLegacyGeneral(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	t.Setenv("COMPLEXITY_ROUTER_ENABLED", "false")
	plan, err := NewPlanner().BuildPlan(t.Context(), models.IntentContext{
		RawIntent:  "你好",
		IntentType: "General",
		Entities:   map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Nodes) != 3 {
		t.Fatalf("router-disabled General plan nodes=%d, want legacy 3", len(plan.Nodes))
	}
	if plan.Meta.GeneralPlanShape != "legacy" {
		t.Fatalf("general_plan_shape=%q, want legacy", plan.Meta.GeneralPlanShape)
	}
	if plan.Meta.PlanningRoute != "template_fallback" {
		t.Fatalf("planning_route=%q, want template_fallback", plan.Meta.PlanningRoute)
	}
}
