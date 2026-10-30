package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"research-assistant-backend/internal/agent/conversation"
	"strings"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
)

func autoResearchSpecFromTask(task *models.Task) (models.ResearchSpec, []byte, string, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "research_spec"))
	if raw == "" {
		return models.ResearchSpec{}, nil, "", fmt.Errorf("research_spec input is required")
	}
	var spec models.ResearchSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return spec, nil, "", fmt.Errorf("decode frozen research_spec: %w", err)
	}
	if spec.Version != models.AutoResearchSpecVersion || len(spec.FrozenProtected) == 0 {
		return spec, nil, "", fmt.Errorf("research_spec is not frozen %s data", models.AutoResearchSpecVersion)
	}
	return spec, []byte(raw), autoResearchSHA([]byte(raw)), nil
}

func validateFrozenAutoResearchSpec(spec models.ResearchSpec) error {
	if spec.Version != models.AutoResearchSpecVersion || strings.TrimSpace(spec.Name) == "" || strings.TrimSpace(spec.Objective) == "" {
		return fmt.Errorf("frozen AutoResearch spec identity is invalid")
	}
	if spec.Direction != "maximize" && spec.Direction != "minimize" {
		return fmt.Errorf("frozen AutoResearch direction is invalid")
	}
	if !autoResearchMetricKeyRE.MatchString(spec.MetricKey) || spec.MinDelta < 0 || math.IsNaN(spec.MinDelta) || math.IsInf(spec.MinDelta, 0) {
		return fmt.Errorf("frozen AutoResearch metric contract is invalid")
	}
	if spec.TargetScore != nil && (math.IsNaN(*spec.TargetScore) || math.IsInf(*spec.TargetScore, 0)) {
		return fmt.Errorf("frozen AutoResearch target score is invalid")
	}
	if spec.SearchRuns < 1 || spec.SearchRuns > autoResearchMaxSearchRuns || !validAutoResearchSearchAggregation(spec.SearchAggregation) {
		return fmt.Errorf("frozen AutoResearch search measurement contract is invalid")
	}
	if spec.MaxTrials < 1 || spec.MaxTrials > autoResearchMaxTrials || spec.MaxWallSeconds < 1 || spec.MaxWallSeconds > autoResearchMaxWallSeconds {
		return fmt.Errorf("frozen AutoResearch budget exceeds runtime limits")
	}
	if spec.ValidationRuns < 0 || spec.ValidationRuns > autoResearchMaxValidationRuns {
		return fmt.Errorf("frozen AutoResearch validation run count exceeds runtime limits")
	}
	if len(spec.EditableFiles) == 0 || len(spec.EditableFiles) > autoResearchMaxEditableFiles || len(spec.ProtectedFiles) == 0 || len(spec.ProtectedFiles) > autoResearchMaxProtectedFiles {
		return fmt.Errorf("frozen AutoResearch file scope is invalid")
	}
	editable := make(map[string]struct{}, len(spec.EditableFiles))
	for _, path := range spec.EditableFiles {
		if path == "" {
			return fmt.Errorf("frozen AutoResearch editable path is empty")
		}
		editable[path] = struct{}{}
	}
	if len(editable) != len(spec.EditableFiles) {
		return fmt.Errorf("frozen AutoResearch editable paths contain duplicates")
	}
	if len(spec.FrozenProtected) != len(spec.ProtectedFiles) {
		return fmt.Errorf("frozen AutoResearch protected hash set is incomplete")
	}
	if len(spec.FrozenWorkspace) != sha256.Size*2 {
		return fmt.Errorf("frozen AutoResearch workspace fingerprint is invalid")
	}
	if _, err := hex.DecodeString(spec.FrozenWorkspace); err != nil {
		return fmt.Errorf("frozen AutoResearch workspace fingerprint is invalid")
	}
	protected := make(map[string]struct{}, len(spec.ProtectedFiles))
	for _, path := range spec.ProtectedFiles {
		if path == "" {
			return fmt.Errorf("frozen AutoResearch protected path is empty")
		}
		if _, overlap := editable[path]; overlap {
			return fmt.Errorf("frozen AutoResearch editable and protected paths overlap")
		}
		protected[path] = struct{}{}
	}
	if len(protected) != len(spec.ProtectedFiles) {
		return fmt.Errorf("frozen AutoResearch protected paths contain duplicates")
	}
	hashedPaths := make(map[string]struct{}, len(spec.FrozenProtected))
	for _, file := range spec.FrozenProtected {
		if _, ok := protected[file.Path]; !ok || len(file.SHA256) != sha256.Size*2 {
			return fmt.Errorf("frozen AutoResearch protected hash is invalid")
		}
		if _, duplicate := hashedPaths[file.Path]; duplicate {
			return fmt.Errorf("frozen AutoResearch protected hashes contain duplicates")
		}
		hashedPaths[file.Path] = struct{}{}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return fmt.Errorf("frozen AutoResearch protected hash is invalid")
		}
	}
	if err := validateAutoResearchCommand(spec.EvalCommand); err != nil {
		return fmt.Errorf("frozen AutoResearch eval command is invalid: %w", err)
	}
	if len(spec.HoldoutCommand) > 0 {
		if err := validateAutoResearchCommand(spec.HoldoutCommand); err != nil {
			return fmt.Errorf("frozen AutoResearch holdout command is invalid: %w", err)
		}
		if equalAutoResearchCommands(spec.HoldoutCommand, spec.EvalCommand) {
			return fmt.Errorf("frozen AutoResearch holdout command must differ from the eval command")
		}
		if spec.HoldoutMinDelta != nil && (*spec.HoldoutMinDelta < 0 || math.IsNaN(*spec.HoldoutMinDelta) || math.IsInf(*spec.HoldoutMinDelta, 0)) {
			return fmt.Errorf("frozen AutoResearch holdout minimum delta is invalid")
		}
	} else if spec.HoldoutMinDelta != nil {
		return fmt.Errorf("frozen AutoResearch holdout minimum delta has no holdout command")
	}
	if len(spec.GuardCommands) > 6 {
		return fmt.Errorf("frozen AutoResearch guard command count is invalid")
	}
	for _, command := range spec.GuardCommands {
		if err := validateAutoResearchCommand(command); err != nil {
			return fmt.Errorf("frozen AutoResearch guard command is invalid: %w", err)
		}
	}
	if len(spec.Dependencies) > 32 {
		return fmt.Errorf("frozen AutoResearch dependency count is invalid")
	}
	for _, dependency := range spec.Dependencies {
		if !autoResearchDependencyRE.MatchString(dependency) {
			return fmt.Errorf("frozen AutoResearch dependency is invalid")
		}
	}
	return nil
}

