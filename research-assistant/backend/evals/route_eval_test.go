package evals

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
)

type routeEvalCase struct {
	ID                 string         `json:"id"`
	Text               string         `json:"text"`
	IntentType         string         `json:"intent_type"`
	Entities           map[string]any `json:"entities"`
	ExpectedComplexity string         `json:"expected_complexity"`
	ExpectedRoute      string         `json:"expected_route"`
}

type routeEvalDataset struct {
	Version string          `json:"version"`
	Cases   []routeEvalCase `json:"cases"`
}

func complexityRank(value string) int {
	switch value {
	case "L0":
		return 0
	case "L1":
		return 1
	case "L2":
		return 2
	case "L3":
		return 3
	case "L4":
		return 4
	default:
		return -1
	}
}

// TestRouteEvalDataset is the golden-set gate for the complexity router:
// accuracy >= 90% and under-routing (complex task treated as simple) <= 1 case.
func TestRouteEvalDataset(t *testing.T) {
	raw, err := os.ReadFile("route_eval_dataset.json")
	if err != nil {
		t.Fatal(err)
	}
	var dataset routeEvalDataset
	if err := json.Unmarshal(raw, &dataset); err != nil {
		t.Fatal(err)
	}
	if len(dataset.Cases) < 30 {
		t.Fatalf("golden set too small: %d cases", len(dataset.Cases))
	}
	router := planner.NewComplexityRouterFromEnv()
	correct, routeCorrect, under, over := 0, 0, 0, 0
	for _, tc := range dataset.Cases {
		entities := tc.Entities
		if entities == nil {
			entities = map[string]any{}
		}
		decision := router.Decide(context.Background(), models.IntentContext{
			RawIntent:  tc.Text,
			IntentType: tc.IntentType,
			Entities:   entities,
		}, nil, nil)
		got := string(decision.Complexity)
		switch {
		case got == tc.ExpectedComplexity:
			correct++
		case complexityRank(got) < complexityRank(tc.ExpectedComplexity):
			under++
			t.Logf("UNDER %s: got %s want %s (%s)", tc.ID, got, tc.ExpectedComplexity, decision.Reason)
		default:
			over++
			t.Logf("OVER %s: got %s want %s (%s)", tc.ID, got, tc.ExpectedComplexity, decision.Reason)
		}
		if decision.RouteHint == tc.ExpectedRoute {
			routeCorrect++
		}
	}
	accuracy := float64(correct) / float64(len(dataset.Cases))
	routeAccuracy := float64(routeCorrect) / float64(len(dataset.Cases))
	t.Logf("route eval: accuracy=%.2f route_hint=%.2f under=%d over=%d cases=%d", accuracy, routeAccuracy, under, over, len(dataset.Cases))
	if accuracy < 0.90 {
		t.Fatalf("complexity accuracy %.2f below 0.90", accuracy)
	}
	if under > 1 {
		t.Fatalf("under-routing count %d above 1", under)
	}
	if routeAccuracy < 0.95 {
		t.Fatalf("route hint accuracy %.2f below 0.95", routeAccuracy)
	}
}
