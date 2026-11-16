package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

// A capability failure comes from the service before running untrusted code.
// Wrap it once to prove that the research loops preserve the typed cause.
type capabilityGateSandbox struct {
	inner   researchCodingSandbox
	failAt  int32
	calls   atomic.Int32
	onBlock func()
}

func syntheticResearchCapabilityError() error {
	return fmt.Errorf("execution refused: %w", &sandbox.CapabilityError{
		Message: "sandbox_capability: synthetic isolation unavailable",
		MissingRequirements: []models.MissingRequirement{{
			Kind: "sandbox_capability", Name: "native",
			Detail: "synthetic isolation unavailable", Remediation: "revalidate the configured runtime",
		}},
	})
}

func (s *capabilityGateSandbox) ExecCommandStream(ctx context.Context, id string, cmd []string, onChunk func(string, string)) (*sandbox.PythonRunResponse, error) {
	if s.calls.Add(1) >= s.failAt {
		if s.onBlock != nil {
			s.onBlock()
		}
		return nil, syntheticResearchCapabilityError()
	}
	return s.inner.ExecCommandStream(ctx, id, cmd, onChunk)
}

func requireResearchCapabilityBlocked(t *testing.T, task *models.Task, err error) {
	t.Helper()
	if err != nil || task.Status != models.StatusBlocked || task.Failure != nil {
		t.Fatalf("capability error did not remain blocked: status=%s failure=%#v err=%v task.error=%s", task.Status, task.Failure, err, task.Error)
	}
	requirements, ok := task.Metadata["missing_requirements"].([]models.MissingRequirement)
	if !ok || len(requirements) != 1 || requirements[0].Name != "native" || !strings.Contains(task.Error, "synthetic isolation unavailable") {
		t.Fatalf("capability requirements were lost: %#v error=%s", task.Metadata, task.Error)
	}
}

func TestResearchCapabilityBlocksBenchmarkWithoutRepair(t *testing.T) {
	workspace, manifest, spec, code := benchmarkHarnessFixture(t)
	manifestJSON, _ := json.Marshal(manifest)
	specJSON, _ := json.Marshal(spec)
	gate := &capabilityGateSandbox{failAt: 1}
	a := &ResearchCodingAgent{Name: "research_coding_agent", ChatModel: newSequentialBenchmarkModel(t), Sandbox: gate}
	task := &models.Task{Type: "benchmark_adapter_preflight", Inputs: map[string]any{
		"workspace_path": workspace, "dataset_manifest": string(manifestJSON),
		"benchmark_adapter_spec": string(specJSON), "benchmark_generated_code": code,
		"prepared_runtime": "nt-fixture", "benchmark_max_preflight_attempts": 3,
	}}
	requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
	if gate.calls.Load() != 1 {
		t.Fatalf("blocked preflight retried: %d calls", gate.calls.Load())
	}
}

func TestResearchCapabilityBlocksPaperWithoutRepair(t *testing.T) {
	_, task := paperDebugTaskFixture(t, "raise RuntimeError('BROKEN')\n")
	task.Inputs["prepared_runtime"] = "nt-fixture"
	gate := &capabilityGateSandbox{failAt: 1}
	a := &ResearchCodingAgent{Name: "research_coding_agent", ChatModel: newPaperDebugModel(t), Sandbox: gate}
	requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
	var report paperCodeDebugReport
	if gate.calls.Load() != 1 || json.Unmarshal([]byte(task.Result), &report) != nil || report.Status != "blocked" || len(report.Patches) != 0 {
		t.Fatalf("blocked paper execution entered repair: calls=%d report=%s", gate.calls.Load(), task.Result)
	}
}

func TestResearchCapabilityBlocksPaperAfterRepairAndRestoresSource(t *testing.T) {
	original := "raise RuntimeError('BROKEN')\n"
	workspace, task := paperDebugTaskFixture(t, original)
	proposal, _ := json.Marshal(map[string]any{
		"status": "patched", "diagnosis": "remove unconditional fixture error",
		"patches": []map[string]string{{"path": "main.py", "content": "print('ok')\n", "reason": "fixture repair"}},
	})
	gate := &capabilityGateSandbox{failAt: 2, inner: &scriptedPaperDebugSandbox{workspace: workspace}}
	a := &ResearchCodingAgent{Name: "research_coding_agent", ChatModel: newPaperDebugModel(t, string(proposal)), Sandbox: gate}
	requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
	source, err := os.ReadFile(filepath.Join(workspace, "main.py"))
	if err != nil || string(source) != original || gate.calls.Load() != 2 {
		t.Fatalf("blocked repaired source was not restored: calls=%d source=%q err=%v", gate.calls.Load(), source, err)
	}
}