func autoResearchLedgerFromTask(task *models.Task) (models.ResearchTrialLedger, []byte, string, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "research_trial_ledger"))
	if raw == "" {
		return models.ResearchTrialLedger{}, nil, "", fmt.Errorf("research_trial_ledger input is required")
	}
	var ledger models.ResearchTrialLedger
	if err := json.Unmarshal([]byte(raw), &ledger); err != nil {
		return ledger, nil, "", fmt.Errorf("decode research_trial_ledger: %w", err)
	}
	if ledger.Version != models.AutoResearchLedgerVersion || ledger.Status != "completed" {
		return ledger, nil, "", fmt.Errorf("research_trial_ledger is not a completed %s ledger", models.AutoResearchLedgerVersion)
	}
	if ledger.MaxTrials < 1 || ledger.MaxTrials > autoResearchMaxTrials || ledger.CompletedTrials < 0 || ledger.CompletedTrials > ledger.MaxTrials || ledger.AcceptedTrials < 0 || ledger.AcceptedTrials > ledger.CompletedTrials {
		return ledger, nil, "", fmt.Errorf("research_trial_ledger contains invalid trial counts")
	}
	if len(ledger.Trials) != ledger.CompletedTrials+1 || len(ledger.BestCandidateFiles) == 0 || len(ledger.ProtectedFiles) == 0 {
		return ledger, nil, "", fmt.Errorf("research_trial_ledger contains incomplete evidence")
	}
	if math.IsNaN(ledger.BaselineScore) || math.IsInf(ledger.BaselineScore, 0) || math.IsNaN(ledger.BestScore) || math.IsInf(ledger.BestScore, 0) {
		return ledger, nil, "", fmt.Errorf("research_trial_ledger contains non-finite scores")
	}
	return ledger, []byte(raw), autoResearchSHA([]byte(raw)), nil
}

