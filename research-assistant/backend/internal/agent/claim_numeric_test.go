package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/claimverify"
	"research-assistant-backend/internal/models"
)

func claimPtr[T any](v T) *T { return &v }

func numericClaimFixture(t *testing.T, value float64) (*models.Task, *models.PlanGraph, models.ClaimRubric) {
	t.Helper()
	code, protocol, environment := "frozen evaluator code", `{"split":"test","seed":42,"preprocess":"frozen"}`, `{"python":"3.11","device":"cpu"}`
	var datasetRows strings.Builder
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&datasetRows, "{\"index\":%d,\"target\":\"correct\"}\n", i)
	}
	dataset := datasetRows.String()
	contract := &models.ClaimNumericContract{
		Version: claimverify.ContractVersion, Operator: "within_tolerance", ToleranceMode: "absolute", AbsoluteTolerance: claimPtr(.01), Inclusive: claimPtr(true),
		SourceTaskID: "measurement", MetricArtifact: "run_metrics", ExtractionMethod: "benchmark_predictions", PredictionTaskType: "classification", PredictionsArtifact: "predictions", DatasetArtifact: "dataset", DatasetSHA256: claimTextHash(dataset),
		Split: "test", ProtocolArtifact: "protocol", ProtocolSHA256: claimTextHash(protocol), CodeSHA256: claimTextHash(code), EnvironmentArtifact: "environment_manifest", EnvironmentSHA256: claimTextHash(environment), Seeds: []int{42}, Aggregation: "single",
	}
	rubric, err := normalizeClaimRubric(models.ClaimRubric{PaperTitle: "Frozen experiment", Claims: []models.PaperClaim{{Title: "Accuracy", Statement: "Test accuracy reaches 0.85", ClaimType: "quantitative", Criteria: []models.ClaimCriterion{{Description: "Evaluate frozen test accuracy", MetricName: "accuracy", ExpectedValue: claimPtr(.85), Unit: "fraction", RequiredEvidence: []string{"metric", "environment", "run"}, NumericContract: contract}}}}})
	if err != nil {
		t.Fatal(err)
	}
	frozen, started, finished := time.Unix(100, 0), time.Unix(200, 0), time.Unix(300, 0)
	plan := &models.PlanGraph{ID: "plan", OwnerID: "owner", CurrentRevision: 1, Nodes: []*models.TaskNode{
		{ID: "assets", Type: "prepare_runtime", Status: models.StatusCompleted, Revision: 1},
		{ID: "freeze", Type: "claim_rubric_extract", Status: models.StatusCompleted, Revision: 1},
		{ID: "measurement", Type: "paper_code_execute", Status: models.StatusCompleted, Revision: 1, ExecutionID: "exec-1", StartedAt: &started, FinishedAt: &finished, Code: code, Dependencies: []string{"assets", "freeze"}},
		{ID: "assessment", Type: "claim_evidence_build", Revision: 1, Dependencies: []string{"measurement"}},
	}, Artifacts: map[string]models.Artifact{}}
	for key, body := range map[string]string{"dataset": dataset, "protocol": protocol, "environment_manifest": environment} {
		plan.Artifacts[key] = models.Artifact{Key: key, Value: body, ProducerTaskID: "assets", Revision: 1, CreatedAt: frozen}
	}
	rubricJSON, _ := json.Marshal(rubric)
	plan.Artifacts["claim_rubric"] = models.Artifact{Key: "claim_rubric", Value: string(rubricJSON), ProducerTaskID: "freeze", Revision: 1, CreatedAt: frozen}
	output := models.MetricExecutionEvidence{Version: claimverify.ExecutionVersion, ExecutionID: "exec-1", DatasetSHA256: contract.DatasetSHA256, Split: "test", ProtocolSHA256: contract.ProtocolSHA256, CodeSHA256: contract.CodeSHA256, EnvironmentRef: "environment_manifest", EnvironmentSHA256: contract.EnvironmentSHA256, Runs: []models.MetricExecutionSample{{Seed: claimPtr(42), Unit: "fraction", Metrics: map[string]float64{"accuracy": value}}}}
	raw, _ := json.Marshal(output)
	plan.Artifacts["run_metrics"] = models.Artifact{Key: "run_metrics", Value: string(raw), ProducerTaskID: "measurement", Revision: 1, CreatedAt: finished}
	var predictions strings.Builder
	correct := int(value*1000 + .5)
	for i := 0; i < 1000; i++ {
		prediction := "wrong"
		if i < correct {
			prediction = "correct"
		}
		fmt.Fprintf(&predictions, "{\"seed\":42,\"index\":%d,\"prediction\":%q,\"target\":\"correct\"}\n", i, prediction)
	}
	plan.Artifacts["predictions"] = models.Artifact{Key: "predictions", Value: predictions.String(), ProducerTaskID: "measurement", Revision: 1, CreatedAt: finished}
	task := &models.Task{ID: "assessment", Type: "claim_evidence_build", Dependencies: []string{"measurement"}, Inputs: map[string]any{"claim_rubric": string(rubricJSON), "run_metrics": string(raw)}, Metadata: map[string]any{"plan_id": "plan", "tenant_id": "owner"}}
	return task, plan, rubric
}

