package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

const (
	autoResearchDefaultTrials         = 3
	autoResearchMaxTrials             = 8
	autoResearchDefaultWallSeconds    = 900
	autoResearchMaxWallSeconds        = 3600
	autoResearchDefaultValidationRuns = 1
	autoResearchMaxValidationRuns     = 5
	autoResearchDefaultSearchRuns     = 1
	autoResearchMaxSearchRuns         = 5
	autoResearchMaxEditableFiles      = 8
	autoResearchMaxProtectedFiles     = 64
	autoResearchMaxPatchFiles         = 3
	autoResearchMaxFileBytes          = 96 * 1024
	autoResearchMaxProtectedBytes     = 16 * 1024 * 1024
	autoResearchOutputPreviewBytes    = 5000
	autoResearchMaxImmutableFiles     = 10000
	autoResearchMaxImmutableBytes     = 256 * 1024 * 1024
	autoResearchMaxReadOnlyFileBytes  = 48 * 1024
	autoResearchMaxReadOnlyBytes      = 96 * 1024
)

var (
	autoResearchMetricKeyRE          = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	autoResearchDependencyRE         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*(?:\[[A-Za-z0-9_,.-]+\])?(?:(?:==|~=|>=|<=|>|<)[A-Za-z0-9.*+!_-]+)?$`)
	autoResearchRepositoryRevisionRE = regexp.MustCompile(`^(?:[A-Fa-f0-9]{40}|[A-Fa-f0-9]{64})$`)
	errAutoResearchIntegrity         = errors.New("AutoResearch workspace integrity failure")
)

type autoResearchCandidatePatch struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

type autoResearchCandidateResponse struct {
	Status     string                       `json:"status"`
	Diagnosis  string                       `json:"diagnosis"`
	Hypothesis string                       `json:"hypothesis"`
	Reason     string                       `json:"reason"`
	Patches    []autoResearchCandidatePatch `json:"patches"`
}

type autoResearchFileSnapshot struct {
	Workspace string
	Relative  string
	Path      string
	Content   []byte
	Mode      os.FileMode
}

type autoResearchEvaluation struct {
	Guards       []models.ResearchCommandResult
	Eval         models.ResearchCommandResult
	Evals        []models.ResearchCommandResult
	Metric       float64
	Metrics      []float64
	MetricStdDev float64
	Aggregation  string
}

type autoResearchSourceFile struct {
	path     string
	relative string
	size     int64
}

func (a *ResearchCodingAgent) executeAutoResearchSpec(ctx context.Context, task *models.Task) error {
	workspacePath, err := benchmarkWorkspace(task)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	raw, source, err := locateAutoResearchSpec(task, workspacePath)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	spec, err := decodeAutoResearchSpec(raw)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	if err := normalizeAndFreezeAutoResearchSpec(task, workspacePath, source, raw, &spec); err != nil {
		return failResearchCodingTask(task, err)
	}

	specJSON, _ := json.Marshal(spec)
	dependenciesJSON, _ := json.Marshal(spec.Dependencies)
	specHash := autoResearchSHA(specJSON)
	report := fmt.Sprintf("frozen AutoResearch spec %s (%s): %d editable file(s), %d protected file(s), max %d trial(s), %ds wall time, search=%dx%s, validation=%d run(s)", spec.Name, specHash[:12], len(spec.EditableFiles), len(spec.ProtectedFiles), spec.MaxTrials, spec.MaxWallSeconds, spec.SearchRuns, spec.SearchAggregation, spec.ValidationRuns)

	task.Result = report
	task.StructuredData = string(specJSON)
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{
		"research_spec":        string(specJSON),
		"research_spec_report": report,
		"dependency_spec":      string(dependenciesJSON),
	})
	logToContext(ctx, "[%s] %s", a.Name, report)
	return nil
}

func locateAutoResearchSpec(task *models.Task, workspacePath string) ([]byte, string, error) {
	for _, key := range []string{"research_spec", "autoresearch_spec"} {
		if task != nil && task.Inputs != nil && task.Inputs[key] != nil {
			value := task.Inputs[key]
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return []byte(text), "task_input:" + key, nil
			}
			raw, err := json.Marshal(value)
			if err == nil && len(raw) > 0 && string(raw) != "null" {
				return raw, "task_input:" + key, nil
			}
		}
	}

	candidates := make([]string, 0, 8)
	for _, key := range []string{"research_spec_path", "autoresearch_spec_path"} {
		if value := strings.TrimSpace(extractTaskInputLike(task, key)); value != "" {
			candidates = append(candidates, value)
		}
	}
	candidates = append(candidates, ".research-assistant/autoresearch/spec.json", "autoresearch.json")
	uploadDirectory := filepath.Join(workspacePath, ".research-assistant", "uploads")
	if entries, err := os.ReadDir(uploadDirectory); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && strings.EqualFold(filepath.Ext(entry.Name()), ".json") {
				candidates = append(candidates, filepath.ToSlash(filepath.Join(".research-assistant", "uploads", entry.Name())))
			}
		}
	}

	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		relative, path, err := autoResearchExistingFile(workspacePath, candidate, autoResearchMaxProtectedBytes, true)
		if err != nil {
			continue
		}
		if _, duplicate := seen[relative]; duplicate {
			continue
		}
		seen[relative] = struct{}{}
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var header struct {
			Version string `json:"version"`
		}
		if json.Unmarshal(raw, &header) == nil && header.Version == models.AutoResearchSpecVersion {
			return raw, relative, nil
		}
	}
	return nil, "", fmt.Errorf("AutoResearch spec not found; add autoresearch.json to the repository or upload an %s JSON file", models.AutoResearchSpecVersion)
}

func decodeAutoResearchSpec(raw []byte) (models.ResearchSpec, error) {
	var spec models.ResearchSpec
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&spec); err != nil {
		return spec, fmt.Errorf("decode AutoResearch spec: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return spec, fmt.Errorf("decode AutoResearch spec: multiple JSON values are not allowed")
	}
	return spec, nil
}

func validateAutoResearchRepositoryRevision(task *models.Task, revision string) error {
	if revision == "" {
		return nil
	}
	raw := strings.TrimSpace(extractTaskInputLike(task, "repo_manifest"))
	if raw == "" {
		return fmt.Errorf("AutoResearch repository_revision requires repo_manifest evidence")
	}
	var manifest struct {
		RequestedRevision string `json:"requested_revision"`
		RepositoryCommit  string `json:"repository_commit"`
	}
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return fmt.Errorf("decode repo_manifest for repository revision: %w", err)
	}
	if requested := strings.TrimSpace(manifest.RequestedRevision); requested != "" && !strings.EqualFold(requested, revision) {
		return fmt.Errorf("repo_manifest requested revision %q does not match AutoResearch repository_revision %q", requested, revision)
	}
	if !strings.EqualFold(strings.TrimSpace(manifest.RepositoryCommit), revision) {
		return fmt.Errorf("repo_manifest commit %q does not match AutoResearch repository_revision %q", manifest.RepositoryCommit, revision)
	}
	return nil
}

func normalizeAndFreezeAutoResearchSpec(task *models.Task, workspacePath, source string, raw []byte, spec *models.ResearchSpec) error {
	if spec == nil {
		return fmt.Errorf("AutoResearch spec is nil")
	}
	if spec.Version != models.AutoResearchSpecVersion {
		return fmt.Errorf("unsupported AutoResearch spec version %q", spec.Version)
	}
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Objective = strings.TrimSpace(spec.Objective)
	spec.RepositoryRevision = strings.ToLower(strings.TrimSpace(spec.RepositoryRevision))
	if spec.Name == "" || spec.Objective == "" {
		return fmt.Errorf("AutoResearch spec requires name and objective")
	}
	if len(spec.Name) > 120 || len(spec.Objective) > 2000 {
		return fmt.Errorf("AutoResearch spec name or objective is too long")
	}
	if spec.RepositoryRevision != "" && !autoResearchRepositoryRevisionRE.MatchString(spec.RepositoryRevision) {
		return fmt.Errorf("AutoResearch repository_revision must be a full 40- or 64-character commit SHA")
	}
	if err := validateAutoResearchRepositoryRevision(task, spec.RepositoryRevision); err != nil {
		return err
	}

	direction := strings.ToLower(strings.TrimSpace(spec.Direction))
	if direction == "" {
		direction = "maximize"
	}
	if direction != "maximize" && direction != "minimize" {
		return fmt.Errorf("AutoResearch direction must be maximize or minimize")
	}
	spec.Direction = direction
	spec.MetricKey = strings.TrimSpace(spec.MetricKey)
	if !autoResearchMetricKeyRE.MatchString(spec.MetricKey) {
		return fmt.Errorf("AutoResearch metric_key is invalid")
	}
	if math.IsNaN(spec.MinDelta) || math.IsInf(spec.MinDelta, 0) || spec.MinDelta < 0 {
		return fmt.Errorf("AutoResearch min_delta must be a finite non-negative number")
	}
	if spec.TargetScore != nil && (math.IsNaN(*spec.TargetScore) || math.IsInf(*spec.TargetScore, 0)) {
		return fmt.Errorf("AutoResearch target_score must be finite when provided")
	}
	if spec.SearchRuns <= 0 {
		spec.SearchRuns = autoResearchDefaultSearchRuns
	}
	if spec.SearchRuns > autoResearchMaxSearchRuns {
		spec.SearchRuns = autoResearchMaxSearchRuns
	}
	spec.SearchAggregation = strings.ToLower(strings.TrimSpace(spec.SearchAggregation))
	if spec.SearchAggregation == "" {
		spec.SearchAggregation = "mean"
	}
	if !validAutoResearchSearchAggregation(spec.SearchAggregation) {
		return fmt.Errorf("AutoResearch search_aggregation must be mean, median, or worst")
	}

	inputMaxTrials := boundedAutoResearchInput(task, "autoresearch_max_trials", autoResearchDefaultTrials, 1, autoResearchMaxTrials)
	if spec.MaxTrials <= 0 {
		spec.MaxTrials = inputMaxTrials
	} else if spec.MaxTrials > inputMaxTrials {
		spec.MaxTrials = inputMaxTrials
	}
	if spec.MaxTrials > autoResearchMaxTrials {
		spec.MaxTrials = autoResearchMaxTrials
	}
	inputWallSeconds := boundedAutoResearchInput(task, "autoresearch_max_wall_seconds", autoResearchDefaultWallSeconds, 1, autoResearchMaxWallSeconds)
	if spec.MaxWallSeconds <= 0 {
		spec.MaxWallSeconds = inputWallSeconds
	} else if spec.MaxWallSeconds > inputWallSeconds {
		spec.MaxWallSeconds = inputWallSeconds
	}
	if spec.MaxWallSeconds > autoResearchMaxWallSeconds {
		spec.MaxWallSeconds = autoResearchMaxWallSeconds
	}
	requestedValidationRuns, hasValidationRunLimit := optionalBoundedAutoResearchInput(task, "autoresearch_validation_runs", 1, autoResearchMaxValidationRuns)
	if spec.ValidationRuns <= 0 {
		spec.ValidationRuns = autoResearchDefaultValidationRuns
		if hasValidationRunLimit {
			spec.ValidationRuns = requestedValidationRuns
		}
	} else if hasValidationRunLimit && spec.ValidationRuns > requestedValidationRuns {
		spec.ValidationRuns = requestedValidationRuns
	}
	if spec.ValidationRuns > autoResearchMaxValidationRuns {
		spec.ValidationRuns = autoResearchMaxValidationRuns
	}

	if err := validateAutoResearchCommand(spec.EvalCommand); err != nil {
		return fmt.Errorf("invalid eval_command: %w", err)
	}
	if len(spec.HoldoutCommand) > 0 {
		if err := validateAutoResearchCommand(spec.HoldoutCommand); err != nil {
			return fmt.Errorf("invalid holdout_command: %w", err)
		}
		if equalAutoResearchCommands(spec.HoldoutCommand, spec.EvalCommand) {
			return fmt.Errorf("holdout_command must differ from eval_command")
		}
		if spec.HoldoutMinDelta != nil && (*spec.HoldoutMinDelta < 0 || math.IsNaN(*spec.HoldoutMinDelta) || math.IsInf(*spec.HoldoutMinDelta, 0)) {
			return fmt.Errorf("holdout_min_delta must be finite and non-negative")
		}
	} else if spec.HoldoutMinDelta != nil {
		return fmt.Errorf("holdout_min_delta requires holdout_command")
	}
	if len(spec.GuardCommands) > 6 {
		return fmt.Errorf("AutoResearch supports at most 6 guard commands")
	}
	for index, command := range spec.GuardCommands {
		if err := validateAutoResearchCommand(command); err != nil {
			return fmt.Errorf("invalid guard_commands[%d]: %w", index, err)
		}
	}
	for _, dependency := range spec.Dependencies {
		if !autoResearchDependencyRE.MatchString(strings.TrimSpace(dependency)) {
			return fmt.Errorf("unsafe or unsupported dependency %q", dependency)
		}
	}
	if len(spec.Dependencies) > 32 {
		return fmt.Errorf("AutoResearch supports at most 32 dependencies")
	}
	spec.Dependencies = cleanAutoResearchStrings(spec.Dependencies)

	editable, err := normalizeAutoResearchPaths(workspacePath, spec.EditableFiles, autoResearchMaxEditableFiles, autoResearchMaxFileBytes, false)
	if err != nil {
		return fmt.Errorf("editable_files: %w", err)
	}
	if len(editable) == 0 {
		return fmt.Errorf("AutoResearch requires at least one editable file")
	}
	protectedInputs := append([]string(nil), spec.ProtectedFiles...)
	if source != "" && !strings.HasPrefix(source, "task_input:") {
		protectedInputs = append(protectedInputs, source)
	}
	protected, err := normalizeAutoResearchPaths(workspacePath, protectedInputs, autoResearchMaxProtectedFiles, autoResearchMaxProtectedBytes, true)
	if err != nil {
		return fmt.Errorf("protected_files: %w", err)
	}
	if len(protected) == 0 {
		return fmt.Errorf("AutoResearch requires at least one protected evaluator or data file")
	}
	editableSet := make(map[string]struct{}, len(editable))
	for _, relative := range editable {
		editableSet[relative] = struct{}{}
	}
	for _, relative := range protected {
		if _, overlap := editableSet[relative]; overlap {
			return fmt.Errorf("file %s cannot be both editable and protected", relative)
		}
	}
	if err := validateAutoResearchEvaluatorScope(workspacePath, spec.EvalCommand, editableSet); err != nil {
		return err
	}
	if len(spec.HoldoutCommand) > 0 {
		if err := validateAutoResearchEvaluatorScope(workspacePath, spec.HoldoutCommand, editableSet); err != nil {
			return fmt.Errorf("holdout_command: %w", err)
		}
		if err := validateAutoResearchCommandProtectedScope(workspacePath, spec.HoldoutCommand, autoResearchPathSet(protected)); err != nil {
			return err
		}
	}

	spec.EditableFiles = editable
	spec.ProtectedFiles = protected
	spec.FrozenProtected, err = hashAutoResearchFiles(workspacePath, protected, autoResearchMaxProtectedBytes, true)
	if err != nil {
		return err
	}
	spec.FrozenWorkspace, err = autoResearchImmutableFingerprint(workspacePath, editable)
	if err != nil {
		return err
	}
	spec.Source = source
	spec.SourceSHA256 = autoResearchSHA(raw)
	spec.CreatedAt = time.Now().UTC()
	return nil
}