func validateAutoResearchLedgerAgainstSpec(ledger models.ResearchTrialLedger, spec models.ResearchSpec) error {
	if ledger.MetricKey != spec.MetricKey || ledger.Direction != spec.Direction || ledger.MaxTrials != spec.MaxTrials ||
		ledger.SearchRuns != spec.SearchRuns || ledger.SearchAggregation != spec.SearchAggregation ||
		!equalOptionalAutoResearchScores(ledger.TargetScore, spec.TargetScore) {
		return fmt.Errorf("research_trial_ledger metric, measurement contract, direction or budget does not match the frozen spec")
	}
	if !equalAutoResearchHashes(ledger.ProtectedFiles, spec.FrozenProtected) {
		return fmt.Errorf("research_trial_ledger protected files do not match the frozen spec")
	}
	if len(spec.HoldoutCommand) > 0 {
		if ledger.HoldoutBaseline == nil || ledger.HoldoutResult == nil {
			return fmt.Errorf("research_trial_ledger is missing frozen holdout baseline evidence")
		}
		if math.IsNaN(*ledger.HoldoutBaseline) || math.IsInf(*ledger.HoldoutBaseline, 0) ||
			!equalAutoResearchCommands(ledger.HoldoutResult.Command, spec.HoldoutCommand) || ledger.HoldoutResult.ExitCode != 0 || ledger.HoldoutResult.Error != "" {
			return fmt.Errorf("research_trial_ledger holdout baseline evidence is invalid")
		}
	} else if ledger.HoldoutBaseline != nil || ledger.HoldoutResult != nil {
		return fmt.Errorf("research_trial_ledger contains holdout evidence absent from the frozen spec")
	}
	if len(ledger.Trials) == 0 {
		return fmt.Errorf("research_trial_ledger has no baseline trial")
	}
	baseline := ledger.Trials[0]
	if baseline.Number != 0 || baseline.Status != "baseline" || baseline.Decision != "keep" || baseline.Metric == nil ||
		math.Float64bits(*baseline.Metric) != math.Float64bits(ledger.BaselineScore) {
		return fmt.Errorf("research_trial_ledger baseline evidence is inconsistent")
	}
	if err := validateAutoResearchTrialMeasurement(baseline, spec); err != nil {
		return fmt.Errorf("research_trial_ledger baseline measurement is invalid: %w", err)
	}

	bestScore := ledger.BaselineScore
	accepted := 0
	targetReached := false
	for index, trial := range ledger.Trials[1:] {
		if trial.Number != index+1 {
			return fmt.Errorf("research_trial_ledger trial numbers are not sequential")
		}
		if trial.Metric != nil && (math.IsNaN(*trial.Metric) || math.IsInf(*trial.Metric, 0)) {
			return fmt.Errorf("research_trial_ledger trial %d contains a non-finite metric", trial.Number)
		}
		if trial.Metric != nil {
			if err := validateAutoResearchTrialMeasurement(trial, spec); err != nil {
				return fmt.Errorf("research_trial_ledger trial %d measurement is invalid: %w", trial.Number, err)
			}
		}
		switch trial.Status {
		case "kept":
			if trial.Decision != "keep" || trial.Metric == nil || !autoResearchImproved(*trial.Metric, bestScore, spec.Direction, spec.MinDelta) {
				return fmt.Errorf("research_trial_ledger kept trial %d violates the frozen acceptance rule", trial.Number)
			}
			bestScore = *trial.Metric
			accepted++
			if autoResearchTargetReached(spec.TargetScore, bestScore, spec.Direction) {
				if index != len(ledger.Trials[1:])-1 {
					return fmt.Errorf("research_trial_ledger continued after reaching the frozen target")
				}
				targetReached = true
			}
		case "rejected":
			if trial.Decision != "reject" {
				return fmt.Errorf("research_trial_ledger rejected trial %d has an invalid decision", trial.Number)
			}
			if trial.Metric != nil && autoResearchImproved(*trial.Metric, bestScore, spec.Direction, spec.MinDelta) {
				return fmt.Errorf("research_trial_ledger rejected trial %d should have been kept", trial.Number)
			}
		default:
			return fmt.Errorf("research_trial_ledger completed trial %d has invalid status %q", trial.Number, trial.Status)
		}
	}
	if accepted != ledger.AcceptedTrials || math.Float64bits(bestScore) != math.Float64bits(ledger.BestScore) {
		return fmt.Errorf("research_trial_ledger best score or accepted count is inconsistent")
	}
	if targetReached != (ledger.StopReason == "target_score_reached") {
		return fmt.Errorf("research_trial_ledger target stop reason is inconsistent")
	}
	if targetReached {
		last := ledger.Trials[len(ledger.Trials)-1]
		if ledger.TargetScore == nil || accepted == 0 || last.Status != "kept" || !autoResearchTargetReached(ledger.TargetScore, bestScore, ledger.Direction) {
			return fmt.Errorf("research_trial_ledger target stop evidence is inconsistent")
		}
	}
	if ledger.ResourceUsage != nil {
		expectedLedger := ledger
		expectedLedger.ResourceUsage = nil
		finalizeAutoResearchLedgerUsage(&expectedLedger)
		if expectedLedger.ResourceUsage == nil || *ledger.ResourceUsage != *expectedLedger.ResourceUsage {
			return fmt.Errorf("research_trial_ledger resource usage is inconsistent with command evidence")
		}
	}
	return nil
}

