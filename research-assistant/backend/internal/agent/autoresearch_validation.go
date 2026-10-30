package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"time"

	"research-assistant-backend/internal/models"
)

func (a *ResearchCodingAgent) executeAutoResearchValidation(ctx context.Context, task *models.Task) error {
	if a == nil || a.Sandbox == nil {
		return failResearchCodingTask(task, fmt.Errorf("AutoResearch sandbox is not configured"))
	}
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	runtimeSession, err := benchmarkRuntimeSession(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, _, specHash, err := autoResearchSpecFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := validateFrozenAutoResearchSpec(spec); err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := validateAutoResearchEvaluatorScope(workspacePath, spec.EvalCommand, autoResearchPathSet(spec.EditableFiles)); err != nil {
		return failResearchCodingTask(task, err)
	}
	if len(spec.HoldoutCommand) > 0 {
		if err := validateAutoResearchEvaluatorScope(workspacePath, spec.HoldoutCommand, autoResearchPathSet(spec.EditableFiles)); err != nil {
			return failResearchCodingTask(task, fmt.Errorf("holdout_command: %w", err))
		}
		if err := validateAutoResearchCommandProtectedScope(workspacePath, spec.HoldoutCommand, autoResearchPathSet(spec.ProtectedFiles)); err != nil {
			return failResearchCodingTask(task, err)
		}
	}
	ledger, _, ledgerHash, err := autoResearchLedgerFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if ledger.SpecSHA256 != specHash {
		return failResearchCodingTask(task, fmt.Errorf("AutoResearch ledger does not match the frozen spec"))
	}
	if err := validateAutoResearchLedgerAgainstSpec(ledger, spec); err != nil {
		return failResearchCodingTask(task, err)
	}
	best, err := autoResearchBestCandidateFromTask(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := validateAutoResearchBestCandidate(best, ledger, specHash); err != nil {
		return failResearchCodingTask(task, err)
	}

	validationStartedAt := time.Now().UTC()
	requestedRuns := effectiveAutoResearchValidationRuns(spec)
	validationMode := "search_evaluator_replay"
	validationCommand := spec.EvalCommand
	expectedScore := ledger.BestScore
	var holdoutBaseline *float64
	if len(spec.HoldoutCommand) > 0 {
		validationMode = "hidden_holdout"
		validationCommand = spec.HoldoutCommand
		if ledger.HoldoutBaseline == nil || ledger.HoldoutResult == nil {
			return failResearchCodingTask(task, fmt.Errorf("AutoResearch ledger is missing the frozen holdout baseline"))
		}
		baseline := *ledger.HoldoutBaseline
		holdoutBaseline = &baseline
		holdoutMinDelta := effectiveAutoResearchHoldoutMinDelta(spec)
		if spec.Direction == "minimize" {
			expectedScore = baseline - holdoutMinDelta
		} else {
			expectedScore = baseline + holdoutMinDelta
		}
	}
	protectedErr := verifyAutoResearchSpecFiles(workspacePath, spec)
	immutableErr := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace)
	candidateHashes, candidateErr := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
	protectedIntact := protectedErr == nil
	workspaceIntact := immutableErr == nil
	candidateIntact := candidateErr == nil && equalAutoResearchHashes(candidateHashes, ledger.BestCandidateFiles)
	runs := make([]models.ResearchValidationRun, 0, requestedRuns)
	observedScores := make([]float64, 0, requestedRuns)
	legacyEvaluation := autoResearchEvaluation{Guards: []models.ResearchCommandResult{}}
	legacyEvaluationSet := false
	passedRuns := 0
	var validationErr error
	if !protectedIntact {
		validationErr = protectedErr
	} else if !workspaceIntact {
		validationErr = immutableErr
	} else if !candidateIntact {
		validationErr = fmt.Errorf("best candidate files do not match the trial ledger")
	} else {
		protectedSnapshots, snapshotErr := snapshotAutoResearchFiles(workspacePath, spec.ProtectedFiles, autoResearchMaxProtectedBytes, true)
		if snapshotErr != nil {
			return failResearchCodingTask(task, snapshotErr)
		}
		candidateSnapshots, snapshotErr := snapshotAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
		if snapshotErr != nil {
			return failResearchCodingTask(task, snapshotErr)
		}
		immutableSnapshots, snapshotErr := snapshotAutoResearchImmutableFiles(workspacePath, spec.EditableFiles)
		if snapshotErr != nil {
			return failResearchCodingTask(task, snapshotErr)
		}
		tolerance := math.Max(1e-9, math.Abs(ledger.BestScore)*1e-6)
		for runNumber := 1; runNumber <= requestedRuns; runNumber++ {
			if ctx.Err() != nil {
				validationErr = ctx.Err()
				break
			}
			run := models.ResearchValidationRun{Number: runNumber, Status: "running", StartedAt: time.Now().UTC()}
			if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, candidateSnapshots); restoreErr != nil {
				run.Status = "failed"
				run.Error = "restore clean validation snapshot: " + restoreErr.Error()
				run.FinishedAt = time.Now().UTC()
				runs = append(runs, run)
				validationErr = fmt.Errorf("validation run %d: %s", run.Number, run.Error)
				break
			}
			evaluation, evalErr := a.evaluateAutoResearchWithCommand(ctx, runtimeSession, workspacePath, spec, validationCommand)
			run.GuardResults = evaluation.Guards
			run.EvalResult = evaluation.Eval
			if !legacyEvaluationSet {
				legacyEvaluation = evaluation
				legacyEvaluationSet = true
			}

			runProtectedErr := verifyAutoResearchSpecFiles(workspacePath, spec)
			runImmutableErr := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace)
			candidateHashes, candidateErr = hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
			runProtectedIntact := runProtectedErr == nil
			runWorkspaceIntact := runImmutableErr == nil
			runCandidateIntact := candidateErr == nil && equalAutoResearchHashes(candidateHashes, ledger.BestCandidateFiles)
			protectedIntact = protectedIntact && runProtectedIntact
			workspaceIntact = workspaceIntact && runWorkspaceIntact
			candidateIntact = candidateIntact && runCandidateIntact
			integrityFailure := !runProtectedIntact || !runWorkspaceIntact || !runCandidateIntact
			if integrityFailure {
				if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, candidateSnapshots); restoreErr != nil {
					evalErr = fmt.Errorf("validation changed frozen files and restore failed: %w", restoreErr)
				} else if evalErr == nil {
					switch {
					case !runProtectedIntact:
						evalErr = runProtectedErr
					case !runWorkspaceIntact:
						evalErr = runImmutableErr
					default:
						evalErr = fmt.Errorf("validation changed best candidate files")
					}
				}
			}

			if evalErr == nil {
				observed := evaluation.Metric
				run.ObservedScore = &observed
				observedScores = append(observedScores, observed)
				if holdoutBaseline != nil {
					delta := autoResearchDelta(observed, *holdoutBaseline, spec.Direction)
					run.DeltaFromBaseline = &delta
					run.ScoreMatches = autoResearchImproved(observed, *holdoutBaseline, spec.Direction, effectiveAutoResearchHoldoutMinDelta(spec))
				} else {
					run.ScoreMatches = math.Abs(observed-ledger.BestScore) <= tolerance
				}
				if run.ScoreMatches {
					run.Status = "validated"
					passedRuns++
				} else {
					run.Status = "failed"
					if holdoutBaseline != nil {
						run.Error = fmt.Sprintf("hidden holdout %s=%.8g did not improve baseline %.8g by required %.8g", spec.MetricKey, observed, *holdoutBaseline, effectiveAutoResearchHoldoutMinDelta(spec))
					} else {
						run.Error = fmt.Sprintf("observed %s=%.8g differs from search score %.8g", spec.MetricKey, observed, ledger.BestScore)
					}
				}
			} else {
				run.Status = "failed"
				run.Error = evalErr.Error()
			}
			run.FinishedAt = time.Now().UTC()
			runs = append(runs, run)
			if run.Status == "failed" && validationErr == nil {
				validationErr = fmt.Errorf("validation run %d: %s", run.Number, run.Error)
			}
			if integrityFailure || ctx.Err() != nil {
				break
			}
		}
	}
	meanScore, stddev, minScore, maxScore := autoResearchScoreStats(observedScores)
	completedRuns := len(runs)
	failedRuns := completedRuns - passedRuns
	unfinishedRuns := requestedRuns - completedRuns
	failureRate := float64(failedRuns+unfinishedRuns) / float64(requestedRuns)
	scoreMatches := passedRuns == requestedRuns
	status := "validated"
	summary := fmt.Sprintf("%d search-evaluator replay run(s) reproduced %s=%.8g (stddev %.8g); no hidden holdout configured", requestedRuns, spec.MetricKey, meanScore, stddev)
	if holdoutBaseline != nil {
		summary = fmt.Sprintf("%d hidden holdout run(s) accepted %s baseline %.8g -> mean %.8g (stddev %.8g)", requestedRuns, spec.MetricKey, *holdoutBaseline, meanScore, stddev)
	} else if requestedRuns == 1 {
		summary = fmt.Sprintf("search-evaluator replay reproduced %s=%.8g; no hidden holdout configured", spec.MetricKey, meanScore)
	}
	if !protectedIntact || !workspaceIntact || !candidateIntact || !scoreMatches {
		status = "failed"
		summary = fmt.Sprintf("validation failed: passed=%d/%d failed=%d unfinished=%d protected_intact=%t workspace_intact=%t candidate_intact=%t", passedRuns, requestedRuns, failedRuns, unfinishedRuns, protectedIntact, workspaceIntact, candidateIntact)
		if validationErr != nil {
			summary += "; evaluator: " + validationErr.Error()
		}
	}
	validatedAt := time.Now().UTC()
	resourceUsage := summarizeAutoResearchValidationUsage(runs, validationStartedAt, validatedAt)
	report := models.ResearchValidationReport{
		Version: models.AutoResearchValidationVersion, Status: status, ValidationMode: validationMode, SpecSHA256: specHash,
		LedgerSHA256: ledgerHash, ExpectedScore: expectedScore, SearchBestScore: ledger.BestScore, HoldoutBaseline: holdoutBaseline, ObservedScore: meanScore,
		ObservedScores: observedScores, MeanScore: meanScore, StdDev: stddev, MinScore: minScore, MaxScore: maxScore,
		MetricKey: spec.MetricKey, ScoreMatches: scoreMatches, ProtectedIntact: protectedIntact,
		RequestedRuns: requestedRuns, CompletedRuns: completedRuns, PassedRuns: passedRuns, FailedRuns: failedRuns,
		UnfinishedRuns: unfinishedRuns, FailureRate: failureRate, WorkspaceIntact: workspaceIntact,
		CandidateIntact: candidateIntact, GuardResults: legacyEvaluation.Guards, EvalResult: legacyEvaluation.Eval,
		Runs: runs, ResourceUsage: resourceUsage, ValidatedAt: validatedAt, Summary: summary,
	}
	reportJSON, _ := json.Marshal(report)
	metricsJSON, _ := json.Marshal(map[string]any{
		"metric_key": spec.MetricKey, "score": meanScore, "expected_score": expectedScore,
		"validation_mode": validationMode, "search_best_score": ledger.BestScore, "holdout_baseline_score": holdoutBaseline,
		"stddev": stddev, "observed_scores": observedScores, "requested_runs": requestedRuns,
		"min_score": minScore, "max_score": maxScore, "completed_runs": completedRuns,
		"passed_runs": passedRuns, "failure_rate": failureRate, "status": status,
		"spec_sha256": specHash, "ledger_sha256": ledgerHash,
	})
	task.Result = string(reportJSON)
	task.StructuredData = string(reportJSON)
	setResearchCodingArtifacts(task, map[string]string{
		"research_validation_report": string(reportJSON),
		"research_best_metrics":      string(metricsJSON),
	})
	if status != "validated" {
		if !protectedIntact || !workspaceIntact || !candidateIntact || unfinishedRuns > 0 {
			return failResearchCodingTask(task, fmt.Errorf("%s", summary))
		}
		task.Status = models.StatusCompleted
		logToContext(ctx, "[%s] AutoResearch candidate rejected by %s: %s", a.Name, validationMode, summary)
		return nil
	}
	task.Status = models.StatusCompleted
	logToContext(ctx, "[%s] %s", a.Name, summary)
	return nil
}