func TestResearchCapabilityBlocksAutoResearchBaselineAndCandidate(t *testing.T) {
	for _, failAt := range []int32{1, 2} {
		t.Run(fmt.Sprintf("call_%d", failAt), func(t *testing.T) {
			workspace, frozenSpec := autoResearchFixture(t, 3)
			responses := []string{}
			if failAt == 2 {
				responses = append(responses, autoResearchProposal(t, 0.8, "one candidate before isolation is unavailable"))
			}
			gate := &capabilityGateSandbox{failAt: failAt, inner: &scriptedAutoResearchSandbox{workspace: workspace}}
			a := &ResearchCodingAgent{Name: "research_coding_agent", ChatModel: newAutoResearchModel(t, responses...), Sandbox: gate}
			task := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
				"workspace_path": workspace, "prepared_runtime": "nt-fixture", "research_spec": frozenSpec,
			}}
			requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
			var ledger models.ResearchTrialLedger
			if json.Unmarshal([]byte(task.Result), &ledger) != nil || ledger.Status != "blocked" || ledger.StopReason != "sandbox_capability" || gate.calls.Load() != failAt {
				t.Fatalf("blocked research run retried/rejected candidate: calls=%d ledger=%s", gate.calls.Load(), task.Result)
			}
			source, err := os.ReadFile(filepath.Join(workspace, "candidate.py"))
			if err != nil || string(source) != "SCORE = 0.5\n" {
				t.Fatalf("blocked candidate was not restored to best source: %q err=%v", source, err)
			}
		})
	}
}

func TestResearchCapabilityStopsAutoResearchValidation(t *testing.T) {
	workspace, frozenSpec := autoResearchFixtureWithValidationRuns(t, 1, 3)
	a := &ResearchCodingAgent{
		Name: "research_coding_agent", ChatModel: newAutoResearchModel(t, autoResearchProposal(t, 0.8, "fixture improvement")),
		Sandbox: &scriptedAutoResearchSandbox{workspace: workspace},
	}
	run := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "nt-fixture", "research_spec": frozenSpec,
	}}
	if err := a.ExecuteTask(t.Context(), run, nil); err != nil || run.Status != models.StatusCompleted {
		t.Fatalf("prepare valid research artifacts: %v status=%s error=%s", err, run.Status, run.Error)
	}
	artifacts := run.Metadata["artifact_values"].(map[string]string)
	gate := &capabilityGateSandbox{failAt: 1}
	a.Sandbox = gate
	validate := &models.Task{Type: "autoresearch_validate", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "nt-fixture", "research_spec": frozenSpec,
		"research_trial_ledger": artifacts["research_trial_ledger"], "research_best_candidate": artifacts["research_best_candidate"],
	}}
	requireResearchCapabilityBlocked(t, validate, a.ExecuteTask(t.Context(), validate, nil))
	if gate.calls.Load() != 1 {
		t.Fatalf("blocked validation continued replay: %d calls", gate.calls.Load())
	}
}

