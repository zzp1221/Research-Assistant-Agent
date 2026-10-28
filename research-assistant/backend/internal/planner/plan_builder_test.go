package planner

import "testing"

func TestPlanBuilderRegistryCoversAllIntents(t *testing.T) {
	for _, intent := range []string{"Code_Execution", "Framework_Evaluation", "Paper_Reproduction", "Custom_Benchmark", "AutoResearch", "General"} {
		if _, ok := planBuilderRegistry[TemplateKey{IntentType: intent, Variant: "standard"}]; !ok {
			t.Fatalf("intent %s has no standard PlanBuilder", intent)
		}
	}
	// The four primary intents must expose the full variant set.
	for _, intent := range []string{"Code_Execution", "Framework_Evaluation", "General"} {
		for _, variant := range []string{"simple", "standard", "full"} {
			if _, ok := planBuilderRegistry[TemplateKey{IntentType: intent, Variant: variant}]; !ok {
				t.Fatalf("intent %s missing variant %s", intent, variant)
			}
		}
	}
	for _, variant := range []string{"light", "standard", "full"} {
		if _, ok := planBuilderRegistry[TemplateKey{IntentType: "Paper_Reproduction", Variant: variant}]; !ok {
			t.Fatalf("Paper_Reproduction missing variant %s", variant)
		}
	}
}