func claimVerifiedProposal() string {
	return `{"findings":[{"claim_id":"claim-001","criterion_id":"claim-001.criterion-01","status":"verified","confidence":0.99,"observed_value":"MODEL CLAIMS 0.999","evidence_keys":["run_metrics"],"reason":"model says success"}]}`
}

func replaceClaimMetricOutput(t *testing.T, task *models.Task, plan *models.PlanGraph, mutate func(*models.MetricExecutionEvidence)) {
	t.Helper()
	var output models.MetricExecutionEvidence
	if err := json.Unmarshal([]byte(plan.Artifacts["run_metrics"].Value), &output); err != nil {
		t.Fatal(err)
	}
	mutate(&output)
	raw, err := json.Marshal(output)
	if err != nil {
		t.Fatal(err)
	}
	a := plan.Artifacts["run_metrics"]
	a.Value = string(raw)
	plan.Artifacts["run_metrics"] = a
	task.Inputs["run_metrics"] = string(raw)
}

func TestClaimNumericBackendVerdictOverridesModelAndCompletesNegativeResult(t *testing.T) {
	for _, value := range []float64{.847, .817} {
		t.Run(fmt.Sprint(value), func(t *testing.T) {
			task, plan, _ := numericClaimFixture(t, value)
			agent := &DataAgent{Name: "data", ChatModel: newTestChatModel(t, claimVerifiedProposal())}
			if err := agent.ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
				t.Fatal(err)
			}
			graph := decodeClaimEvidenceGraph(t, task.StructuredData)
			v := graph.Claims[0].Criteria[0]
			want := models.ClaimStatusVerified
			if value == .817 {
				want = models.ClaimStatusContradicted
			}
			if v.Status != want || graph.Claims[0].Status != want || v.DeterministicVerdict == nil || *v.DeterministicVerdict.ObservedValue != value {
				t.Fatalf("model overrode numeric evidence: %+v", v)
			}
			if v.ModelProposal == nil || v.ModelProposal.ObservedValue != "MODEL CLAIMS 0.999" || strings.Contains(v.ObservedValue, "MODEL") {
				t.Fatalf("model proposal and observation were conflated: %+v", v)
			}
			if task.Status != models.StatusCompleted || graph.Status != "assessed" {
				t.Fatalf("valid scientific result should complete normally: %s/%s", task.Status, graph.Status)
			}
			if v.DeterministicVerdict.TrustBoundary != "backend_recomputed_from_execution_artifacts" || v.DeterministicVerdict.ReviewRequired {
				t.Fatal("independent recomputation provenance was not retained")
			}
		})
	}
}

func TestClaimNumericCanAssessWithoutModel(t *testing.T) {
	task, plan, _ := numericClaimFixture(t, .847)
	if err := (&DataAgent{Name: "data"}).ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
		t.Fatal(err)
	}
	graph := decodeClaimEvidenceGraph(t, task.StructuredData)
	if graph.Status != "assessed" || graph.Claims[0].Status != models.ClaimStatusVerified {
		t.Fatalf("model availability changed deterministic result: %+v", graph)
	}
}

