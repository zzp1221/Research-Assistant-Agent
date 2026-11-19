package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

type isolationNamedWords []string
type isolationNamedScores map[string]int64
type isolationNamedCount int64
type isolationNamedPointer *isolationDiagnostic

type isolationDiagnostic struct {
	Counts []int
	Labels map[string]string
	At     time.Time
}

func TestClonePreservesDynamicTypesAndEmptyValues(t *testing.T) {
	now := time.Now()
	var typedNilMap map[string]string
	var typedNilSlice isolationNamedWords
	var typedNilPointer *models.Verification
	report := &isolationDiagnostic{Counts: []int{1}, Labels: map[string]string{"source": "original"}, At: now}
	shared := map[string]any{"score": 1}
	plan := &models.PlanGraph{
		ID: "clone-types",
		Nodes: []*models.TaskNode{{
			Contract: models.TaskContract{AllowedTools: []string{}},
			Inputs: map[string]any{
				"large_integer": int64(9007199254740993),
				"named_count":   isolationNamedCount(42),
				"typed_map":     isolationNamedScores{"model": 7},
				"typed_slice":   isolationNamedWords{"original"},
				"array":         [1][]string{{"original"}},
				"raw":           json.RawMessage(`[1]`),
				"report":        report,
				"named_pointer": isolationNamedPointer(report),
				"shared_a":      shared,
				"shared_b":      shared,
				"nil_map":       typedNilMap,
				"nil_slice":     typedNilSlice,
				"nil_pointer":   typedNilPointer,
				"empty_map":     map[string]string{},
				"empty_slice":   []int{},
			},
		}, nil},
		Artifacts: map[string]models.Artifact{},
		CreatedAt: now,
	}
	cloned, err := clonePlanGraph(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan, cloned) {
		t.Fatal("copy changed dynamic types, values, time metadata, or nil/empty containers")
	}
	inputs := cloned.Nodes[0].Inputs
	inputs["typed_map"].(isolationNamedScores)["model"] = 999
	inputs["typed_slice"].(isolationNamedWords)[0] = "mutated"
	inputs["array"].([1][]string)[0][0] = "mutated"
	inputs["raw"].(json.RawMessage)[1] = '9'
	inputs["report"].(*isolationDiagnostic).Counts[0] = 999
	inputs["named_pointer"].(isolationNamedPointer).Labels["source"] = "mutated"
	inputs["shared_a"].(map[string]any)["score"] = 999
	if plan.Nodes[0].Inputs["typed_map"].(isolationNamedScores)["model"] != 7 ||
		plan.Nodes[0].Inputs["typed_slice"].(isolationNamedWords)[0] != "original" ||
		plan.Nodes[0].Inputs["array"].([1][]string)[0][0] != "original" ||
		string(plan.Nodes[0].Inputs["raw"].(json.RawMessage)) != `[1]` ||
		report.Counts[0] != 1 || report.Labels["source"] != "original" || shared["score"] != 1 {
		t.Fatal("a copied dynamic value still aliases its input")
	}
	if cloned.Nodes[0].Contract.AllowedTools == nil || cloned.Nodes[1] != nil {
		t.Fatal("empty allowed tools or nil node semantics changed")
	}
}

func TestCloneRejectsCircularAndUnsupportedValues(t *testing.T) {
	circularMap := map[string]any{}
	circularMap["self"] = circularMap
	circularSlice := make([]any, 1)
	circularSlice[0] = circularSlice
	type link struct{ Next *link }
	circularPointer := &link{}
	circularPointer.Next = circularPointer
	type privateValue struct{ labels []string }
	cases := map[string]any{
		"map cycle":     circularMap,
		"slice cycle":   circularSlice,
		"pointer cycle": circularPointer,
		"function":      func() {},
		"channel":       make(chan int),
		"private field": privateValue{labels: []string{"hidden"}},
		"object key":    map[*int]string{new(int): "identity"},
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			plan := &models.PlanGraph{Nodes: []*models.TaskNode{{Inputs: map[string]any{"bad": value}}}}
			cloned, err := clonePlanGraph(plan)
			if err == nil || cloned != nil {
				t.Fatal("invalid value must fail without returning a partial plan")
			}
			if !strings.Contains(err.Error(), "Inputs[bad]") {
				t.Fatalf("error must identify the failing field: %v", err)
			}
		})
	}
}

func TestClonePlanGraphNil(t *testing.T) {
	cloned, err := clonePlanGraph(nil)
	if err != nil || cloned != nil {
		t.Fatalf("nil plan: %v, %v", cloned, err)
	}
}