func TestResearchCapabilitySurvivesConcurrentIntegrityFailure(t *testing.T) {
	t.Run("benchmark", func(t *testing.T) {
		workspace, manifest, spec, code := benchmarkHarnessFixture(t)
		manifestJSON, _ := json.Marshal(manifest)
		specJSON, _ := json.Marshal(spec)
		gate := &capabilityGateSandbox{failAt: 1, onBlock: func() {
			if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("concurrent change"), 0o600); err != nil {
				t.Error(err)
			}
		}}
		a := &ResearchCodingAgent{Name: "research_coding_agent", ChatModel: newSequentialBenchmarkModel(t), Sandbox: gate}
		task := &models.Task{Type: "benchmark_adapter_preflight", Inputs: map[string]any{
			"workspace_path": workspace, "dataset_manifest": string(manifestJSON),
			"benchmark_adapter_spec": string(specJSON), "benchmark_generated_code": code, "prepared_runtime": "nt-fixture",
		}}
		requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
	})
	t.Run("paper", func(t *testing.T) {
		workspace, task := paperDebugTaskFixture(t, "print('original')\n")
		gate := &capabilityGateSandbox{failAt: 1, onBlock: func() {
			if err := os.WriteFile(filepath.Join(workspace, "main.py"), []byte("print('concurrent change')\n"), 0o600); err != nil {
				t.Error(err)
			}
		}}
		a := &ResearchCodingAgent{Name: "research_coding_agent", ChatModel: newPaperDebugModel(t), Sandbox: gate}
		requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
	})
	t.Run("autoresearch", func(t *testing.T) {
		workspace, frozenSpec := autoResearchFixture(t, 3)
		gate := &capabilityGateSandbox{failAt: 1, onBlock: func() {
			if err := os.WriteFile(filepath.Join(workspace, "evaluator.py"), []byte("concurrent change"), 0o600); err != nil {
				t.Error(err)
			}
		}}
		a := &ResearchCodingAgent{Name: "research_coding_agent", ChatModel: newAutoResearchModel(t), Sandbox: gate}
		task := &models.Task{Type: "autoresearch_run", Inputs: map[string]any{
			"workspace_path": workspace, "prepared_runtime": "nt-fixture", "research_spec": frozenSpec,
		}}
		requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
		protected, err := os.ReadFile(filepath.Join(workspace, "evaluator.py"))
		if err != nil || string(protected) != "# frozen evaluator\n" {
			t.Fatalf("blocked evaluation lost protected restoration: %q err=%v", protected, err)
		}
	})
	t.Run("experiment", func(t *testing.T) {
		workspace, frozen := experimentCapabilityFixture(t, 1)
		var spec models.ExperimentResearchSpec
		if err := json.Unmarshal([]byte(frozen), &spec); err != nil || len(spec.FrozenFiles) == 0 {
			t.Fatalf("missing frozen files: %v", err)
		}
		gate := &capabilityGateSandbox{failAt: 1, onBlock: func() {
			if err := os.WriteFile(filepath.Join(workspace, filepath.FromSlash(spec.FrozenFiles[0].Path)), []byte("concurrent change"), 0o600); err != nil {
				t.Error(err)
			}
		}}
		a := &ResearchCodingAgent{Name: "research_coding_agent", Sandbox: gate}
		task := &models.Task{Type: "experiment_run", Inputs: map[string]any{
			"workspace_path": workspace, "prepared_runtime": "nt-fixture", "experiment_spec": frozen,
		}}
		requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
	})
}

