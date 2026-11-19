package memoryeval

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"research-assistant-backend/internal/modelprofile"
)

func TestFrozenPilotUsesActualRequestsAndIsolatesArms(t *testing.T) {
	t.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	server := httptest.NewServer(http.HandlerFunc(MockHandler))
	defer server.Close()
	call := modelprofile.Resolved{APIKey: "mock", Profile: modelprofile.Profile{BaseURL: server.URL, Model: "deterministic-mock", Revision: 1, ContextWindowTokens: 6000, MaxOutputTokens: 768, ContextPolicy: modelprofile.ContextPolicy{APIFormat: modelprofile.APIChat, CompactionMode: modelprofile.CompactionText, CompactionTriggerRatio: .55, TextSummaryTargetRatio: .15}}}
	out := t.TempDir()
	report, err := Run(context.Background(), Pilot, Options{Call: call, Repetitions: 2, OutputDir: out, ProviderKind: "deterministic_mock"})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Samples) != 72 || len(report.Pairs) != 2 {
		t.Fatal("unpaired report")
	}
	compactions := 0
	for _, sample := range report.Samples {
		if sample.Context.Requests.Compaction > 0 {
			compactions++
		}
		if sample.Arm == "C" && !sample.Passed {
			t.Errorf("memory arm failed %s: %s %s", sample.TaskID, sample.Error, sample.Answer)
		}
		if sample.Family == "reuse" && sample.Arm != "C" && sample.Passed {
			t.Error("memory seed leaked into disabled arm")
		}
		if sample.Family == "isolation" && sample.NoAnswerFalseRecall {
			t.Error("invisible source leaked")
		}
		if sample.CostUSD != nil {
			t.Error("invented provider pricing")
		}
	}
	if compactions == 0 {
		t.Error("pilot failed to exercise compression")
	}
	raw, err := os.ReadFile(filepath.Join(out, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved Report
	if json.Unmarshal(raw, &saved) != nil || saved.SuiteSHA256 != Digest(Pilot) || len(saved.Samples) != 72 {
		t.Fatal("report not complete")
	}
}
