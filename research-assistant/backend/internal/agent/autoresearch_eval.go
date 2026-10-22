package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

func (a *ResearchCodingAgent) evaluateAutoResearchCandidate(ctx context.Context, runtimeSession, workspacePath string, spec models.ResearchSpec) (autoResearchEvaluation, error) {
	return a.evaluateAutoResearchWithCommandRuns(ctx, runtimeSession, workspacePath, spec, spec.EvalCommand, spec.SearchRuns, spec.SearchAggregation)
}

func (a *ResearchCodingAgent) evaluateAutoResearchWithCommand(ctx context.Context, runtimeSession, workspacePath string, spec models.ResearchSpec, evalCommand []string) (autoResearchEvaluation, error) {
	return a.evaluateAutoResearchWithCommandRuns(ctx, runtimeSession, workspacePath, spec, evalCommand, 1, "mean")
}

func (a *ResearchCodingAgent) evaluateAutoResearchWithCommandRuns(ctx context.Context, runtimeSession, workspacePath string, spec models.ResearchSpec, evalCommand []string, runs int, aggregation string) (autoResearchEvaluation, error) {
	result := autoResearchEvaluation{
		Guards: []models.ResearchCommandResult{}, Evals: []models.ResearchCommandResult{},
		Metrics: []float64{}, Aggregation: aggregation,
	}
	if runs < 1 || runs > autoResearchMaxSearchRuns || !validAutoResearchSearchAggregation(aggregation) {
		return result, fmt.Errorf("invalid search measurement contract")
	}
	if err := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace); err != nil {
		return result, err
	}
	for _, command := range spec.GuardCommands {
		commandResult, _, err := a.runAutoResearchCommand(ctx, runtimeSession, command)
		result.Guards = append(result.Guards, commandResult)
		if integrityErr := verifyAutoResearchSpecFiles(workspacePath, spec); integrityErr != nil {
			return result, integrityErr
		}
		if integrityErr := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace); integrityErr != nil {
			return result, integrityErr
		}
		if err != nil {
			return result, fmt.Errorf("guard command failed: %w", err)
		}
	}
	for runNumber := 1; runNumber <= runs; runNumber++ {
		evalResult, metric, err := a.evaluateAutoResearchMetricCommand(ctx, runtimeSession, workspacePath, spec, evalCommand)
		result.Eval = evalResult
		result.Evals = append(result.Evals, evalResult)
		if err != nil {
			return result, fmt.Errorf("evaluator run %d/%d failed: %w", runNumber, runs, err)
		}
		result.Metrics = append(result.Metrics, metric)
	}
	metric, err := aggregateAutoResearchScores(result.Metrics, aggregation, spec.Direction)
	if err != nil {
		return result, err
	}
	result.Metric = metric
	_, result.MetricStdDev, _, _ = autoResearchScoreStats(result.Metrics)
	return result, nil
}

func (a *ResearchCodingAgent) evaluateAutoResearchMetricCommand(ctx context.Context, runtimeSession, workspacePath string, spec models.ResearchSpec, command []string) (models.ResearchCommandResult, float64, error) {
	evalResult, stdout, err := a.runAutoResearchCommand(ctx, runtimeSession, command)
	if integrityErr := verifyAutoResearchSpecFiles(workspacePath, spec); integrityErr != nil {
		return evalResult, 0, integrityErr
	}
	if integrityErr := verifyAutoResearchImmutableFingerprint(workspacePath, spec.EditableFiles, spec.FrozenWorkspace); integrityErr != nil {
		return evalResult, 0, integrityErr
	}
	if err != nil {
		return evalResult, 0, err
	}
	metric, err := parseAutoResearchMetric(stdout, spec.MetricKey)
	if err != nil {
		return evalResult, 0, err
	}
	return evalResult, metric, nil
}

func (a *ResearchCodingAgent) runAutoResearchCommand(ctx context.Context, runtimeSession string, command []string) (models.ResearchCommandResult, string, error) {
	result := models.ResearchCommandResult{Command: append([]string(nil), command...), ExitCode: -1}
	if err := validateAutoResearchCommand(command); err != nil {
		result.Error = err.Error()
		return result, "", err
	}
	quoted := make([]string, 0, len(command))
	for _, argument := range command {
		quoted = append(quoted, shellEscape(argument))
	}
	shellCommand := "cd /workspace && PYTHONPATH=/workspace:${PYTHONPATH:-} " + strings.Join(quoted, " ")
	started := time.Now()
	response, err := a.Sandbox.ExecCommandStream(ctx, runtimeSession, []string{"bash", "-lc", shellCommand}, func(stream, line string) {
		logToContext(ctx, "[%s] AutoResearch %s: %s", a.Name, stream, line)
	})
	result.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result, "", err
	}
	if response == nil {
		err := fmt.Errorf("sandbox returned nil response")
		result.Error = err.Error()
		return result, "", err
	}
	result.ExitCode = response.ExitCode
	result.StdoutPreview = truncatePaperDebugText(strings.TrimSpace(response.Stdout), autoResearchOutputPreviewBytes)
	result.StderrPreview = truncatePaperDebugText(strings.TrimSpace(response.Stderr), autoResearchOutputPreviewBytes)
	if response.ExitCode != 0 {
		err := fmt.Errorf("command exited with code %d: %s", response.ExitCode, chooseNonEmpty(result.StderrPreview, result.StdoutPreview))
		result.Error = err.Error()
		return result, response.Stdout, err
	}
	return result, response.Stdout, nil
}