func TestQuantitativeClaimMissingNumericFieldsCannotUseModelSuccess(t *testing.T) {
	for _, claimType := range []string{"quantitative", "efficiency"} {
		t.Run(claimType, func(t *testing.T) {
			task, plan, rubric := numericClaimFixture(t, .847)
			rubric.Claims[0].ClaimType = claimType
			criterion := &rubric.Claims[0].Criteria[0]
			criterion.NumericContract, criterion.ExpectedValue, criterion.MetricName = nil, nil, ""
			rubric.SHA256 = claimRubricSHA256(rubric)
			payload, err := json.Marshal(rubric)
			if err != nil {
				t.Fatal(err)
			}
			task.Inputs["claim_rubric"] = string(payload)
			if err := (&DataAgent{Name: "data", ChatModel: newTestChatModel(t, claimVerifiedProposal())}).ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
				t.Fatal(err)
			}
			graph := decodeClaimEvidenceGraph(t, task.StructuredData)
			verdict := graph.Claims[0].Criteria[0]
			if verdict.Status != models.ClaimStatusUnverifiable || verdict.DeterministicVerdict == nil || verdict.ModelProposal == nil || verdict.ModelProposal.Status != models.ClaimStatusVerified || graph.Status != "degraded" {
				t.Fatalf("missing numeric contract allowed model success: %+v", verdict)
			}
		})
	}
}

func TestClaimNumericExternalReportedMetricRequiresReview(t *testing.T) {
	task, plan, rubric := numericClaimFixture(t, .847)
	c := rubric.Claims[0].Criteria[0].NumericContract
	c.ExtractionMethod, c.PredictionTaskType, c.PredictionsArtifact = "json_metric", "", ""
	rubric.SHA256 = claimRubricSHA256(rubric)
	raw, _ := json.Marshal(rubric)
	a := plan.Artifacts["claim_rubric"]
	a.Value = string(raw)
	plan.Artifacts["claim_rubric"] = a
	task.Inputs["claim_rubric"] = string(raw)
	if err := (&DataAgent{Name: "data"}).ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
		t.Fatal(err)
	}
	graph := decodeClaimEvidenceGraph(t, task.StructuredData)
	v := graph.Claims[0].Criteria[0]
	if v.Status != models.ClaimStatusUnverifiable || graph.Status != "degraded" || v.DeterministicVerdict.Status != models.ClaimStatusVerified || !v.DeterministicVerdict.ReviewRequired {
		t.Fatalf("parsed metric falsely gained independent-verification status: %+v", v)
	}
}

func TestClaimRecomputedMetricRejectsTargetForgeryAndIncompleteSplit(t *testing.T) {
	for _, body := range []string{
		"{\"seed\":42,\"index\":0,\"prediction\":\"wrong\",\"target\":\"wrong\"}\n",
		"{\"seed\":42,\"index\":0,\"prediction\":\"correct\"}\n",
	} {
		task, plan, _ := numericClaimFixture(t, .847)
		a := plan.Artifacts["predictions"]
		a.Value = body
		plan.Artifacts["predictions"] = a
		if err := (&DataAgent{Name: "data"}).ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
			t.Fatal(err)
		}
		if v := decodeClaimEvidenceGraph(t, task.StructuredData).Claims[0].Criteria[0]; v.Status != models.ClaimStatusUnverifiable {
			t.Fatalf("forged targets or incomplete split gained verification: %+v", v)
		}
	}
}

func TestClaimNumericUsesCompletedExecutionStampAfterSchedulerClearsLease(t *testing.T) {
	task, plan, _ := numericClaimFixture(t, .847)
	run := plan.Nodes[2]
	run.ExecutionID, run.ExecutionEpoch = "", 3
	run.Metadata = map[string]any{"completed_execution_id": "exec-1", "completed_execution_epoch": float64(3), "completed_revision": float64(1)}
	if err := (&DataAgent{Name: "data"}).ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
		t.Fatal(err)
	}
	if v := decodeClaimEvidenceGraph(t, task.StructuredData).Claims[0].Criteria[0]; v.Status != models.ClaimStatusVerified {
		t.Fatalf("scheduler completion stamp was not accepted: %+v", v)
	}
	run.Metadata["completed_execution_epoch"] = 2
	if err := (&DataAgent{Name: "data"}).ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
		t.Fatal(err)
	}
	if v := decodeClaimEvidenceGraph(t, task.StructuredData).Claims[0].Criteria[0]; v.Status != models.ClaimStatusUnverifiable {
		t.Fatalf("old execution epoch was accepted: %+v", v)
	}
}

