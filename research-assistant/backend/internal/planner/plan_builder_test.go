package planner

import "testing"

func TestPlanBuilderRegistryCoversAllIntents(t *testing.T) {
	for _, intent := range []string{"Code_Execution", "Framework_Evaluation", "Paper_Reproduction", "Custom_Benchmark", "AutoResearch", "General"} {
		if _, ok := planBuilderRegistry[intent]; !ok {
			t.Fatalf("intent %s has no PlanBuilder", intent)
		}
	}
}