func effectiveAutoResearchValidationRuns(spec models.ResearchSpec) int {
	if spec.ValidationRuns <= 0 {
		return autoResearchDefaultValidationRuns
	}
	return spec.ValidationRuns
}

func effectiveAutoResearchHoldoutMinDelta(spec models.ResearchSpec) float64 {
	if spec.HoldoutMinDelta != nil {
		return *spec.HoldoutMinDelta
	}
	return spec.MinDelta
}

func autoResearchScoreStats(scores []float64) (mean, stddev, minimum, maximum float64) {
	if len(scores) == 0 {
		return 0, 0, 0, 0
	}
	minimum = scores[0]
	maximum = scores[0]
	for _, score := range scores {
		mean += score
		if score < minimum {
			minimum = score
		}
		if score > maximum {
			maximum = score
		}
	}
	mean /= float64(len(scores))
	if minimum == maximum {
		return mean, 0, minimum, maximum
	}
	for _, score := range scores {
		delta := score - mean
		stddev += delta * delta
	}
	stddev = math.Sqrt(stddev / float64(len(scores)))
	return mean, stddev, minimum, maximum
}

func validAutoResearchSearchAggregation(aggregation string) bool {
	switch aggregation {
	case "mean", "median", "worst":
		return true
	default:
		return false
	}
}