func TestClaimNumericMissingProtocolOrProvenanceNeverVerifiesOrContradicts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*models.Task, *models.PlanGraph)
	}{
		{"model_only_no_plan", func(_ *models.Task, p *models.PlanGraph) { p.ID = "" }},
		{"missing_execution", func(_ *models.Task, p *models.PlanGraph) { p.Nodes[2].ExecutionID = "" }},
		{"failed_execution", func(_ *models.Task, p *models.PlanGraph) { p.Nodes[2].Status = models.StatusFailed }},
		{"non_execution_producer", func(_ *models.Task, p *models.PlanGraph) { p.Nodes[2].Type = "general_process" }},
		{"wrong_split", func(task *models.Task, p *models.PlanGraph) {
			replaceClaimMetricOutput(t, task, p, func(o *models.MetricExecutionEvidence) { o.Split = "train" })
		}},
		{"wrong_protocol", func(task *models.Task, p *models.PlanGraph) {
			replaceClaimMetricOutput(t, task, p, func(o *models.MetricExecutionEvidence) { o.ProtocolSHA256 = strings.Repeat("b", 64) })
		}},
		{"missing_seed", func(task *models.Task, p *models.PlanGraph) {
			replaceClaimMetricOutput(t, task, p, func(o *models.MetricExecutionEvidence) { o.Runs[0].Seed = nil })
		}},
		{"missing_original", func(_ *models.Task, p *models.PlanGraph) { delete(p.Artifacts, "dataset") }},
		{"wrong_dataset_original", func(_ *models.Task, p *models.PlanGraph) {
			a := p.Artifacts["dataset"]
			a.Value += "changed"
			p.Artifacts["dataset"] = a
		}},
		{"missing_executed_code", func(_ *models.Task, p *models.PlanGraph) { p.Nodes[2].Code = "" }},
		{"wrong_owner", func(task *models.Task, _ *models.PlanGraph) { task.Metadata["tenant_id"] = "other-owner" }},
		{"late_rubric", func(_ *models.Task, p *models.PlanGraph) {
			a := p.Artifacts["claim_rubric"]
			a.CreatedAt = time.Unix(250, 0)
			p.Artifacts["claim_rubric"] = a
		}},
		{"stale_revision", func(_ *models.Task, p *models.PlanGraph) {
			a := p.Artifacts["run_metrics"]
			a.Revision = 0
			p.Artifacts["run_metrics"] = a
		}},
		{"stale_metric_from_previous_epoch", func(task *models.Task, p *models.PlanGraph) {
			replaceClaimMetricOutput(t, task, p, func(o *models.MetricExecutionEvidence) { o.ExecutionID = "" })
			a := p.Artifacts["run_metrics"]
			a.CreatedAt = time.Unix(150, 0)
			p.Artifacts["run_metrics"] = a
		}},
		{"stale_predictions_from_previous_epoch", func(_ *models.Task, p *models.PlanGraph) {
			a := p.Artifacts["predictions"]
			a.CreatedAt = time.Unix(150, 0)
			p.Artifacts["predictions"] = a
		}},
		{"forged_input", func(task *models.Task, _ *models.PlanGraph) {
			task.Inputs["run_metrics"] = `{"accuracy":0.85,"extraction_method":"backend_recomputed"}`
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task, plan, _ := numericClaimFixture(t, .817)
			tc.mutate(task, plan)
			if err := (&DataAgent{Name: "data", ChatModel: newTestChatModel(t, claimVerifiedProposal())}).ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
				t.Fatal(err)
			}
			graph := decodeClaimEvidenceGraph(t, task.StructuredData)
			if v := graph.Claims[0].Criteria[0]; v.Status != models.ClaimStatusUnverifiable || v.DeterministicVerdict == nil {
				t.Fatalf("untrusted evidence produced a scientific verdict: %+v", v)
			}
			if graph.Status != "degraded" || task.Status != models.StatusCompleted {
				t.Fatalf("missing proof must produce a reviewable graph: %s/%s", graph.Status, task.Status)
			}
		})
	}
}

