package agent

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"

	"research-assistant-backend/internal/claimverify"
	"research-assistant-backend/internal/models"
)

type claimNumericAssessment struct {
	verdict      models.DeterministicClaimVerdict
	observations []models.MetricObservation
}

// Administrators/callers may supply explicit protocols before extraction;
// these are bound before the rubric hash exists, never after measuring a run.
func bindFrozenClaimNumericContracts(task *models.Task, rubric *models.ClaimRubric) error {
	raw, ok := task.Inputs["claim_numeric_contracts"]
	if !ok {
		return nil
	}
	payload, err := json.Marshal(raw)
	if text, isString := raw.(string); isString {
		payload = []byte(text)
	}
	if err != nil {
		return fmt.Errorf("numeric contract configuration cannot be serialized")
	}
	var contracts map[string]models.ClaimNumericContract
	if json.Unmarshal(payload, &contracts) != nil {
		return fmt.Errorf("claim_numeric_contracts must map stable criterion IDs to frozen contracts")
	}
	for claimIndex := range rubric.Claims {
		for criterionIndex := range rubric.Claims[claimIndex].Criteria {
			id := fmt.Sprintf("claim-%03d.criterion-%02d", claimIndex+1, criterionIndex+1)
			if contract, found := contracts[id]; found {
				criterion := &rubric.Claims[claimIndex].Criteria[criterionIndex]
				criterion.NumericContract = &contract
				if err := claimverify.ValidateContract(*criterion); err != nil {
					return fmt.Errorf("invalid numeric contract %s: %w", id, err)
				}
				delete(contracts, id)
			}
		}
	}
	if len(contracts) > 0 {
		return fmt.Errorf("numeric contracts reference an unknown criterion")
	}
	rubric.SHA256 = claimRubricSHA256(*rubric)
	return nil
}

func appendNumericClaimEvidence(evidence []models.ClaimEvidenceNode, values map[string]string, numeric map[string]claimNumericAssessment, shared map[string]interface{}) ([]models.ClaimEvidenceNode, map[string]string) {
	plan, _ := shared["plan_graph"].(*models.PlanGraph)
	if plan == nil {
		return evidence, values
	}
	sourceHashes := map[string]string{}
	for _, assessment := range numeric {
		for _, observation := range assessment.observations {
			for key, hash := range observation.SourceArtifacts {
				sourceHashes[key] = hash
			}
		}
	}
	keys := make([]string, 0, len(sourceHashes))
	for key := range sourceHashes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		body := plan.Artifacts[key].EffectiveValue()
		values[key] = body
		node := models.ClaimEvidenceNode{ID: "evidence-" + strings.ReplaceAll(key, "_", "-"), ArtifactKey: key, EvidenceType: claimEvidenceType(key), SHA256: sourceHashes[key], Available: true, Summary: "original verification source; numeric verifier retained its hash; content is not a model observation"}
		if key == "environment_manifest" {
			node.EvidenceType = "environment"
		}
		replaced := false
		for index := range evidence {
			if evidence[index].ArtifactKey == key {
				evidence[index] = node
				replaced = true
				break
			}
		}
		if !replaced {
			evidence = append(evidence, node)
		}
	}
	return evidence, values
}

// numericClaimAssessments accepts provenance only from the program-owned DAG,
// never from a model response or a task input's self-declared trust marker.
func numericClaimAssessments(task *models.Task, rubric models.ClaimRubric, shared map[string]interface{}) map[string]claimNumericAssessment {
	result := map[string]claimNumericAssessment{}
	for _, claim := range rubric.Claims {
		for _, criterion := range claim.Criteria {
			// A missing metric must not turn a quantitative claim into a model-only
			// verdict. Missing frozen fields remain explicitly unverifiable.
			numericClaim := claim.ClaimType == "quantitative" || claim.ClaimType == "efficiency"
			if !numericClaim && criterion.NumericContract == nil && criterion.ExpectedValue == nil && criterion.MetricName == "" {
				continue
			}
			observations, err := extractClaimObservations(task, rubric, criterion, shared)
			assessment := claimNumericAssessment{observations: observations}
			if err != nil {
				assessment.verdict = claimverify.Unverifiable(err.Error())
			} else {
				assessment.verdict = claimverify.Verify(criterion, observations)
			}
			// An unavailable observation must not erase the frozen target shown
			// for review; expected values are never inferred from model advice.
			assessment.verdict.ExpectedValue, assessment.verdict.Unit = criterion.ExpectedValue, criterion.Unit
			if contract := criterion.NumericContract; contract != nil {
				assessment.verdict.Operator, assessment.verdict.ToleranceMode = contract.Operator, contract.ToleranceMode
				assessment.verdict.AbsoluteTolerance, assessment.verdict.RelativeTolerance = contract.AbsoluteTolerance, contract.RelativeTolerance
				assessment.verdict.Inclusive, assessment.verdict.Split, assessment.verdict.ProtocolSHA256 = contract.Inclusive, contract.Split, contract.ProtocolSHA256
			}
			result[criterion.ID] = assessment
		}
	}
	return result
}