func autoResearchTargetReached(target *float64, score float64, direction string) bool {
	if target == nil || math.IsNaN(score) || math.IsInf(score, 0) {
		return false
	}
	const epsilon = 1e-12
	if direction == "minimize" {
		return score <= *target+epsilon
	}
	return direction == "maximize" && score >= *target-epsilon
}

func equalOptionalAutoResearchScores(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return math.Float64bits(*left) == math.Float64bits(*right)
}

func aggregateAutoResearchScores(scores []float64, aggregation, direction string) (float64, error) {
	if len(scores) == 0 {
		return 0, fmt.Errorf("cannot aggregate empty AutoResearch score samples")
	}
	for _, score := range scores {
		if math.IsNaN(score) || math.IsInf(score, 0) {
			return 0, fmt.Errorf("cannot aggregate non-finite AutoResearch score sample")
		}
	}
	switch aggregation {
	case "mean":
		mean, _, _, _ := autoResearchScoreStats(scores)
		return mean, nil
	case "median":
		ordered := append([]float64(nil), scores...)
		sort.Float64s(ordered)
		middle := len(ordered) / 2
		if len(ordered)%2 == 1 {
			return ordered[middle], nil
		}
		return (ordered[middle-1] + ordered[middle]) / 2, nil
	case "worst":
		_, _, minimum, maximum := autoResearchScoreStats(scores)
		if direction == "maximize" {
			return minimum, nil
		}
		if direction == "minimize" {
			return maximum, nil
		}
		return 0, fmt.Errorf("invalid AutoResearch metric direction %q", direction)
	default:
		return 0, fmt.Errorf("invalid AutoResearch search aggregation %q", aggregation)
	}
}

func summarizeAutoResearchValidationUsage(runs []models.ResearchValidationRun, startedAt, finishedAt time.Time) models.ResearchResourceUsage {
	usage := models.ResearchResourceUsage{}
	for _, run := range runs {
		for _, command := range run.GuardResults {
			addAutoResearchCommandUsage(&usage, command, true)
		}
		addAutoResearchCommandUsage(&usage, run.EvalResult, false)
	}
	usage.WallDurationMS = autoResearchElapsedMS(startedAt, finishedAt)
	return usage
}