func validateAutoResearchCommand(command []string) error {
	if len(command) == 0 || len(command) > 64 {
		return fmt.Errorf("command must contain 1-64 arguments")
	}
	allowed := map[string]struct{}{
		"python": {}, "python3": {}, "pytest": {}, "go": {}, "node": {}, "npm": {},
		"pnpm": {}, "yarn": {}, "cargo": {}, "make": {},
	}
	if _, ok := allowed[strings.TrimSpace(command[0])]; !ok {
		return fmt.Errorf("executable %q is not allowed", command[0])
	}
	for _, argument := range command {
		if argument == "" || len(argument) > 2048 || strings.ContainsAny(argument, "\x00\r\n") {
			return fmt.Errorf("command contains an empty, oversized or multiline argument")
		}
	}
	return nil
}

func equalAutoResearchCommands(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func autoResearchCommandFileCandidates(command []string) []string {
	candidates := append([]string(nil), command[1:]...)
	for index, argument := range command[1:] {
		if argument == "-m" && index+2 < len(command) {
			modulePath := strings.ReplaceAll(command[index+2], ".", "/") + ".py"
			candidates = append(candidates, modulePath)
		}
	}
	return candidates
}

func validateAutoResearchEvaluatorScope(workspacePath string, command []string, editable map[string]struct{}) error {
	for _, candidate := range autoResearchCommandFileCandidates(command) {
		if candidate == "" || strings.HasPrefix(candidate, "-") {
			continue
		}
		relative, _, err := autoResearchExistingFile(workspacePath, candidate, autoResearchMaxProtectedBytes, true)
		if err != nil {
			continue
		}
		if _, mutable := editable[relative]; mutable {
			return fmt.Errorf("eval_command cannot execute editable file %s as its evaluator", relative)
		}
	}
	return nil
}

func validateAutoResearchCommandProtectedScope(workspacePath string, command []string, protected map[string]struct{}) error {
	referencedFiles := 0
	for _, candidate := range autoResearchCommandFileCandidates(command) {
		if candidate == "" || strings.HasPrefix(candidate, "-") {
			continue
		}
		relative, _, err := autoResearchExistingFile(workspacePath, candidate, autoResearchMaxProtectedBytes, true)
		if err != nil {
			continue
		}
		referencedFiles++
		if _, frozen := protected[relative]; !frozen {
			return fmt.Errorf("holdout_command file %s must be listed in protected_files", relative)
		}
	}
	if referencedFiles == 0 {
		return fmt.Errorf("holdout_command must directly reference at least one protected workspace file")
	}
	return nil
}

func autoResearchPathSet(paths []string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		set[path] = struct{}{}
	}
	return set
}

func parseAutoResearchMetric(stdout, metricKey string) (float64, error) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		var payload map[string]any
		if err := decoder.Decode(&payload); err != nil {
			continue
		}
		value, ok := autoResearchMetricValue(payload, metricKey)
		if !ok {
			continue
		}
		var metric float64
		switch typed := value.(type) {
		case json.Number:
			parsed, err := typed.Float64()
			if err != nil {
				return 0, fmt.Errorf("metric %s is not numeric", metricKey)
			}
			metric = parsed
		case float64:
			metric = typed
		default:
			return 0, fmt.Errorf("metric %s is not numeric", metricKey)
		}
		if math.IsNaN(metric) || math.IsInf(metric, 0) {
			return 0, fmt.Errorf("metric %s is not finite", metricKey)
		}
		return metric, nil
	}
	return 0, fmt.Errorf("evaluator stdout has no final JSON object containing metric %s", metricKey)
}

func autoResearchMetricValue(payload map[string]any, key string) (any, bool) {
	if value, ok := payload[key]; ok {
		return value, true
	}
	var current any = payload
	for _, part := range strings.Split(key, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[part]
		if !ok {
			return nil, false
		}
	}
	return current, true
}