func extractClaimObservations(task *models.Task, rubric models.ClaimRubric, criterion models.ClaimCriterion, shared map[string]interface{}) ([]models.MetricObservation, error) {
	if err := claimverify.ValidateContract(criterion); err != nil {
		return nil, err
	}
	c := criterion.NumericContract
	plan, _ := shared["plan_graph"].(*models.PlanGraph)
	if plan == nil || plan.ID == "" || plan.CurrentRevision <= 0 {
		return nil, fmt.Errorf("program-owned execution provenance is unavailable")
	}
	if owner := metadataString(task, "tenant_id"); owner != "" && owner != plan.OwnerID {
		return nil, fmt.Errorf("claim evidence owner does not match execution provenance")
	}
	if id := metadataString(task, "plan_id"); id != "" && id != plan.ID {
		return nil, fmt.Errorf("claim evidence plan does not match execution provenance")
	}
	nodes := map[string]*models.TaskNode{}
	for _, n := range plan.Nodes {
		if n != nil {
			nodes[n.ID] = n
		}
	}
	ancestors := map[string]bool{}
	var visit func(string)
	visit = func(id string) {
		if ancestors[id] {
			return
		}
		ancestors[id] = true
		if n := nodes[id]; n != nil {
			for _, dep := range n.Dependencies {
				visit(dep)
			}
		}
	}
	dependencies := task.Dependencies
	if current := nodes[task.ID]; current != nil {
		dependencies = current.Dependencies
	}
	for _, dep := range dependencies {
		visit(dep)
	}
	run := nodes[c.SourceTaskID]
	executionID := ""
	if run != nil {
		executionID = run.ExecutionID
		if stamped, ok := run.Metadata["completed_execution_id"].(string); ok {
			if claimStampInt(run.Metadata["completed_execution_epoch"]) != run.ExecutionEpoch || claimStampInt(run.Metadata["completed_revision"]) != plan.CurrentRevision {
				return nil, fmt.Errorf("completed execution stamp belongs to a different epoch or revision")
			}
			executionID = stamped
		}
	}
	if run == nil || !ancestors[run.ID] || run.Status != models.StatusCompleted || run.Revision != plan.CurrentRevision || executionID == "" || run.StartedAt == nil || run.FinishedAt == nil || run.FinishedAt.Before(*run.StartedAt) {
		return nil, fmt.Errorf("numeric source is not a completed upstream execution in the current revision")
	}
	switch run.Type {
	case "paper_code_execute", "fix_and_rerun", "benchmark_execute", "autoresearch_run", "experiment_run":
	default:
		return nil, fmt.Errorf("numeric source is not an execution node")
	}
	artifact := func(key string) (models.Artifact, string, error) {
		a, ok := plan.Artifacts[key]
		producer := nodes[a.ProducerTaskID]
		if !ok || a.Key != key || a.Revision != plan.CurrentRevision || !ancestors[a.ProducerTaskID] || producer == nil || producer.Status != models.StatusCompleted || producer.Revision != plan.CurrentRevision {
			return a, "", fmt.Errorf("artifact %s lacks current upstream producer provenance", key)
		}
		raw := a.EffectiveValue()
		if raw == "" {
			return a, "", fmt.Errorf("artifact %s has no original content available for verification", key)
		}
		return a, raw, nil
	}
	frozen, frozenRaw, err := artifact("claim_rubric")
	if err != nil {
		return nil, err
	}
	var saved models.ClaimRubric
	if json.Unmarshal([]byte(frozenRaw), &saved) != nil || saved.SHA256 != rubric.SHA256 || claimRubricSHA256(saved) != rubric.SHA256 || nodes[frozen.ProducerTaskID].Type != "claim_rubric_extract" || frozen.CreatedAt.IsZero() || !frozen.CreatedAt.Before(*run.StartedAt) {
		return nil, fmt.Errorf("numeric rubric was not frozen before the measured execution")
	}
	metricArtifact, raw, err := artifact(c.MetricArtifact)
	if err != nil {
		return nil, err
	}
	if metricArtifact.ProducerTaskID != run.ID {
		return nil, fmt.Errorf("metric artifact does not belong to the frozen execution node")
	}
	if metricArtifact.CreatedAt.IsZero() || metricArtifact.CreatedAt.Before(*run.StartedAt) || metricArtifact.CreatedAt.After(*run.FinishedAt) {
		return nil, fmt.Errorf("metric artifact does not belong to the completed execution time window")
	}
	if input := extractTaskInputLike(task, c.MetricArtifact); strings.TrimSpace(input) == "" || strings.TrimSpace(input) != strings.TrimSpace(raw) {
		return nil, fmt.Errorf("metric input does not match its original execution artifact")
	}
	if run.Code == "" || !claimverify.SameHash(claimTextHash(run.Code), c.CodeSHA256) {
		return nil, fmt.Errorf("executed code is unavailable or does not match the frozen code hash")
	}
	sources := map[string]string{c.MetricArtifact: claimTextHash(raw), "claim_rubric": claimTextHash(frozenRaw)}
	for key, expected := range map[string]string{c.DatasetArtifact: c.DatasetSHA256, c.ProtocolArtifact: c.ProtocolSHA256, c.EnvironmentArtifact: c.EnvironmentSHA256} {
		source, body, sourceErr := artifact(key)
		if sourceErr != nil {
			return nil, sourceErr
		}
		if source.CreatedAt.IsZero() || source.CreatedAt.After(*run.StartedAt) {
			return nil, fmt.Errorf("original %s was not fixed before the execution", key)
		}
		hash := claimTextHash(body)
		if !claimverify.SameHash(hash, expected) {
			return nil, fmt.Errorf("original %s hash does not match the frozen protocol", key)
		}
		sources[key] = hash
	}
	var output models.MetricExecutionEvidence
	if len(raw) > 8*1024*1024 || json.Unmarshal([]byte(raw), &output) != nil || output.Version != claimverify.ExecutionVersion || output.ExecutionID != "" && output.ExecutionID != executionID {
		return nil, fmt.Errorf("metric output is not a complete structured execution record")
	}
	output.ExecutionID = executionID
	if len(output.Runs) == 0 || len(output.Runs) > 100 {
		return nil, fmt.Errorf("execution record has no bounded seed observations")
	}
	var predictions map[int][]benchmarkPredictionRecord
	if c.ExtractionMethod == "benchmark_predictions" {
		predArtifact, predictionRaw, predErr := artifact(c.PredictionsArtifact)
		if predErr != nil {
			return nil, predErr
		}
		if predArtifact.ProducerTaskID != run.ID {
			return nil, fmt.Errorf("predictions do not belong to the measured execution")
		}
		if predArtifact.CreatedAt.IsZero() || predArtifact.CreatedAt.Before(*run.StartedAt) || predArtifact.CreatedAt.After(*run.FinishedAt) {
			return nil, fmt.Errorf("predictions do not belong to the completed execution time window")
		}
		predictions, predErr = parseClaimPredictions(predictionRaw, plan.Artifacts[c.DatasetArtifact].EffectiveValue())
		if predErr != nil {
			return nil, predErr
		}
		sources[c.PredictionsArtifact] = claimTextHash(predictionRaw)
	}
	observations := make([]models.MetricObservation, 0, len(output.Runs))
	for _, sample := range output.Runs {
		value, present := sample.Metrics[criterion.MetricName]
		method := "deterministic_json"
		if c.ExtractionMethod == "benchmark_predictions" {
			if sample.Seed == nil {
				return nil, fmt.Errorf("prediction sample seed is missing")
			}
			rows := predictions[*sample.Seed]
			if len(rows) == 0 {
				return nil, fmt.Errorf("raw predictions are missing for a frozen seed")
			}
			method = "backend_recomputed"
			var metricErr error
			value, metricErr = recomputeClaimMetric(c.PredictionTaskType, criterion.MetricName, rows)
			if metricErr != nil {
				return nil, metricErr
			}
			present = true
			// Recomputed Benchmark metrics are expressed in their native scale.
			if c.PredictionTaskType == "classification" {
				sample.Unit = "fraction"
			}
		}
		if !present || !claimverify.Finite(value) {
			return nil, fmt.Errorf("execution metric is missing or non-finite")
		}
		observations = append(observations, models.MetricObservation{
			Version: claimverify.ObservationVersion, CriterionID: criterion.ID, MetricName: criterion.MetricName, Value: value, Unit: sample.Unit,
			DatasetSHA256: output.DatasetSHA256, Split: output.Split, ProtocolSHA256: output.ProtocolSHA256, Seed: sample.Seed, ExecutionID: output.ExecutionID,
			CodeSHA256: output.CodeSHA256, EnvironmentRef: output.EnvironmentRef, EnvironmentSHA256: output.EnvironmentSHA256, SourceArtifacts: sources, ExtractionMethod: method,
		})
	}
	return observations, nil
}

