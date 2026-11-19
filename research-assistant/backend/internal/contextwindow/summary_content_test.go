package contextwindow

import (
	"encoding/json"
	"strings"
	"testing"
)

func groundedSummary(category, text, id string) []byte {
	summary := map[string]any{"version": "context.summary/v1", "goal": "historical context", "constraints": []any{}, "facts": []any{}, "decisions": []any{}, "attempts": []any{}, "hypotheses": []any{}, "open_questions": []string{}, "next_steps": []string{}, "artifact_refs": []string{}}
	summary[category] = []summaryEntry{{Text: text, SourceIDs: []string{id}}}
	raw, _ := json.Marshal(summary)
	return raw
}

func TestSummaryContentRejectsCorrectIDWithUnsupportedClaim(t *testing.T) {
	sources := map[string]string{
		"old":        "B23 的 macro-F1 为 0.58",
		"new":        "纠正：B23 的 macro-F1 为 0.61，旧值 0.58 作废",
		"negative":   "GPU 上 batch=128 没有通过验证",
		"hypothesis": "假设：B24 的 macro-F1 可能达到 0.85，尚未验证",
		"experiment": "B25 的 macro-F1 为 0.85",
		"structured": "experiment=B26 dataset=corpus-v7 split=test metric=macro-F1 aggregation=mean value=58 unit=percent status=observed",
	}
	ids := []string{"old", "negative", "hypothesis", "experiment", "structured", "new"}
	for _, test := range []struct {
		name, category, text, id string
		valid                    bool
	}{
		{"wrong_number", "facts", "B23 的 macro-F1 为 0.85", "old", false},
		{"negative_flip", "facts", "GPU 上 batch=128 通过验证", "negative", false},
		{"negative_removed", "facts", "通过验证", "negative", false},
		{"old_over_correction", "facts", sources["old"], "old", false},
		{"mixed_experiments", "facts", "B23 的 macro-F1 为 0.85", "experiment", false},
		{"hypothesis_promoted", "facts", sources["hypothesis"], "hypothesis", false},
		{"unit_changed", "facts", strings.ReplaceAll(sources["structured"], "unit=percent", "unit=ratio"), "structured", false},
		{"corrected", "facts", sources["new"], "new", true},
		{"negative_preserved", "constraints", sources["negative"], "negative", true},
		{"hypothesis_preserved", "hypotheses", sources["hypothesis"], "hypothesis", true},
		{"structured", "facts", sources["structured"], "structured", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSummaryContent(groundedSummary(test.category, test.text, test.id), ids, sources)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
	anchors := ExtractCriticalAnchors(ids, sources)
	if len(anchors) != 6 || anchors[4].Fields["dataset"] != "corpus-v7" || anchors[5].Status != "correction" {
		t.Fatalf("lost original coordinates: %#v", anchors)
	}
}

func TestMaterialSelectionReservesVerbatimAndReportsOmissions(t *testing.T) {
	optional := Material{ID: "optional", Retention: "retrievable", Text: strings.Repeat("研", 1000)}
	required := Material{ID: "document", Retention: "verbatim", Text: "source excerpt"}
	selected, reports, fits := SelectMaterials([]Material{optional, required}, 200)
	if !fits || len(selected) != 1 || selected[0].ID != "document" || reports[0].Selected || !reports[1].Selected {
		t.Fatalf("bad selection: %#v %#v %v", selected, reports, fits)
	}
	if _, _, fits := SelectMaterials([]Material{required}, 1); fits {
		t.Fatal("required evidence silently dropped")
	}
}

func TestSummaryRejectsUnvalidatedExtraFields(t *testing.T) {
	raw := groundedSummary("facts", "observed", "source")
	for _, mutated := range []string{strings.Replace(string(raw), `"goal":`, `"unvalidated_instruction":"invented", "goal":`, 1), strings.Replace(string(raw), `"text":"observed"`, `"text":"observed","unvalidated_fact":"invented"`, 1)} {
		if err := ValidateSummaryContent([]byte(mutated), []string{"source"}, map[string]string{"source": "observed"}); err == nil {
			t.Fatal("unknown summary field reached the model")
		}
	}
}