func validateAutoResearchTrialMeasurement(trial models.ResearchTrial, spec models.ResearchSpec) error {
	if trial.Metric == nil {
		return fmt.Errorf("aggregated metric is missing")
	}
	if len(trial.MetricSamples) != spec.SearchRuns || len(trial.EvalResults) != spec.SearchRuns {
		return fmt.Errorf("expected %d complete evaluator samples, got metrics=%d commands=%d", spec.SearchRuns, len(trial.MetricSamples), len(trial.EvalResults))
	}
	if trial.MetricAggregation != spec.SearchAggregation {
		return fmt.Errorf("aggregation %q does not match frozen %q", trial.MetricAggregation, spec.SearchAggregation)
	}
	for _, result := range trial.EvalResults {
		if !equalAutoResearchCommands(result.Command, spec.EvalCommand) || result.ExitCode != 0 || result.Error != "" {
			return fmt.Errorf("evaluator command evidence is incomplete")
		}
	}
	aggregated, err := aggregateAutoResearchScores(trial.MetricSamples, trial.MetricAggregation, spec.Direction)
	if err != nil {
		return err
	}
	if math.Float64bits(aggregated) != math.Float64bits(*trial.Metric) {
		return fmt.Errorf("aggregated metric does not match raw samples")
	}
	_, stddev, _, _ := autoResearchScoreStats(trial.MetricSamples)
	if math.Float64bits(stddev) != math.Float64bits(trial.MetricStdDev) {
		return fmt.Errorf("metric standard deviation does not match raw samples")
	}
	last := trial.EvalResults[len(trial.EvalResults)-1]
	if !equalAutoResearchCommands(trial.EvalResult.Command, last.Command) || trial.EvalResult.ExitCode != last.ExitCode || trial.EvalResult.Error != last.Error {
		return fmt.Errorf("legacy evaluator result does not identify the final sample")
	}
	return nil
}

func autoResearchBestCandidateFromTask(task *models.Task) (models.ResearchBestCandidate, error) {
	raw := strings.TrimSpace(extractTaskInputLike(task, "research_best_candidate"))
	if raw == "" {
		return models.ResearchBestCandidate{}, fmt.Errorf("research_best_candidate input is required")
	}
	var best models.ResearchBestCandidate
	if err := json.Unmarshal([]byte(raw), &best); err != nil {
		return best, fmt.Errorf("decode research_best_candidate: %w", err)
	}
	return best, nil
}

func validateAutoResearchBestCandidate(best models.ResearchBestCandidate, ledger models.ResearchTrialLedger, specHash string) error {
	if best.SpecSHA256 != specHash || best.MetricKey != ledger.MetricKey || best.Direction != ledger.Direction || best.AcceptedTrials != ledger.AcceptedTrials {
		return fmt.Errorf("research_best_candidate does not match the frozen spec and trial ledger")
	}
	if math.Float64bits(best.Score) != math.Float64bits(ledger.BestScore) || !equalAutoResearchHashes(best.Files, ledger.BestCandidateFiles) {
		return fmt.Errorf("research_best_candidate score or file hashes do not match the trial ledger")
	}
	return nil
}

