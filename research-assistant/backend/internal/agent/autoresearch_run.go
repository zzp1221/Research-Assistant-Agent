package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"research-assistant-backend/internal/agent/conversation"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

func (a *ResearchCodingAgent) executeAutoResearchRun(ctx context.Context, task *models.Task) error {
	if a == nil || a.Sandbox == nil {
		return failResearchCodingTask(task, fmt.Errorf("AutoResearch sandbox is not configured"))
	}
	if a.ChatModel == nil {
		return failResearchCodingTask(task, fmt.Errorf("AutoResearch candidate model is not configured"))
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
	if err := verifyAutoResearchSpecFiles(workspacePath, spec); err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace); err != nil {
		return failResearchCodingTask(task, err)
	}

	editableSnapshots, err := snapshotAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	protectedSnapshots, err := snapshotAutoResearchFiles(workspacePath, spec.ProtectedFiles, autoResearchMaxProtectedBytes, true)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	immutableSnapshots, err := snapshotAutoResearchImmutableFiles(workspacePath, spec.EditableFiles)
	if err != nil {
		return failResearchCodingTask(task, err)
	}

	startedAt := time.Now().UTC()
	ledger := models.ResearchTrialLedger{
		Version: models.AutoResearchLedgerVersion, SpecSHA256: specHash, Status: "running",
		MetricKey: spec.MetricKey, Direction: spec.Direction, TargetScore: spec.TargetScore, SearchRuns: spec.SearchRuns,
		SearchAggregation: spec.SearchAggregation, MaxTrials: spec.MaxTrials,
		ProtectedFiles: append([]models.ResearchFileHash(nil), spec.FrozenProtected...),
		Trials:         []models.ResearchTrial{}, StartedAt: startedAt,
	}
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(spec.MaxWallSeconds)*time.Second)
	defer cancel()

	baselineEditableHashes, err := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
	if err != nil {
		return failAutoResearchRun(task, &ledger, err)
	}
	baselineStart := time.Now().UTC()
	baseline, baselineErr := a.evaluateAutoResearchCandidate(runCtx, runtimeSession, workspacePath, spec)
	if baselineErr == nil {
		currentHashes, hashErr := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
		if hashErr != nil {
			baselineErr = hashErr
		} else if !equalAutoResearchHashes(currentHashes, baselineEditableHashes) {
			baselineErr = fmt.Errorf("editable files changed during frozen baseline evaluation")
		}
	}
	baselineTrial := models.ResearchTrial{
		Number: 0, Status: "baseline", Hypothesis: "frozen baseline", Decision: "keep",
		Reason: "baseline establishes the initial score", GuardResults: baseline.Guards,
		EvalResult: baseline.Eval, EvalResults: baseline.Evals,
		MetricSamples: append([]float64(nil), baseline.Metrics...), MetricStdDev: baseline.MetricStdDev,
		MetricAggregation: baseline.Aggregation, StartedAt: baselineStart, FinishedAt: time.Now().UTC(),
	}
	if baselineErr != nil {
		baselineTrial.Status = "failed"
		baselineTrial.Decision = "abort"
		baselineTrial.Reason = baselineErr.Error()
		ledger.Trials = append(ledger.Trials, baselineTrial)
		ledger.Status = "failed"
		ledger.StopReason = "baseline_failed"
		ledger.FinishedAt = time.Now().UTC()
		if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, editableSnapshots); restoreErr != nil {
			baselineErr = fmt.Errorf("%w; baseline restore failed: %v", baselineErr, restoreErr)
			baselineTrial.Reason = baselineErr.Error()
			ledger.Trials[len(ledger.Trials)-1].Reason = baselineTrial.Reason
		}
		return failAutoResearchRun(task, &ledger, baselineErr)
	}
	baselineMetric := baseline.Metric
	baselineTrial.Metric = &baselineMetric
	ledger.Trials = append(ledger.Trials, baselineTrial)
	ledger.BaselineScore = baselineMetric
	ledger.BestScore = baselineMetric
	if len(spec.HoldoutCommand) > 0 {
		holdoutResult, holdoutMetric, holdoutErr := a.evaluateAutoResearchMetricCommand(runCtx, runtimeSession, workspacePath, spec, spec.HoldoutCommand)
		ledger.HoldoutResult = &holdoutResult
		if holdoutErr == nil {
			currentHashes, hashErr := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
			if hashErr != nil {
				holdoutErr = hashErr
			} else if !equalAutoResearchHashes(currentHashes, baselineEditableHashes) {
				holdoutErr = fmt.Errorf("editable files changed during frozen holdout baseline evaluation")
			}
		}
		if holdoutErr != nil {
			ledger.Status = "failed"
			ledger.StopReason = "holdout_baseline_failed"
			ledger.FinishedAt = time.Now().UTC()
			if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, editableSnapshots); restoreErr != nil {
				holdoutErr = fmt.Errorf("%w; holdout baseline restore failed: %v", holdoutErr, restoreErr)
			}
			return failAutoResearchRun(task, &ledger, holdoutErr)
		}
		ledger.HoldoutBaseline = &holdoutMetric
		logToContext(ctx, "[%s] AutoResearch frozen holdout baseline captured", a.Name)
	}

	bestSnapshots := editableSnapshots
	candidateSpecJSON := autoResearchCandidateSpecJSON(spec)
	readOnlyContext := autoResearchReadOnlySourceContext(workspacePath, spec)
	rejectedCandidateFeedback := "{}"
	var candidateSession *conversation.Session
	logToContext(ctx, "[%s] AutoResearch baseline %s=%.8g", a.Name, spec.MetricKey, baselineMetric)

	for trialNumber := 1; trialNumber <= spec.MaxTrials; trialNumber++ {
		if runCtx.Err() != nil {
			ledger.StopReason = "wall_time_budget_exhausted"
			break
		}
		proposal, session, proposalErr := a.proposeAutoResearchCandidateWithSession(runCtx, spec, candidateSpecJSON, ledger, bestSnapshots, readOnlyContext, rejectedCandidateFeedback, candidateSession)
		candidateSession = session
		if candidateSession != nil {
			candidateSession.Record(task)
		}
		if proposalErr != nil {
			if runCtx.Err() != nil {
				ledger.StopReason = "wall_time_budget_exhausted"
				break
			}
			now := time.Now().UTC()
			failure := "candidate model output rejected: " + proposalErr.Error()
			ledger.Trials = append(ledger.Trials, models.ResearchTrial{
				Number: trialNumber, Status: "rejected", Decision: "reject", Reason: failure,
				StartedAt: now, FinishedAt: now,
			})
			ledger.CompletedTrials++
			rejectedCandidateFeedback = autoResearchModelFailureFeedback(failure)
			logToContext(ctx, "[%s] AutoResearch trial %d: candidate model output rejected", a.Name, trialNumber)
			continue
		}
		if proposal.Status != "propose" {
			if proposal.Status == "stop" {
				visibleFailures := autoResearchVisibleFailures(ledger.Trials)
				if len(visibleFailures) > 0 {
					now := time.Now().UTC()
					ledger.Trials = append(ledger.Trials, models.ResearchTrial{
						Number: trialNumber, Status: "rejected", Diagnosis: proposal.Diagnosis,
						Hypothesis: proposal.Hypothesis, Decision: "reject",
						Reason:    fmt.Sprintf("premature stop rejected: visible evaluator still reports unresolved cases: %s", strings.Join(visibleFailures, ", ")),
						StartedAt: now, FinishedAt: now,
					})
					ledger.CompletedTrials++
					logToContext(ctx, "[%s] AutoResearch trial %d: premature stop rejected", a.Name, trialNumber)
					continue
				}
			}
			ledger.StopReason = proposal.Status + ": " + chooseNonEmpty(proposal.Reason, "candidate model stopped")
			break
		}

		trial := models.ResearchTrial{
			Number: trialNumber, Status: "running", Diagnosis: proposal.Diagnosis, Hypothesis: proposal.Hypothesis,
			Decision: "reject", Reason: proposal.Reason, StartedAt: time.Now().UTC(),
		}
		patches, applyErr := applyAutoResearchCandidate(workspacePath, proposal, bestSnapshots, len(spec.HoldoutCommand) > 0)
		trial.Patches = patches
		if applyErr != nil {
			trial.Status = "rejected"
			trial.Reason = "candidate rejected before execution: " + applyErr.Error()
			rejectedCandidateFeedback = autoResearchRejectedCandidateFeedback(proposal, trial.Reason, autoResearchEvaluation{})
			trial.FinishedAt = time.Now().UTC()
			ledger.Trials = append(ledger.Trials, trial)
			ledger.CompletedTrials++
			if restoreErr := restoreAutoResearchGroups(bestSnapshots); restoreErr != nil {
				ledger.Status = "failed"
				ledger.StopReason = "candidate_restore_failed"
				return failAutoResearchRun(task, &ledger, fmt.Errorf("candidate rejection restore failed: %w", restoreErr))
			}
			continue
		}
		expectedCandidate, hashErr := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
		if hashErr != nil {
			trial.Status = "rejected"
			trial.Reason = "candidate hash failed: " + hashErr.Error()
			rejectedCandidateFeedback = autoResearchRejectedCandidateFeedback(proposal, trial.Reason, autoResearchEvaluation{})
			trial.FinishedAt = time.Now().UTC()
			ledger.Trials = append(ledger.Trials, trial)
			ledger.CompletedTrials++
			if restoreErr := restoreAutoResearchGroups(bestSnapshots); restoreErr != nil {
				ledger.Status = "failed"
				ledger.StopReason = "candidate_restore_failed"
				return failAutoResearchRun(task, &ledger, fmt.Errorf("candidate hash restore failed: %w", restoreErr))
			}
			continue
		}

		evaluation, evalErr := a.evaluateAutoResearchCandidate(runCtx, runtimeSession, workspacePath, spec)
		trial.GuardResults = evaluation.Guards
		trial.EvalResult = evaluation.Eval
		trial.EvalResults = evaluation.Evals
		trial.MetricSamples = append([]float64(nil), evaluation.Metrics...)
		trial.MetricStdDev = evaluation.MetricStdDev
		trial.MetricAggregation = evaluation.Aggregation
		if integrityErr := verifyAutoResearchSpecFiles(workspacePath, spec); integrityErr != nil {
			trial.Status = "compromised"
			trial.Decision = "abort"
			trial.Reason = integrityErr.Error()
			trial.FinishedAt = time.Now().UTC()
			ledger.Trials = append(ledger.Trials, trial)
			ledger.CompletedTrials++
			ledger.Status = "compromised"
			ledger.StopReason = "protected_file_integrity_failure"
			ledger.FinishedAt = time.Now().UTC()
			if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, bestSnapshots); restoreErr != nil {
				integrityErr = fmt.Errorf("%w; compromised workspace restore failed: %v", integrityErr, restoreErr)
			}
			return failAutoResearchRun(task, &ledger, integrityErr)
		}
		if evalErr != nil && errors.Is(evalErr, errAutoResearchIntegrity) {
			trial.Status = "compromised"
			trial.Decision = "abort"
			trial.Reason = evalErr.Error()
			trial.FinishedAt = time.Now().UTC()
			ledger.Trials = append(ledger.Trials, trial)
			ledger.CompletedTrials++
			ledger.Status = "compromised"
			ledger.StopReason = "workspace_integrity_failure"
			ledger.FinishedAt = time.Now().UTC()
			if restoreErr := restoreAutoResearchWorkspace(workspacePath, spec.EditableFiles, immutableSnapshots, protectedSnapshots, bestSnapshots); restoreErr != nil {
				evalErr = fmt.Errorf("%w; compromised workspace restore failed: %v", evalErr, restoreErr)
			}
			return failAutoResearchRun(task, &ledger, evalErr)
		}
		if evalErr == nil {
			if current, hashErr := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false); hashErr != nil || !equalAutoResearchHashes(current, expectedCandidate) {
				evalErr = fmt.Errorf("editable files changed during frozen evaluation")
			}
		}

		if evalErr != nil {
			trial.Status = "rejected"
			trial.Reason = "candidate execution failed: " + evalErr.Error()
			rejectedCandidateFeedback = autoResearchRejectedCandidateFeedback(proposal, trial.Reason, evaluation)
			if restoreErr := restoreAutoResearchGroups(bestSnapshots); restoreErr != nil {
				trial.Status = "failed"
				trial.Decision = "abort"
				trial.Reason += "; restore failed: " + restoreErr.Error()
				trial.FinishedAt = time.Now().UTC()
				ledger.Trials = append(ledger.Trials, trial)
				ledger.CompletedTrials++
				ledger.Status = "failed"
				ledger.StopReason = "candidate_restore_failed"
				return failAutoResearchRun(task, &ledger, restoreErr)
			}
		} else {
			metric := evaluation.Metric
			delta := autoResearchDelta(metric, ledger.BestScore, spec.Direction)
			trial.Metric = &metric
			trial.DeltaFromBest = &delta
			if autoResearchImproved(metric, ledger.BestScore, spec.Direction, spec.MinDelta) {
				trial.Status = "kept"
				trial.Decision = "keep"
				trial.Reason = fmt.Sprintf("%s improved by %.8g (required %.8g)", spec.MetricKey, delta, spec.MinDelta)
				ledger.BestScore = metric
				ledger.AcceptedTrials++
				newBestSnapshots, snapshotErr := snapshotAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
				if snapshotErr != nil {
					return failAutoResearchRun(task, &ledger, snapshotErr)
				}
				bestSnapshots = newBestSnapshots
				rejectedCandidateFeedback = "{}"
			} else {
				trial.Status = "rejected"
				trial.Reason = fmt.Sprintf("%s delta %.8g did not meet %.8g", spec.MetricKey, delta, spec.MinDelta)
				rejectedCandidateFeedback = autoResearchRejectedCandidateFeedback(proposal, trial.Reason, evaluation)
				if restoreErr := restoreAutoResearchGroups(bestSnapshots); restoreErr != nil {
					trial.Status = "failed"
					trial.Decision = "abort"
					trial.Reason += "; restore failed: " + restoreErr.Error()
					trial.FinishedAt = time.Now().UTC()
					ledger.Trials = append(ledger.Trials, trial)
					ledger.CompletedTrials++
					ledger.Status = "failed"
					ledger.StopReason = "candidate_restore_failed"
					return failAutoResearchRun(task, &ledger, restoreErr)
				}
			}
		}
		trial.FinishedAt = time.Now().UTC()
		ledger.Trials = append(ledger.Trials, trial)
		ledger.CompletedTrials++
		logToContext(ctx, "[%s] AutoResearch trial %d: %s", a.Name, trialNumber, trial.Status)
		if trial.Status == "kept" && autoResearchTargetReached(spec.TargetScore, ledger.BestScore, spec.Direction) {
			ledger.StopReason = "target_score_reached"
			break
		}
	}

	if ledger.StopReason == "" {
		ledger.StopReason = "trial_budget_exhausted"
	}
	ledger.Status = "completed"
	ledger.FinishedAt = time.Now().UTC()
	finalizeAutoResearchLedgerUsage(&ledger)
	bestHashes, err := hashAutoResearchFiles(workspacePath, spec.EditableFiles, autoResearchMaxFileBytes, false)
	if err != nil {
		return failAutoResearchRun(task, &ledger, err)
	}
	ledger.BestCandidateFiles = bestHashes
	best := models.ResearchBestCandidate{
		SpecSHA256: specHash, Score: ledger.BestScore, MetricKey: spec.MetricKey,
		Direction: spec.Direction, AcceptedTrials: ledger.AcceptedTrials, Files: bestHashes,
	}
	ledgerJSON, _ := json.Marshal(ledger)
	bestJSON, _ := json.Marshal(best)
	report := fmt.Sprintf("AutoResearch completed %d/%d candidate trial(s); kept %d; %s %.8g -> %.8g; search=%dx%s; commands=%d; command_time=%dms; stop=%s", ledger.CompletedTrials, spec.MaxTrials, ledger.AcceptedTrials, spec.MetricKey, ledger.BaselineScore, ledger.BestScore, spec.SearchRuns, spec.SearchAggregation, ledger.ResourceUsage.CommandRuns, ledger.ResourceUsage.CommandDurationMS, ledger.StopReason)

	task.Result = string(ledgerJSON)
	task.StructuredData = string(ledgerJSON)
	if len(bestSnapshots) > 0 {
		task.Code = string(bestSnapshots[0].Content)
	}
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"research_trial_ledger":   string(ledgerJSON),
		"research_best_candidate": string(bestJSON),
		"research_run_report":     report,
	})
	return nil
}