func parseClaimPredictions(raw, datasetRaw string) (map[int][]benchmarkPredictionRecord, error) {
	if len(raw) > 8*1024*1024 || len(datasetRaw) > 8*1024*1024 {
		return nil, fmt.Errorf("prediction artifact exceeds verification resource limit")
	}
	targets := map[int]json.RawMessage{}
	datasetScanner := bufio.NewScanner(strings.NewReader(datasetRaw))
	datasetScanner.Buffer(make([]byte, 4096), 1024*1024)
	for datasetScanner.Scan() {
		if strings.TrimSpace(datasetScanner.Text()) == "" {
			continue
		}
		var row struct {
			Index  *int            `json:"index"`
			Target json.RawMessage `json:"target"`
		}
		if json.Unmarshal(datasetScanner.Bytes(), &row) != nil || row.Index == nil || *row.Index < 0 || len(row.Target) == 0 || string(row.Target) == "null" {
			return nil, fmt.Errorf("frozen dataset row lacks an index and target")
		}
		if _, duplicate := targets[*row.Index]; duplicate {
			return nil, fmt.Errorf("frozen dataset contains duplicate sample indexes")
		}
		targets[*row.Index] = row.Target
	}
	if err := datasetScanner.Err(); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("frozen dataset targets are unavailable for independent recomputation")
	}
	result := map[int][]benchmarkPredictionRecord{}
	seen := map[int]map[int]bool{}
	scanner := bufio.NewScanner(strings.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var row struct {
			Seed       *int            `json:"seed"`
			Index      *int            `json:"index"`
			Prediction json.RawMessage `json:"prediction"`
			Target     json.RawMessage `json:"target"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.Seed == nil || row.Index == nil || len(row.Prediction) == 0 || string(row.Prediction) == "null" {
			return nil, fmt.Errorf("prediction row lacks seed, index or prediction")
		}
		target, known := targets[*row.Index]
		if !known {
			return nil, fmt.Errorf("prediction index is outside the frozen dataset")
		}
		if seen[*row.Seed] == nil {
			seen[*row.Seed] = map[int]bool{}
		}
		if seen[*row.Seed][*row.Index] {
			return nil, fmt.Errorf("duplicate prediction sample for a seed")
		}
		seen[*row.Seed][*row.Index] = true
		var record benchmarkPredictionRecord
		if json.Unmarshal(row.Prediction, &record.Prediction) != nil || json.Unmarshal(target, &record.Target) != nil {
			return nil, fmt.Errorf("prediction row cannot be decoded")
		}
		if len(row.Target) > 0 {
			var supplied any
			if json.Unmarshal(row.Target, &supplied) != nil || fmt.Sprint(supplied) != fmt.Sprint(record.Target) {
				return nil, fmt.Errorf("prediction-reported target differs from the frozen dataset")
			}
		}
		result[*row.Seed] = append(result[*row.Seed], record)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("prediction artifact is empty")
	}
	for _, rows := range result {
		if len(rows) != len(targets) {
			return nil, fmt.Errorf("predictions do not cover the entire frozen dataset split")
		}
	}
	return result, nil
}

// Reuse the same calculators as the production Benchmark harness, rather than
// allowing a model-reported accuracy/F1 to become a verified observation.
func recomputeClaimMetric(taskType, name string, rows []benchmarkPredictionRecord) (float64, error) {
	if taskType == "classification" {
		for _, row := range rows {
			switch row.Prediction.(type) {
			case string, bool, float64:
			default:
				return 0, fmt.Errorf("classification prediction is not a scalar label")
			}
			switch row.Target.(type) {
			case string, bool, float64:
			default:
				return 0, fmt.Errorf("classification target is not a scalar label")
			}
		}
		accuracy, macroF1 := recomputeClassificationMetrics(rows)
		switch name {
		case "accuracy":
			return accuracy, nil
		case "macro_f1":
			return macroF1, nil
		}
	}
	if taskType == "regression" {
		mse, mae, err := recomputeRegressionMetrics(rows)
		if err != nil || math.IsInf(mse, 0) || math.IsInf(mae, 0) || math.IsNaN(mse) || math.IsNaN(mae) {
			return 0, fmt.Errorf("regression predictions are invalid or non-finite")
		}
		switch name {
		case "mse":
			return mse, nil
		case "mae":
			return mae, nil
		}
	}
	return 0, fmt.Errorf("metric is not supported by the existing Benchmark recomputer")
}

func claimTextHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func claimStampInt(value any) int {
	switch n := value.(type) {
	case int:
		return n
	case float64:
		if n == math.Trunc(n) {
			return int(n)
		}
	case json.Number:
		parsed, err := n.Int64()
		if err == nil {
			return int(parsed)
		}
	}
	return -1
}
