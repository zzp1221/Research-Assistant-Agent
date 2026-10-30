package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
)

func applyAutoResearchCandidate(workspacePath string, proposal autoResearchCandidateResponse, best []autoResearchFileSnapshot, hiddenHoldout bool) ([]models.ResearchPatch, error) {
	allowed := make(map[string]autoResearchFileSnapshot, len(best))
	for _, snapshot := range best {
		allowed[snapshot.Relative] = snapshot
	}
	seen := map[string]struct{}{}
	type pendingPatch struct {
		proposal autoResearchCandidatePatch
		snapshot autoResearchFileSnapshot
		content  []byte
	}
	pending := make([]pendingPatch, 0, len(proposal.Patches))
	for _, patch := range proposal.Patches {
		relative, path, err := autoResearchExistingFile(workspacePath, patch.Path, autoResearchMaxFileBytes, false)
		if err != nil {
			return nil, err
		}
		snapshot, ok := allowed[relative]
		if !ok || path != snapshot.Path {
			return nil, fmt.Errorf("candidate patch targets non-editable file %s", relative)
		}
		if _, duplicate := seen[relative]; duplicate {
			return nil, fmt.Errorf("candidate contains duplicate patch %s", relative)
		}
		seen[relative] = struct{}{}
		if strings.TrimSpace(patch.Reason) == "" {
			return nil, fmt.Errorf("candidate patch %s has no reason", relative)
		}
		content := []byte(patch.Content)
		if len(content) == 0 || len(content) > autoResearchMaxFileBytes {
			return nil, fmt.Errorf("candidate patch %s is empty or too large", relative)
		}
		if bytes.Equal(content, snapshot.Content) {
			continue
		}
		if err := validatePaperCodePatchPolicy(string(snapshot.Content), string(content)); err != nil {
			return nil, fmt.Errorf("candidate patch %s violates policy: %w", relative, err)
		}
		if hiddenHoldout {
			if err := validateAutoResearchHiddenEvaluationPolicy(string(snapshot.Content), string(content)); err != nil {
				return nil, fmt.Errorf("candidate patch %s violates hidden-evaluation policy: %w", relative, err)
			}
		}
		pending = append(pending, pendingPatch{proposal: patch, snapshot: snapshot, content: content})
	}
	if len(pending) == 0 {
		return nil, fmt.Errorf("candidate produced no effective editable-file change")
	}

	records := make([]models.ResearchPatch, 0, len(pending))
	for _, patch := range pending {
		if err := writePaperDebugFile(patch.snapshot.Path, patch.content, patch.snapshot.Mode); err != nil {
			if restoreErr := restoreAutoResearchGroups(best); restoreErr != nil {
				return nil, fmt.Errorf("apply candidate failed: %w; restore failed: %v", err, restoreErr)
			}
			return nil, err
		}
		records = append(records, models.ResearchPatch{
			Path: patch.snapshot.Relative, Reason: strings.TrimSpace(patch.proposal.Reason),
			BeforeSHA256: autoResearchSHA(patch.snapshot.Content), AfterSHA256: autoResearchSHA(patch.content),
		})
	}
	return records, nil
}

func validateAutoResearchHiddenEvaluationPolicy(original, patched string) error {
	lowerOriginal := strings.ToLower(original)
	lowerPatched := strings.ToLower(patched)
	for _, forbidden := range []string{
		".research-assistant/", ".research-assistant\\", "holdout", "/proc/", "sys.argv", "__file__",
		"os.listdir(", "os.scandir(", "os.walk(", "glob.glob(", ".rglob(", "inspect.",
	} {
		if strings.Contains(lowerPatched, forbidden) && !strings.Contains(lowerOriginal, forbidden) {
			return fmt.Errorf("introduced evaluator-discovery construct %q", forbidden)
		}
	}
	return nil
}