func TestClaimNumericRecomputesBenchmarkPredictionsInsteadOfReportedMetric(t *testing.T) {
	for _, correct := range []int{17, 16} {
		t.Run(fmt.Sprint(correct), func(t *testing.T) {
			task, plan, rubric := numericClaimFixture(t, .999)
			c := rubric.Claims[0].Criteria[0].NumericContract
			c.ExtractionMethod, c.PredictionTaskType, c.PredictionsArtifact = "benchmark_predictions", "classification", "predictions"
			var dataset strings.Builder
			for i := 0; i < 20; i++ {
				fmt.Fprintf(&dataset, "{\"index\":%d,\"target\":\"correct\"}\n", i)
			}
			c.DatasetSHA256 = claimTextHash(dataset.String())
			datasetArtifact := plan.Artifacts["dataset"]
			datasetArtifact.Value = dataset.String()
			plan.Artifacts["dataset"] = datasetArtifact
			replaceClaimMetricOutput(t, task, plan, func(o *models.MetricExecutionEvidence) { o.DatasetSHA256 = c.DatasetSHA256 })
			rubric.SHA256 = claimRubricSHA256(rubric)
			raw, _ := json.Marshal(rubric)
			a := plan.Artifacts["claim_rubric"]
			a.Value = string(raw)
			plan.Artifacts["claim_rubric"] = a
			task.Inputs["claim_rubric"] = string(raw)
			var predictions strings.Builder
			for i := 0; i < 20; i++ {
				prediction := "wrong"
				if i < correct {
					prediction = "correct"
				}
				fmt.Fprintf(&predictions, "{\"seed\":42,\"index\":%d,\"prediction\":%q,\"target\":\"correct\"}\n", i, prediction)
			}
			predictionArtifact := plan.Artifacts["predictions"]
			predictionArtifact.Value = predictions.String()
			plan.Artifacts["predictions"] = predictionArtifact
			if err := (&DataAgent{Name: "data", ChatModel: newTestChatModel(t, claimVerifiedProposal())}).ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
				t.Fatal(err)
			}
			v := decodeClaimEvidenceGraph(t, task.StructuredData).Claims[0].Criteria[0]
			want := models.ClaimStatusVerified
			if correct == 16 {
				want = models.ClaimStatusContradicted
			}
			if v.Status != want || len(v.Observations) != 1 || v.Observations[0].ExtractionMethod != "backend_recomputed" || v.Observations[0].Value != float64(correct)/20 {
				t.Fatalf("reported metric bypassed recomputation: %+v", v)
			}
		})
	}
}

func TestNumericSuccessDoesNotVerifyMixedClaimWithoutQualitativeEvidence(t *testing.T) {
	task, plan, rubric := numericClaimFixture(t, .847)
	rubric.Claims[0].Criteria = append(rubric.Claims[0].Criteria, models.ClaimCriterion{ID: "claim-001.criterion-02", Description: "Reproduce the qualitative figure", RequiredEvidence: []string{"figure"}})
	rubric.SHA256 = claimRubricSHA256(rubric)
	raw, _ := json.Marshal(rubric)
	a := plan.Artifacts["claim_rubric"]
	a.Value = string(raw)
	plan.Artifacts["claim_rubric"] = a
	task.Inputs["claim_rubric"] = string(raw)
	var proposal claimEvidenceProposal
	_ = json.Unmarshal([]byte(claimVerifiedProposal()), &proposal)
	proposal.Findings = append(proposal.Findings, claimCriterionFinding{ClaimID: "claim-001", CriterionID: "claim-001.criterion-02", Status: "verified", Confidence: 1, EvidenceKeys: []string{"run_metrics"}})
	proposalRaw, _ := json.Marshal(proposal)
	if err := (&DataAgent{Name: "data", ChatModel: newTestChatModel(t, string(proposalRaw))}).ExecuteTask(t.Context(), task, map[string]interface{}{"plan_graph": plan}); err != nil {
		t.Fatal(err)
	}
	claim := decodeClaimEvidenceGraph(t, task.StructuredData).Claims[0]
	if claim.Status != models.ClaimStatusPartiallyReproduced || claim.Criteria[1].Status != models.ClaimStatusUnverifiable {
		t.Fatalf("numeric success falsely verified a mixed claim: %+v", claim)
	}
}

func TestFrozenNumericContractBindingChangesHashAndSurvivesCanonicalReload(t *testing.T) {
	_, _, rubric := numericClaimFixture(t, .847)
	contract := *rubric.Claims[0].Criteria[0].NumericContract
	rubric.Claims[0].Criteria[0].NumericContract = nil
	rubric.SHA256 = claimRubricSHA256(rubric)
	old := rubric.SHA256
	task := &models.Task{Inputs: map[string]any{"claim_numeric_contracts": map[string]models.ClaimNumericContract{"claim-001.criterion-01": contract}}}
	if err := bindFrozenClaimNumericContracts(task, &rubric); err != nil {
		t.Fatal(err)
	}
	if rubric.SHA256 == old || rubric.SHA256 != claimRubricSHA256(rubric) {
		t.Fatal("numeric protocol was not covered by the frozen rubric hash")
	}
	raw, _ := json.Marshal(rubric)
	if _, _, err := claimRubricFromTask(&models.Task{Inputs: map[string]any{"claim_rubric": string(raw)}}); err != nil {
		t.Fatalf("canonical numeric rubric could not be restored: %v", err)
	}
}