// proposeAutoResearchCandidateWithSession keeps trial proposals in one
// conversation: the previous proposal stays an assistant message and only the
// compact ledger/feedback delta is appended.
func (a *ResearchCodingAgent) proposeAutoResearchCandidateWithSession(ctx context.Context, spec models.ResearchSpec, specJSON []byte, ledger models.ResearchTrialLedger, snapshots []autoResearchFileSnapshot, readOnlyContext, rejectedCandidateFeedback string, session *conversation.Session) (autoResearchCandidateResponse, *conversation.Session, error) {
	var response autoResearchCandidateResponse
	contextText := autoResearchSourceContext(snapshots)
	ledgerSummary := autoResearchLedgerSummaryJSON(ledger)
	if session == nil {
		session = conversation.NewSession("autoresearch-candidate", prompts.AutoResearchCandidateSystemPrompt,
			prompts.AutoResearchCandidateUserPrompt(string(specJSON), ledgerSummary, contextText, readOnlyContext, rejectedCandidateFeedback))
	} else {
		session.AddUser("Trial result update:\n" + ledgerSummary + "\nFeedback:\n" + rejectedCandidateFeedback)
	}
	message, err := session.Generate(ctx, a.ChatModel)
	if err != nil {
		return response, session, fmt.Errorf("candidate generation failed: %w", err)
	}
	if err := json.Unmarshal([]byte(cleanJSONResponse(message.Content)), &response); err != nil {
		return response, session, fmt.Errorf("decode candidate response: %w", err)
	}
	response.Status = strings.ToLower(strings.TrimSpace(response.Status))
	response.Diagnosis = strings.TrimSpace(response.Diagnosis)
	response.Hypothesis = strings.TrimSpace(response.Hypothesis)
	response.Reason = strings.TrimSpace(response.Reason)
	switch response.Status {
	case "propose":
		if response.Diagnosis == "" || response.Hypothesis == "" || len(response.Patches) == 0 || len(response.Patches) > autoResearchMaxPatchFiles {
			return response, session, fmt.Errorf("candidate proposal requires a diagnosis, hypothesis and 1-%d patches", autoResearchMaxPatchFiles)
		}
	case "stop", "unsupported":
		response.Patches = nil
	default:
		return response, session, fmt.Errorf("invalid candidate status %q", response.Status)
	}
	return response, session, nil
}

func autoResearchRejectedCandidateFeedback(proposal autoResearchCandidateResponse, failure string, evaluation autoResearchEvaluation) string {
	patches := make([]map[string]string, 0, len(proposal.Patches))
	for _, patch := range proposal.Patches {
		patches = append(patches, map[string]string{
			"path": patch.Path, "content": patch.Content, "reason": patch.Reason,
		})
	}
	payload := map[string]any{
		"failure": failure,
		"patches": patches,
	}
	if len(evaluation.Guards) > 0 {
		payload["guard_results"] = evaluation.Guards
	}
	if len(evaluation.Eval.Command) > 0 || evaluation.Eval.Error != "" || evaluation.Eval.StdoutPreview != "" || evaluation.Eval.StderrPreview != "" {
		payload["evaluator_result"] = evaluation.Eval
	}
	encoded, _ := json.Marshal(payload)
	return truncatePaperDebugText(string(encoded), autoResearchMaxReadOnlyBytes)
}

func autoResearchModelFailureFeedback(failure string) string {
	payload := map[string]any{
		"failure": failure,
		"required_response": map[string]any{
			"status":     "propose|stop|unsupported",
			"diagnosis":  "failing case, actual input and current call path",
			"hypothesis": "falsifiable hypothesis",
			"reason":     "evidence-based rationale",
			"patches": []map[string]string{{
				"path": "editable/path", "content": "complete file content", "reason": "why this file changes",
			}},
		},
		"instruction": "Return one raw JSON object without Markdown fences or comments; escape all newlines and quotes inside file content.",
	}
	encoded, _ := json.Marshal(payload)
	return truncatePaperDebugText(string(encoded), autoResearchMaxReadOnlyBytes)
}