func autoResearchImmutableFingerprint(workspacePath string, editableFiles []string) (string, error) {
	files, err := autoResearchImmutableSourceFiles(workspacePath, editableFiles)
	if err != nil {
		return "", err
	}
	hasher := sha256.New()
	for _, file := range files {
		hasher.Write([]byte(file.relative))
		hasher.Write([]byte{0})
		hasher.Write([]byte(fmt.Sprint(file.size)))
		hasher.Write([]byte{0})
		handle, err := os.Open(file.path)
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(hasher, handle)
		closeErr := handle.Close()
		if copyErr != nil {
			return "", copyErr
		}
		if closeErr != nil {
			return "", closeErr
		}
		hasher.Write([]byte{0})
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

func autoResearchImmutableSourceFiles(workspacePath string, editableFiles []string) ([]autoResearchSourceFile, error) {
	editable := make(map[string]struct{}, len(editableFiles))
	for _, relative := range editableFiles {
		editable[filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))] = struct{}{}
	}
	files := make([]autoResearchSourceFile, 0, 256)
	var totalBytes int64
	err := filepath.WalkDir(workspacePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(workspacePath, path)
		if err != nil {
			return err
		}
		relativeSlash := filepath.ToSlash(relative)
		if entry.IsDir() {
			switch strings.ToLower(entry.Name()) {
			case ".git", ".research-assistant", "__pycache__", ".venv", "venv", "node_modules", "dist", "build", "target":
				if relative != "." {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if _, mutable := editable[relativeSlash]; mutable {
			return nil
		}
		if !autoResearchImmutableFile(relativeSlash) {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("AutoResearch immutable source cannot be a symbolic link: %s", relativeSlash)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if len(files) >= autoResearchMaxImmutableFiles || totalBytes+info.Size() > autoResearchMaxImmutableBytes {
			return fmt.Errorf("AutoResearch immutable source fingerprint exceeds %d files or %d bytes", autoResearchMaxImmutableFiles, autoResearchMaxImmutableBytes)
		}
		totalBytes += info.Size()
		files = append(files, autoResearchSourceFile{path: path, relative: relativeSlash, size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relative < files[j].relative })
	return files, nil
}

func snapshotAutoResearchImmutableFiles(workspacePath string, editableFiles []string) ([]autoResearchFileSnapshot, error) {
	files, err := autoResearchImmutableSourceFiles(workspacePath, editableFiles)
	if err != nil {
		return nil, err
	}
	snapshots := make([]autoResearchFileSnapshot, 0, len(files))
	for _, file := range files {
		raw, err := os.ReadFile(file.path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(file.path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, autoResearchFileSnapshot{
			Workspace: workspacePath, Relative: file.relative, Path: file.path, Content: raw, Mode: info.Mode(),
		})
	}
	return snapshots, nil
}

func autoResearchImmutableFile(relative string) bool {
	base := strings.ToLower(filepath.Base(relative))
	switch base {
	case "makefile", "dockerfile", "go.mod", "go.sum", "package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "pyproject.toml", "requirements.txt":
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".py", ".pyi", ".go", ".js", ".jsx", ".ts", ".tsx", ".rs", ".java", ".c", ".cc", ".cpp", ".h", ".hpp", ".sh", ".toml", ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func verifyAutoResearchImmutableFingerprint(workspacePath string, editableFiles []string, expected string) error {
	current, err := autoResearchImmutableFingerprint(workspacePath, editableFiles)
	if err != nil {
		return fmt.Errorf("%w: immutable source fingerprint failed: %v", errAutoResearchIntegrity, err)
	}
	if current != expected {
		return fmt.Errorf("%w: non-editable repository source changed during frozen evaluation", errAutoResearchIntegrity)
	}
	return nil
}

func autoResearchSourceContext(snapshots []autoResearchFileSnapshot) string {
	budget := agentEnvInt("AUTORESEARCH_SOURCE_MAX_TOKENS", 50000)
	perFile := budget
	if len(snapshots) > 0 {
		perFile = budget / len(snapshots)
		if perFile < 2000 {
			perFile = 2000
		}
	}
	files := make([]map[string]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		content := string(snapshot.Content)
		if perFile > 0 {
			content = truncateToTokens(content, perFile)
		}
		files = append(files, map[string]string{
			"path": filepath.ToSlash(snapshot.Relative), "content": content,
		})
	}
	encoded, _ := json.Marshal(files)
	return string(encoded)
}

// autoResearchLedgerSummaryJSON builds a compact, bounded ledger summary. It
// keeps only the fields the model needs and drops old trials first when the
// token budget is exceeded, so the JSON always stays valid.
func autoResearchLedgerSummaryJSON(ledger models.ResearchTrialLedger) string {
	budget := agentEnvInt("AUTORESEARCH_LEDGER_MAX_TOKENS", 4000)
	recent := make([]map[string]any, 0, 2)
	for _, trial := range recentAutoResearchTrials(ledger.Trials, 2) {
		recent = append(recent, map[string]any{
			"number":     trial.Number,
			"status":     trial.Status,
			"hypothesis": truncateToRunes(trial.Hypothesis, 500),
			"decision":   trial.Decision,
			"reason":     truncateToRunes(trial.Reason, 500),
			"metric":     trial.Metric,
			"delta":      trial.DeltaFromBest,
		})
	}
	counter := prompts.EstimatedCounter{}
	for {
		payload := map[string]any{
			"baseline_score":            ledger.BaselineScore,
			"best_score":                ledger.BestScore,
			"completed_trials":          ledger.CompletedTrials,
			"accepted_trials":           ledger.AcceptedTrials,
			"recent_trials":             recent,
			"latest_evaluator_evidence": latestAutoResearchEvaluatorEvidence(ledger.Trials),
		}
		encoded, err := json.Marshal(payload)
		if err != nil {
			return "{}"
		}
		if counter.Count(string(encoded)) <= budget || len(recent) == 0 {
			return string(encoded)
		}
		recent = recent[1:]
	}
}

func autoResearchCandidateSpecJSON(spec models.ResearchSpec) []byte {
	publicSpec := map[string]any{
		"version":            spec.Version,
		"name":               spec.Name,
		"objective":          spec.Objective,
		"editable_files":     spec.EditableFiles,
		"eval_command":       spec.EvalCommand,
		"guard_commands":     spec.GuardCommands,
		"metric_key":         spec.MetricKey,
		"direction":          spec.Direction,
		"min_delta":          spec.MinDelta,
		"target_score":       spec.TargetScore,
		"search_runs":        spec.SearchRuns,
		"search_aggregation": spec.SearchAggregation,
		"max_trials":         spec.MaxTrials,
		"max_wall_seconds":   spec.MaxWallSeconds,
		"holdout_validation": len(spec.HoldoutCommand) > 0,
	}
	encoded, _ := json.Marshal(publicSpec)
	return encoded
}

func autoResearchReadOnlySourceContext(workspacePath string, spec models.ResearchSpec) string {
	allowedExtensions := map[string]struct{}{
		".c": {}, ".cc": {}, ".cpp": {}, ".go": {}, ".js": {}, ".jsx": {},
		".mjs": {}, ".cjs": {}, ".py": {}, ".rs": {}, ".sh": {}, ".ts": {}, ".tsx": {},
	}
	editable := autoResearchPathSet(spec.EditableFiles)
	commands := make([][]string, 0, len(spec.GuardCommands)+1)
	commands = append(commands, spec.EvalCommand)
	commands = append(commands, spec.GuardCommands...)
	seen := map[string]struct{}{}
	files := make([]map[string]string, 0, len(commands))
	totalBytes := 0

	for _, command := range commands {
		for _, argument := range command[1:] {
			if strings.HasPrefix(argument, "-") || strings.Contains(argument, "=") {
				continue
			}
			relative, path, err := autoResearchExistingFile(workspacePath, argument, autoResearchMaxReadOnlyFileBytes, true)
			if err != nil {
				continue
			}
			if _, mutable := editable[relative]; mutable {
				continue
			}
			if _, duplicate := seen[relative]; duplicate {
				continue
			}
			if _, allowed := allowedExtensions[strings.ToLower(filepath.Ext(relative))]; !allowed {
				continue
			}
			content, err := os.ReadFile(path)
			if err != nil || len(content) == 0 || !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
				continue
			}
			entryBytes := len(relative) + len(content)
			if totalBytes+entryBytes > autoResearchMaxReadOnlyBytes {
				continue
			}
			seen[relative] = struct{}{}
			totalBytes += entryBytes
			files = append(files, map[string]string{
				"path": filepath.ToSlash(relative), "content": string(content), "access": "read_only",
			})
		}
	}
	if len(files) == 0 {
		return "[]"
	}
	encoded, _ := json.Marshal(files)
	return string(encoded)
}

func latestAutoResearchEvaluatorEvidence(trials []models.ResearchTrial) map[string]any {
	for index := len(trials) - 1; index >= 0; index-- {
		trial := trials[index]
		if strings.TrimSpace(trial.EvalResult.StdoutPreview) == "" && strings.TrimSpace(trial.EvalResult.StderrPreview) == "" {
			continue
		}
		return map[string]any{
			"trial": trial.Number, "status": trial.Status, "metric": trial.Metric,
			"stdout": truncateToRunes(trial.EvalResult.StdoutPreview, 2000),
			"stderr": truncateToRunes(trial.EvalResult.StderrPreview, 2000),
		}
	}
	return map[string]any{}
}

func autoResearchVisibleFailures(trials []models.ResearchTrial) []string {
	for index := len(trials) - 1; index >= 0; index-- {
		payload, ok := autoResearchFinalJSONObject(trials[index].EvalResult.StdoutPreview)
		if !ok {
			continue
		}
		rawCases, ok := payload["cases"].([]any)
		if !ok {
			return nil
		}
		failures := make([]string, 0, len(rawCases))
		for caseIndex, rawCase := range rawCases {
			entry, ok := rawCase.(map[string]any)
			if !ok {
				continue
			}
			passed, ok := entry["passed"].(bool)
			if !ok || passed {
				continue
			}
			name, _ := entry["name"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				name = fmt.Sprintf("case_%d", caseIndex+1)
			}
			failures = append(failures, name)
		}
		return failures
	}
	return nil
}

func autoResearchFinalJSONObject(stdout string) (map[string]any, bool) {
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		var payload map[string]any
		if err := decoder.Decode(&payload); err == nil {
			return payload, true
		}
	}
	return nil, false
}

func recentAutoResearchTrials(trials []models.ResearchTrial, maximum int) []models.ResearchTrial {
	if len(trials) <= maximum {
		return append([]models.ResearchTrial(nil), trials...)
	}
	return append([]models.ResearchTrial(nil), trials[len(trials)-maximum:]...)
}

func autoResearchImproved(candidate, best float64, direction string, minDelta float64) bool {
	return autoResearchDelta(candidate, best, direction)+1e-12 >= minDelta && autoResearchDelta(candidate, best, direction) > 0
}

func autoResearchDelta(candidate, best float64, direction string) float64 {
	if direction == "minimize" {
		return best - candidate
	}
	return candidate - best
}