func experimentCapabilityFixture(t *testing.T, parallel int) (string, string) {
	t.Helper()
	corpus, queries := filepath.Join(t.TempDir(), "corpus.csv"), filepath.Join(t.TempDir(), "queries.csv")
	writeExperimentCSV(t, corpus, []string{"doc_id", "text", "links"}, [][]string{
		{"d1", "pump alarm", "d2"}, {"d2", "pressure valve", "d1"}, {"d3", "credentials", ""},
	})
	writeExperimentCSV(t, queries, []string{"query_id", "query", "relevant_doc_ids", "split"}, [][]string{
		{"q1", "pump alarm", "d1", "search"}, {"q2", "pressure", "d2", "search"},
		{"q3", "credentials", "d3", "holdout"}, {"q4", "valve", "d2", "holdout"},
	})
	a := &ResearchCodingAgent{Name: "research_coding_agent", Optimizer: &scriptedResearchOptimizer{}}
	prepare := &models.Task{Type: "experiment_dataset_prepare", Inputs: map[string]any{
		"uploaded_files":  []map[string]any{{"name": "corpus.csv", "storage_path": corpus}, {"name": "queries.csv", "storage_path": queries}},
		"research_domain": "retrieval", "experiment_adapter": "retrieval.v1",
	}}
	if err := a.ExecuteTask(t.Context(), prepare, nil); err != nil {
		t.Fatal(err)
	}
	artifacts := prepare.Metadata["artifact_values"].(map[string]string)
	workspace := artifacts["workspace_path"]
	if !filepath.IsAbs(workspace) || !strings.EqualFold(filepath.Dir(workspace), filepath.Clean(os.TempDir())) || !strings.HasPrefix(filepath.Base(workspace), "research-assistant-experiment-") {
		t.Fatalf("unexpected generated experiment workspace: %q", workspace)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	freeze := &models.Task{Type: "experiment_spec", Inputs: map[string]any{
		"workspace_path": workspace, "experiment_dataset_manifest": artifacts["experiment_dataset_manifest"],
		"experiment_max_trials": 3, "experiment_max_wall_seconds": 60, "experiment_validation_runs": 3,
		"experiment_max_parallel_trials": parallel,
	}}
	if err := a.ExecuteTask(t.Context(), freeze, nil); err != nil {
		t.Fatal(err)
	}
	var spec models.ExperimentResearchSpec
	frozen := freeze.Metadata["artifact_values"].(map[string]string)["experiment_spec"]
	if err := json.Unmarshal([]byte(frozen), &spec); err != nil || spec.MaxParallelTrials != parallel {
		t.Fatalf("experiment parallel fixture was not applied: %#v err=%v", spec, err)
	}
	return workspace, frozen
}

func TestResearchCapabilityBlocksExperimentBaselineAndCandidate(t *testing.T) {
	for _, failAt := range []int32{1, 2} {
		t.Run(fmt.Sprintf("call_%d", failAt), func(t *testing.T) {
			workspace, frozen := experimentCapabilityFixture(t, 1)
			gate := &capabilityGateSandbox{failAt: failAt, inner: &scriptedExperimentSandbox{workspace: workspace}}
			a := &ResearchCodingAgent{Name: "research_coding_agent", Sandbox: gate}
			task := &models.Task{Type: "experiment_run", Inputs: map[string]any{
				"workspace_path": workspace, "prepared_runtime": "nt-fixture", "experiment_spec": frozen,
			}}
			requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
			var ledger models.ExperimentTrialLedger
			if json.Unmarshal([]byte(task.Result), &ledger) != nil || ledger.Status != "blocked" || ledger.StopReason != "sandbox_capability" || gate.calls.Load() != failAt {
				t.Fatalf("blocked experiment continued: calls=%d ledger=%s", gate.calls.Load(), task.Result)
			}
		})
	}
}

func TestResearchCapabilityStopsExperimentValidation(t *testing.T) {
	workspace, frozen := experimentCapabilityFixture(t, 1)
	a := &ResearchCodingAgent{Name: "research_coding_agent", Sandbox: &scriptedExperimentSandbox{workspace: workspace}}
	run := &models.Task{Type: "experiment_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "nt-fixture", "experiment_spec": frozen,
	}}
	if err := a.ExecuteTask(t.Context(), run, nil); err != nil || run.Status != models.StatusCompleted {
		t.Fatalf("prepare experiment artifacts: err=%v status=%s error=%s", err, run.Status, run.Error)
	}
	artifacts := run.Metadata["artifact_values"].(map[string]string)
	for _, failAt := range []int32{1, 2} {
		t.Run(fmt.Sprintf("call_%d", failAt), func(t *testing.T) {
			gate := &capabilityGateSandbox{failAt: failAt, inner: &scriptedExperimentSandbox{workspace: workspace}}
			a.Sandbox = gate
			task := &models.Task{Type: "experiment_validate", Inputs: map[string]any{
				"workspace_path": workspace, "prepared_runtime": "nt-fixture", "experiment_spec": frozen,
				"experiment_trial_ledger": artifacts["experiment_trial_ledger"], "experiment_best_candidate": artifacts["experiment_best_candidate"],
			}}
			requireResearchCapabilityBlocked(t, task, a.ExecuteTask(t.Context(), task, nil))
			if gate.calls.Load() != failAt {
				t.Fatalf("blocked experiment validation continued: %d calls", gate.calls.Load())
			}
		})
	}
}

type parallelCapabilityGateSandbox struct {
	inner        researchCodingSandbox
	calls        atomic.Int32
	cancelled    atomic.Int32
	otherStarted chan struct{}
}

func (s *parallelCapabilityGateSandbox) ExecCommandStream(ctx context.Context, id string, cmd []string, onChunk func(string, string)) (*sandbox.PythonRunResponse, error) {
	switch s.calls.Add(1) {
	case 1:
		return s.inner.ExecCommandStream(ctx, id, cmd, onChunk)
	case 2:
		select {
		case <-s.otherStarted:
			return nil, syntheticResearchCapabilityError()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	case 3:
		close(s.otherStarted)
		<-ctx.Done()
		s.cancelled.Add(1)
		return nil, ctx.Err()
	default:
		return nil, fmt.Errorf("unexpected evaluator after capability block")
	}
}

func TestResearchCapabilityCancelsOutstandingExperimentEvaluations(t *testing.T) {
	workspace, frozen := experimentCapabilityFixture(t, 2)
	gate := &parallelCapabilityGateSandbox{
		inner: &scriptedExperimentSandbox{workspace: workspace}, otherStarted: make(chan struct{}),
	}
	a := &ResearchCodingAgent{Name: "research_coding_agent", Sandbox: gate}
	task := &models.Task{Type: "experiment_run", Inputs: map[string]any{
		"workspace_path": workspace, "prepared_runtime": "nt-fixture", "experiment_spec": frozen,
	}}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	requireResearchCapabilityBlocked(t, task, a.ExecuteTask(ctx, task, nil))
	if gate.calls.Load() != 3 || gate.cancelled.Load() != 1 {
		t.Fatalf("parallel evaluator was not cancelled and drained: calls=%d cancelled=%d", gate.calls.Load(), gate.cancelled.Load())
	}
}
