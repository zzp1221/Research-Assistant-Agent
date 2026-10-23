package scheduler

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/template"
	"time"

	"research-assistant-backend/internal/catalog"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/workspace"
)

type repoPrepareManifest struct {
	RepoURL                string                    `json:"repo_url"`
	RequestedRevision      string                    `json:"requested_revision,omitempty"`
	RepositoryCommit       string                    `json:"repository_commit,omitempty"`
	AcquisitionMethod      string                    `json:"acquisition_method,omitempty"`
	WorkspacePath          string                    `json:"workspace_path"`
	SelectedCodeFile       string                    `json:"selected_code_file,omitempty"`
	DependencyFiles        []string                  `json:"dependency_files,omitempty"`
	CodeFileCandidates     []string                  `json:"code_file_candidates,omitempty"`
	CloneAttempts          []string                  `json:"clone_attempts,omitempty"`
	ReproEntryKind         string                    `json:"repro_entry_kind,omitempty"`
	ReproductionMode       string                    `json:"reproduction_mode"`
	FullReproductionSwitch bool                      `json:"full_reproduction_switch"`
	ModeDecision           ReproductionModeDecision  `json:"mode_decision"`
	HardwareProbe          ReproductionResourceProbe `json:"hardware_probe"`
	UploadedFiles          []string                  `json:"uploaded_files,omitempty"`
}

const (
	reproductionSmokeRunnerName    = "research-assistant_repro_smoke.py"
	repositorySourceMarkerName     = ".research-assistant-repository-source.json"
	maxRepositoryArchiveBytes      = 256 << 20
	maxRepositoryExtractedBytes    = 512 << 20
	maxRepositoryFileBytes         = 128 << 20
	maxRepositoryArchiveFileCount  = 50000
	maxRepositoryRevisionSpecBytes = 1 << 20
)

type repositorySourceMarker struct {
	RepoURL     string `json:"repo_url"`
	Commit      string `json:"commit,omitempty"`
	Acquisition string `json:"acquisition"`
}

func executeRepoPrepare(ctx context.Context, runtimeTask *models.Task) error {
	if runtimeTask == nil {
		return fmt.Errorf("runtime task is nil")
	}

	repoURL := strings.TrimSpace(taskInputValue(runtimeTask, "repo_url"))
	if repoURL == "" {
		return fmt.Errorf("repo_prepare: missing repo_url")
	}
	repositoryRevision, err := repoPrepareRepositoryRevision(runtimeTask)
	if err != nil {
		return err
	}

	candidateURLs := repoPrepareCandidateURLs(runtimeTask, repoURL)
	repoURL, workspacePath, cloneAttempts, err := cloneFirstAvailableRepository(ctx, candidateURLs, repositoryRevision)
	if err != nil {
		return err
	}

	dependencyFiles, codeCandidates, scanErr := scanRepositoryWorkspace(workspacePath)
	if scanErr != nil {
		return scanErr
	}
	uploadedFiles, uploadErr := materializeUploadedFiles(workspacePath, runtimeTask)
	if uploadErr != nil {
		return uploadErr
	}

	selectedCodeFile := choosePreferredCodeFile(codeCandidates)
	repositorySource, _ := readRepositorySourceMarker(workspacePath)
	if repositoryRevision != "" && !strings.EqualFold(repositorySource.Commit, repositoryRevision) {
		return fmt.Errorf("prepared repository commit %q does not match requested revision %q", repositorySource.Commit, repositoryRevision)
	}
	reproEntryKind := ""
	modeDecision := decideReproductionMode(runtimeTask, workspacePath)
	if taskBoolInput(runtimeTask, "skip_reproduction_smoke_runner") {
		reproEntryKind = "repository_workspace"
	} else if modeDecision.EffectiveMode == reproductionModeFull {
		reproEntryKind = "repository_full_experiment"
	} else {
		if smokeFile, smokeKind, createErr := maybeCreateReproductionSmokeRunner(workspacePath, runtimeTask); createErr != nil {
			return createErr
		} else if smokeFile != "" {
			selectedCodeFile = smokeFile
			codeCandidates = append([]string{smokeFile}, codeCandidates...)
			reproEntryKind = smokeKind
		}
	}

	generatedCode := ""
	if selectedCodeFile != "" {
		raw, readErr := os.ReadFile(selectedCodeFile)
		if readErr != nil {
			return fmt.Errorf("read selected repo code file failed: %w", readErr)
		}
		generatedCode = string(raw)
	}

	manifest := repoPrepareManifest{
		RepoURL:                repoURL,
		RequestedRevision:      repositoryRevision,
		RepositoryCommit:       repositorySource.Commit,
		AcquisitionMethod:      repositorySource.Acquisition,
		WorkspacePath:          workspacePath,
		SelectedCodeFile:       selectedCodeFile,
		DependencyFiles:        toWorkspaceRelativePaths(workspacePath, dependencyFiles),
		CodeFileCandidates:     toWorkspaceRelativePaths(workspacePath, codeCandidates),
		CloneAttempts:          cloneAttempts,
		ReproEntryKind:         reproEntryKind,
		ReproductionMode:       modeDecision.EffectiveMode,
		FullReproductionSwitch: modeDecision.EffectiveMode == reproductionModeFull,
		ModeDecision:           modeDecision,
		HardwareProbe:          modeDecision.Probe,
		UploadedFiles:          uploadedFiles,
	}
	manifestJSON, _ := json.Marshal(manifest)
	modeReport := reproductionModeReport(modeDecision)

	if runtimeTask.Metadata == nil {
		runtimeTask.Metadata = map[string]any{}
	}
	runtimeTask.Metadata["artifact_values"] = map[string]any{
		"workspace_path":           workspacePath,
		"code_file_path":           selectedCodeFile,
		"generated_code":           generatedCode,
		"repo_manifest":            string(manifestJSON),
		"reproduction_mode_report": modeReport,
	}

	runtimeTask.Result = chooseNonEmpty(workspacePath, selectedCodeFile, repoURL)
	runtimeTask.Code = generatedCode
	runtimeTask.Status = models.StatusCompleted
	return nil
}

type uploadedFileInput struct {
	Name        string `json:"name"`
	StoragePath string `json:"storage_path"`
}

func repoPrepareRepositoryRevision(task *models.Task) (string, error) {
	requested := strings.ToLower(strings.TrimSpace(taskInputValue(task, "repository_revision")))
	if requested != "" && !validGitCommit(requested) {
		return "", fmt.Errorf("repository_revision must be a full 40- or 64-character commit SHA")
	}
	if task == nil || task.Inputs == nil || task.Inputs["uploaded_files"] == nil {
		return requested, nil
	}

	raw, err := json.Marshal(task.Inputs["uploaded_files"])
	if err != nil {
		return "", fmt.Errorf("encode uploaded files for repository revision: %w", err)
	}
	var uploaded []uploadedFileInput
	if err := json.Unmarshal(raw, &uploaded); err != nil {
		return "", fmt.Errorf("decode uploaded files for repository revision: %w", err)
	}
	for _, file := range uploaded {
		if !strings.EqualFold(filepath.Ext(strings.TrimSpace(file.Name)), ".json") {
			continue
		}
		path, validateErr := workspace.ValidateUploadedFilePath(file.StoragePath)
		if validateErr != nil {
			continue
		}
		info, statErr := os.Lstat(path)
		if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxRepositoryRevisionSpecBytes {
			continue
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var header struct {
			Version            string `json:"version"`
			RepositoryRevision string `json:"repository_revision"`
		}
		if json.Unmarshal(content, &header) != nil || header.Version != models.AutoResearchSpecVersion {
			continue
		}
		revision := strings.ToLower(strings.TrimSpace(header.RepositoryRevision))
		if revision == "" {
			continue
		}
		if !validGitCommit(revision) {
			return "", fmt.Errorf("uploaded AutoResearch repository_revision must be a full 40- or 64-character commit SHA")
		}
		if requested != "" && !strings.EqualFold(requested, revision) {
			return "", fmt.Errorf("repository_revision %q conflicts with uploaded AutoResearch revision %q", requested, revision)
		}
		requested = revision
	}
	return requested, nil
}

func materializeUploadedFiles(workspacePath string, task *models.Task) ([]string, error) {
	if task == nil || task.Inputs == nil || task.Inputs["uploaded_files"] == nil {
		return nil, nil
	}
	raw, err := json.Marshal(task.Inputs["uploaded_files"])
	if err != nil {
		return nil, fmt.Errorf("encode uploaded files: %w", err)
	}
	var uploaded []uploadedFileInput
	if err := json.Unmarshal(raw, &uploaded); err != nil {
		return nil, fmt.Errorf("decode uploaded files: %w", err)
	}
	if len(uploaded) == 0 {
		return nil, nil
	}
	targetDirectory, err := ensureWorkspaceUploadDirectory(workspacePath)
	if err != nil {
		return nil, err
	}
	materialized := make([]string, 0, len(uploaded))
	for index, file := range uploaded {
		sourcePath, err := workspace.ValidateUploadedFilePath(file.StoragePath)
		if err != nil {
			return nil, fmt.Errorf("uploaded file %q is outside the allowed upload roots: %w", file.Name, err)
		}
		info, err := os.Stat(sourcePath)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("uploaded file %q is unavailable", file.Name)
		}
		name := filepath.Base(strings.TrimSpace(file.Name))
		if name == "" || name == "." {
			name = fmt.Sprintf("attachment-%d", index+1)
		}
		targetPath := filepath.Join(targetDirectory, fmt.Sprintf("%02d-%s", index+1, name))
		if err := copyRegularFile(sourcePath, targetPath); err != nil {
			return nil, err
		}
		relative, _ := filepath.Rel(workspacePath, targetPath)
		materialized = append(materialized, filepath.ToSlash(relative))
	}
	return materialized, nil
}

func ensureWorkspaceUploadDirectory(workspacePath string) (string, error) {
	current := filepath.Clean(workspacePath)
	info, err := os.Lstat(current)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("workspace is not a regular directory")
	}
	for _, component := range []string{".research-assistant", "uploads"} {
		current = filepath.Join(current, component)
		info, err = os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return "", fmt.Errorf("create uploaded file workspace: %w", err)
			}
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect uploaded file workspace: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("uploaded file workspace contains an unsafe path component")
		}
	}
	return current, nil
}

func copyRegularFile(sourcePath, targetPath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open uploaded file: %w", err)
	}
	defer source.Close()
	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create workspace attachment: %w", err)
	}
	defer target.Close()
	if _, err := io.Copy(target, source); err != nil {
		return fmt.Errorf("copy workspace attachment: %w", err)
	}
	return nil
}

func reproductionModeReport(decision ReproductionModeDecision) string {
	raw, _ := json.MarshalIndent(decision, "", "  ")
	return string(raw)
}

func maybeCreateReproductionSmokeRunner(workspacePath string, task *models.Task) (string, string, error) {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" {
		return "", "", nil
	}
	paperTitle := smokePaperTitle(task)
	for _, recipe := range catalog.Recipes() {
		if !smokeRecipeMatches(workspacePath, task, recipe) {
			continue
		}
		raw, err := catalog.Template(recipe)
		if err != nil {
			return "", "", err
		}
		rendered, err := renderSmokeTemplate(recipe, raw, paperTitle, task)
		if err != nil {
			return "", "", err
		}
		runnerPath := filepath.Join(workspacePath, reproductionSmokeRunnerName)
		if err := os.WriteFile(runnerPath, []byte(rendered), 0o644); err != nil {
			return "", "", fmt.Errorf("create smoke runner %q: %w", recipe.ID, err)
		}
		return runnerPath, recipe.Kind, nil
	}
	return "", "", nil
}

// smokePaperTitle prefers the structured paper title supplied by the planner so
// generated runners never hardcode a paper name.
func smokePaperTitle(task *models.Task) string {
	if title := strings.TrimSpace(taskInputValue(task, "paper_title")); title != "" {
		return title
	}
	if task != nil {
		if title := extractTitleHeuristic(task.Description); title != "" {
			return title
		}
	}
	return "paper"
}

func smokeRecipeMatches(workspacePath string, task *models.Task, recipe catalog.SmokeRecipe) bool {
	for _, relative := range recipe.RequireFiles {
		info, err := os.Stat(filepath.Join(workspacePath, filepath.FromSlash(relative)))
		if err != nil || info.IsDir() {
			return false
		}
	}
	// A planner-provided ablation plan is an explicit request for the ablation
	// recipe even when the task description itself is terse.
	if recipe.Variants != nil && strings.TrimSpace(taskInputValue(task, "ablation_plan")) != "" {
		return true
	}
	if len(recipe.TaskGroups) > 0 && !matchesAllGroups(smokeTaskText(task), recipe.TaskGroups) {
		return false
	}
	if len(recipe.TextGroups) > 0 && !matchesAllGroups(smokeWorkspaceText(workspacePath, task), recipe.TextGroups) {
		return false
	}
	return true
}

func smokeTaskText(task *models.Task) string {
	return strings.ToLower(strings.Join([]string{
		taskField(task, func(item *models.Task) string { return item.Name }),
		taskField(task, func(item *models.Task) string { return item.Description }),
		taskInputValue(task, "repo_url"),
		taskInputValue(task, "paper_title"),
	}, "\n"))
}

func smokeWorkspaceText(workspacePath string, task *models.Task) string {
	parts := []string{smokeTaskText(task)}
	for _, name := range []string{"README.md", "readme.md", "README.rst"} {
		raw, err := os.ReadFile(filepath.Join(workspacePath, name))
		if err == nil {
			parts = append(parts, string(raw))
		}
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}

// matchesAllGroups treats the outer slice as AND and each inner slice as OR.
func matchesAllGroups(text string, groups [][]string) bool {
	for _, group := range groups {
		matched := false
		for _, term := range group {
			if term != "" && strings.Contains(text, strings.ToLower(term)) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func renderSmokeTemplate(recipe catalog.SmokeRecipe, raw, paperTitle string, task *models.Task) (string, error) {
	titleJSON, err := json.Marshal(paperTitle)
	if err != nil {
		return "", fmt.Errorf("encode paper title: %w", err)
	}
	data := map[string]any{"PaperTitleJSON": string(titleJSON)}
	if recipe.Variants != nil {
		variants := selectedSmokeVariants(task, recipe.Variants)
		encoded, err := json.Marshal(variants)
		if err != nil {
			return "", fmt.Errorf("encode smoke variants: %w", err)
		}
		data["VariantsJSON"] = string(encoded)
	}
	tmpl, err := template.New(recipe.ID).Option("missingkey=error").Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse smoke template %q: %w", recipe.ID, err)
	}
	var buffer strings.Builder
	if err := tmpl.Execute(&buffer, data); err != nil {
		return "", fmt.Errorf("render smoke template %q: %w", recipe.ID, err)
	}
	return buffer.String(), nil
}

func selectedSmokeVariants(task *models.Task, set *catalog.VariantSet) []catalog.Variant {
	variants := []catalog.Variant{set.Baseline}
	categories := map[string]bool{}
	if raw := strings.TrimSpace(taskInputValue(task, "ablation_plan")); raw != "" {
		var plan models.AblationPlan
		if err := json.Unmarshal([]byte(raw), &plan); err == nil {
			for _, candidate := range plan.Selected {
				categories[candidate.Category] = true
			}
		}
	}
	if len(categories) == 0 {
		for _, category := range set.DefaultCategories {
			categories[category] = true
		}
	}
	for _, category := range []string{"parameter", "module", "data_scale", "seed_stability", "runtime_cost"} {
		if categories[category] {
			variants = append(variants, set.Categories[category]...)
		}
	}
	return variants
}

func taskField(task *models.Task, getter func(*models.Task) string) string {
	if task == nil {
		return ""
	}
	return getter(task)
}

func repoPrepareCandidateURLs(task *models.Task, primary string) []string {
	urls := make([]string, 0, 6)
	appendURL := func(value string) {
		value = normalizeGitHubRepoURL(value)
		if value == "" {
			return
		}
		urls = append(urls, value)
	}

	appendURL(primary)

	// For well-known papers, keep a curated implementation near the front.
	// This gives paper reproduction a trustworthy fallback when GitHub Search
	// returns a stale or oversized repository.
	if task != nil {
		for _, candidate := range curatedRepoFallbackCandidates(task.Description) {
			for _, repoURL := range candidate.RepoURLs {
				appendURL(repoURL)
			}
		}
	}

	if raw := strings.TrimSpace(taskInputValue(task, "candidate_repositories")); raw != "" {
		var candidates []repoCandidate
		if err := json.Unmarshal([]byte(raw), &candidates); err == nil {
			for _, candidate := range candidates {
				for _, repoURL := range candidate.RepoURLs {
					appendURL(repoURL)
				}
			}
		}
	}

	return uniqueNonEmptyStrings(urls)
}

func normalizeGitHubRepoURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimRight(value, "/")
	return value
}

func cloneFirstAvailableRepository(ctx context.Context, candidateURLs []string, revision string) (string, string, []string, error) {
	candidateURLs = uniqueNonEmptyStrings(candidateURLs)
	if len(candidateURLs) == 0 {
		return "", "", nil, fmt.Errorf("repo_prepare: missing clone candidates")
	}
	if len(candidateURLs) > 5 {
		candidateURLs = candidateURLs[:5]
	}

	attempts := make([]string, 0, len(candidateURLs)*2)
	if cachedURL, cachedWorkspace := findCachedRepositoryWorkspace(candidateURLs, revision); cachedWorkspace != "" {
		freshWorkspace, err := cloneCachedGitRepository(ctx, cachedURL, cachedWorkspace, revision)
		if err == nil {
			attempts = append(attempts, fmt.Sprintf("%s: immutable Git cache clone from %s", cachedURL, cachedWorkspace))
			return cachedURL, freshWorkspace, attempts, nil
		}
		attempts = append(attempts, fmt.Sprintf("%s: Git cache clone failed: %v", cachedURL, err))
	}

	for _, repoURL := range candidateURLs {
		workspacePath, err := os.MkdirTemp("", "research-assistant_repo_workspace_")
		if err != nil {
			return "", "", attempts, fmt.Errorf("create repo workspace: %w", err)
		}

		if err := cloneRepositoryWithRetry(ctx, repoURL, workspacePath, revision, &attempts); err != nil {
			_ = os.RemoveAll(workspacePath)
			continue
		}
		return repoURL, workspacePath, attempts, nil
	}

	return "", "", attempts, fmt.Errorf("clone repo failed after %d candidate(s): %s", len(candidateURLs), strings.Join(attempts, " | "))
}

func findCachedRepositoryWorkspace(candidateURLs []string, revision string) (string, string) {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return "", ""
	}

	for _, repoURL := range candidateURLs {
		normalizedURL := normalizeGitHubRepoURL(repoURL)
		if normalizedURL == "" {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "research-assistant_repo_workspace_") {
				continue
			}
			workspacePath := filepath.Join(os.TempDir(), entry.Name())
			if info, err := os.Stat(filepath.Join(workspacePath, ".git")); err != nil || !info.IsDir() {
				continue
			}
			if workspaceMatchesRepoURL(workspacePath, normalizedURL) {
				if revision != "" {
					source, err := readRepositorySourceMarker(workspacePath)
					if err != nil || !strings.EqualFold(source.Commit, revision) {
						continue
					}
				}
				return normalizedURL, workspacePath
			}
		}
	}
	return "", ""
}

func cloneCachedGitRepository(ctx context.Context, repoURL, cachedWorkspace, revision string) (string, error) {
	workspacePath, err := os.MkdirTemp("", "research-assistant_repo_workspace_")
	if err != nil {
		return "", fmt.Errorf("create cached repo workspace: %w", err)
	}
	cloneCtx, cancel := context.WithTimeout(ctx, envDuration("REPO_LOCAL_CLONE_TIMEOUT", 45*time.Second))
	defer cancel()
	cmd := exec.CommandContext(cloneCtx, "git", "clone", "--local", "--no-hardlinks", cachedWorkspace, workspacePath)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		_ = os.RemoveAll(workspacePath)
		return "", fmt.Errorf("clone cached Git commit: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	commit := repositoryCommit(ctx, workspacePath)
	if revision != "" && !strings.EqualFold(commit, revision) {
		_ = os.RemoveAll(workspacePath)
		return "", fmt.Errorf("cached repository commit %q does not match requested revision %q", commit, revision)
	}
	if err := writeRepositorySourceMarker(workspacePath, repositorySourceMarker{
		RepoURL: repoURL, Commit: commit, Acquisition: "git_local_cache_clone",
	}); err != nil {
		_ = os.RemoveAll(workspacePath)
		return "", err
	}
	return workspacePath, nil
}

func workspaceMatchesRepoURL(workspacePath string, repoURL string) bool {
	if source, err := readRepositorySourceMarker(workspacePath); err == nil {
		return normalizeGitHubRepoURL(source.RepoURL) == normalizeGitHubRepoURL(repoURL)
	}
	raw, err := os.ReadFile(filepath.Join(workspacePath, ".git", "config"))
	if err != nil {
		return false
	}
	config := strings.ToLower(string(raw))
	repoURL = strings.ToLower(normalizeGitHubRepoURL(repoURL))
	return repoURL != "" && (strings.Contains(config, repoURL) || strings.Contains(config, repoURL+".git"))
}

func cloneRepositoryWithRetry(ctx context.Context, repoURL, workspacePath, revision string, attempts *[]string) error {
	if revision != "" {
		return cloneRepositoryRevisionWithRetry(ctx, repoURL, workspacePath, revision, attempts)
	}
	cloneCommands := [][]string{
		{"clone", "--depth", "1", "--filter=blob:none", "--single-branch", repoURL, workspacePath},
		{"clone", "--depth", "1", repoURL, workspacePath},
	}

	var lastErr error
	for idx, args := range cloneCommands {
		if idx > 0 {
			_ = os.RemoveAll(workspacePath)
			if err := os.MkdirAll(workspacePath, 0o700); err != nil {
				return fmt.Errorf("recreate repo workspace: %w", err)
			}
		}

		cloneCtx, cancel := context.WithTimeout(ctx, envDuration("REPO_CLONE_TIMEOUT", 45*time.Second))
		cmd := exec.CommandContext(cloneCtx, "git", args...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		cmd.WaitDelay = 5 * time.Second
		output, err := cmd.CombinedOutput()
		cancel()
		if err == nil {
			commit := repositoryCommit(ctx, workspacePath)
			if markerErr := writeRepositorySourceMarker(workspacePath, repositorySourceMarker{
				RepoURL: repoURL, Commit: commit, Acquisition: "git_shallow_clone",
			}); markerErr != nil {
				return markerErr
			}
			*attempts = append(*attempts, fmt.Sprintf("%s: git shallow clone ok", repoURL))
			return nil
		}

		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		} else {
			msg = fmt.Sprintf("%v (%s)", err, msg)
		}
		*attempts = append(*attempts, fmt.Sprintf("%s: %s", repoURL, msg))
		lastErr = fmt.Errorf("%s", msg)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := os.RemoveAll(workspacePath); err != nil {
		return fmt.Errorf("reset repository workspace for archive fallback: %w", err)
	}
	if err := os.MkdirAll(workspacePath, 0o700); err != nil {
		return fmt.Errorf("recreate repository workspace for archive fallback: %w", err)
	}
	if err := downloadGitHubRepositoryArchive(ctx, repoURL, workspacePath, ""); err == nil {
		*attempts = append(*attempts, fmt.Sprintf("%s: GitHub archive fallback ok", repoURL))
		return nil
	} else {
		*attempts = append(*attempts, fmt.Sprintf("%s: GitHub archive fallback failed: %v", repoURL, err))
		lastErr = err
	}
	return lastErr
}

func cloneRepositoryRevisionWithRetry(ctx context.Context, repoURL, workspacePath, revision string, attempts *[]string) error {
	if !validGitCommit(revision) {
		return fmt.Errorf("repository revision must be a full commit SHA")
	}
	fetchOptions := [][]string{
		{"--depth", "1", "--filter=blob:none", "origin", revision},
		{"--depth", "1", "origin", revision},
	}
	var lastErr error
	for index, options := range fetchOptions {
		if index > 0 {
			if err := resetRepositoryWorkspace(workspacePath); err != nil {
				return err
			}
		}
		cloneCtx, cancel := context.WithTimeout(ctx, envDuration("REPO_CLONE_TIMEOUT", 45*time.Second))
		commands := [][]string{
			{"init", workspacePath},
			{"-C", workspacePath, "remote", "add", "origin", repoURL},
			append([]string{"-C", workspacePath, "fetch"}, options...),
			{"-C", workspacePath, "checkout", "--detach", "FETCH_HEAD"},
		}
		var output []string
		var commandErr error
		for _, arguments := range commands {
			result, err := runRepositoryGitCommand(cloneCtx, arguments...)
			if text := strings.TrimSpace(string(result)); text != "" {
				output = append(output, text)
			}
			if err != nil {
				commandErr = err
				break
			}
		}
		cancel()
		if commandErr == nil {
			commit := repositoryCommit(ctx, workspacePath)
			if !strings.EqualFold(commit, revision) {
				commandErr = fmt.Errorf("checked out commit %q, want %q", commit, revision)
			} else if markerErr := writeRepositorySourceMarker(workspacePath, repositorySourceMarker{
				RepoURL: repoURL, Commit: commit, Acquisition: "git_pinned_fetch",
			}); markerErr != nil {
				return markerErr
			} else {
				*attempts = append(*attempts, fmt.Sprintf("%s: pinned Git fetch %s ok", repoURL, revision))
				return nil
			}
		}

		message := commandErr.Error()
		if len(output) > 0 {
			message = fmt.Sprintf("%v (%s)", commandErr, strings.Join(output, " | "))
		}
		*attempts = append(*attempts, fmt.Sprintf("%s: pinned Git fetch %s failed: %s", repoURL, revision, message))
		lastErr = commandErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err := resetRepositoryWorkspace(workspacePath); err != nil {
		return err
	}
	if err := downloadGitHubRepositoryArchive(ctx, repoURL, workspacePath, revision); err == nil {
		*attempts = append(*attempts, fmt.Sprintf("%s: pinned GitHub archive %s ok", repoURL, revision))
		return nil
	} else {
		*attempts = append(*attempts, fmt.Sprintf("%s: pinned GitHub archive %s failed: %v", repoURL, revision, err))
		lastErr = err
	}
	return lastErr
}

func runRepositoryGitCommand(ctx context.Context, arguments ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", arguments...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd.CombinedOutput()
}

func resetRepositoryWorkspace(workspacePath string) error {
	if err := os.RemoveAll(workspacePath); err != nil {
		return fmt.Errorf("reset repository workspace: %w", err)
	}
	if err := os.MkdirAll(workspacePath, 0o700); err != nil {
		return fmt.Errorf("recreate repository workspace: %w", err)
	}
	return nil
}

func repositoryCommit(ctx context.Context, workspacePath string) string {
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", "-C", workspacePath, "rev-parse", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func readRepositorySourceMarker(workspacePath string) (repositorySourceMarker, error) {
	var marker repositorySourceMarker
	raw, err := os.ReadFile(filepath.Join(workspacePath, repositorySourceMarkerName))
	if err != nil {
		return marker, err
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		return marker, err
	}
	if normalizeGitHubRepoURL(marker.RepoURL) == "" {
		return marker, fmt.Errorf("repository source marker is missing repo_url")
	}
	return marker, nil
}

func writeRepositorySourceMarker(workspacePath string, marker repositorySourceMarker) error {
	marker.RepoURL = normalizeGitHubRepoURL(marker.RepoURL)
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode repository source marker: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workspacePath, repositorySourceMarkerName), raw, 0o600); err != nil {
		return fmt.Errorf("write repository source marker: %w", err)
	}
	return nil
}

type githubRepository struct {
	Owner string
	Name  string
}

func parseGitHubRepository(repoURL string) (githubRepository, error) {
	parsed, err := url.Parse(normalizeGitHubRepoURL(repoURL))
	if err != nil || !strings.EqualFold(parsed.Hostname(), "github.com") {
		return githubRepository{}, fmt.Errorf("archive fallback only supports github.com repositories")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Port() != "" {
		return githubRepository{}, fmt.Errorf("GitHub repository URL contains unsupported components")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return githubRepository{}, fmt.Errorf("unsupported GitHub URL scheme")
	}
	parts := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(parts) != 2 {
		return githubRepository{}, fmt.Errorf("GitHub URL must identify one repository")
	}
	owner, ownerErr := url.PathUnescape(parts[0])
	name, nameErr := url.PathUnescape(strings.TrimSuffix(parts[1], ".git"))
	if ownerErr != nil || nameErr != nil || !validGitHubPathPart(owner) || !validGitHubPathPart(name) {
		return githubRepository{}, fmt.Errorf("GitHub repository path is invalid")
	}
	return githubRepository{Owner: owner, Name: name}, nil
}

func validGitHubPathPart(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._-", character) {
			continue
		}
		return false
	}
	return true
}

func resolveRepositoryHEAD(ctx context.Context, repoURL string) (string, error) {
	resolveCtx, cancel := context.WithTimeout(ctx, envDuration("REPO_ARCHIVE_RESOLVE_TIMEOUT", 20*time.Second))
	defer cancel()
	cmd := exec.CommandContext(resolveCtx, "git", "ls-remote", repoURL, "HEAD")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("resolve repository HEAD: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	fields := strings.Fields(string(output))
	if len(fields) < 2 || fields[1] != "HEAD" || !validGitCommit(fields[0]) {
		return "", fmt.Errorf("repository HEAD response is invalid")
	}
	return fields[0], nil
}

func validGitCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func downloadGitHubRepositoryArchive(ctx context.Context, repoURL, workspacePath, revision string) error {
	repository, err := parseGitHubRepository(repoURL)
	if err != nil {
		return err
	}
	commit := strings.ToLower(strings.TrimSpace(revision))
	if commit == "" {
		commit, err = resolveRepositoryHEAD(ctx, repoURL)
		if err != nil {
			return err
		}
	} else if !validGitCommit(commit) {
		return fmt.Errorf("repository archive revision must be a full commit SHA")
	}
	archiveURL := fmt.Sprintf("https://codeload.github.com/%s/%s/tar.gz/%s", repository.Owner, repository.Name, commit)
	downloadCtx, cancel := context.WithTimeout(ctx, envDuration("REPO_ARCHIVE_TIMEOUT", 3*time.Minute))
	defer cancel()
	request, err := http.NewRequestWithContext(downloadCtx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return fmt.Errorf("create repository archive request: %w", err)
	}
	request.Header.Set("User-Agent", "ResearchAssistantAgent/1.0")
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return fmt.Errorf("download repository archive: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download repository archive: unexpected HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxRepositoryArchiveBytes {
		return fmt.Errorf("repository archive exceeds %d bytes", maxRepositoryArchiveBytes)
	}
	limited := &io.LimitedReader{R: response.Body, N: maxRepositoryArchiveBytes + 1}
	if err := extractRepositoryArchive(limited, workspacePath); err != nil {
		return err
	}
	if limited.N <= 0 {
		return fmt.Errorf("repository archive exceeds %d bytes", maxRepositoryArchiveBytes)
	}
	return writeRepositorySourceMarker(workspacePath, repositorySourceMarker{
		RepoURL: repoURL, Commit: commit, Acquisition: "github_codeload_archive",
	})
}

func extractRepositoryArchive(compressed io.Reader, workspacePath string) error {
	gzipReader, err := gzip.NewReader(compressed)
	if err != nil {
		return fmt.Errorf("open repository archive: %w", err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	archiveRoot := ""
	fileCount := 0
	var extractedBytes int64
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read repository archive: %w", err)
		}
		if repositoryArchiveMetadataEntry(header.Typeflag) {
			continue
		}
		relativePath, root, err := repositoryArchivePath(header.Name, archiveRoot)
		if err != nil {
			return err
		}
		if archiveRoot == "" {
			archiveRoot = root
		}
		if relativePath == "" {
			continue
		}
		targetPath := filepath.Join(workspacePath, filepath.FromSlash(relativePath))
		if !pathWithinWorkspace(workspacePath, targetPath) {
			return fmt.Errorf("repository archive path escapes workspace: %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, 0o700); err != nil {
				return fmt.Errorf("create repository archive directory: %w", err)
			}
		case tar.TypeSymlink:
			fileCount++
			if fileCount > maxRepositoryArchiveFileCount {
				return fmt.Errorf("repository archive contains too many files")
			}
			if !safeRepositorySymlinkTarget(relativePath, header.Linkname) {
				return fmt.Errorf("repository archive contains an unsafe symlink %q -> %q", header.Name, header.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
				return fmt.Errorf("create repository symlink parent: %w", err)
			}
			if err := os.Symlink(filepath.FromSlash(header.Linkname), targetPath); err != nil {
				return fmt.Errorf("create repository archive symlink: %w", err)
			}
		case tar.TypeReg, tar.TypeRegA:
			fileCount++
			if fileCount > maxRepositoryArchiveFileCount {
				return fmt.Errorf("repository archive contains too many files")
			}
			if header.Size < 0 || header.Size > maxRepositoryFileBytes || extractedBytes+header.Size > maxRepositoryExtractedBytes {
				return fmt.Errorf("repository archive exceeds extraction limits")
			}
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
				return fmt.Errorf("create repository archive parent: %w", err)
			}
			mode := os.FileMode(0o644)
			if header.FileInfo().Mode()&0o111 != 0 {
				mode = 0o755
			}
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return fmt.Errorf("create repository archive file: %w", err)
			}
			written, copyErr := io.CopyN(file, tarReader, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return fmt.Errorf("extract repository archive file %q: %w", relativePath, copyErr)
			}
			if written != header.Size {
				return fmt.Errorf("extract repository archive file %q: wrote %d of %d bytes", relativePath, written, header.Size)
			}
			if closeErr != nil {
				return fmt.Errorf("close repository archive file: %w", closeErr)
			}
			extractedBytes += written
		default:
			return fmt.Errorf("repository archive contains unsupported entry type %d at %q", header.Typeflag, header.Name)
		}
	}
	if fileCount == 0 {
		return fmt.Errorf("repository archive contains no regular files")
	}
	return nil
}

func repositoryArchiveMetadataEntry(typeFlag byte) bool {
	switch typeFlag {
	case tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNULongName, tar.TypeGNULongLink:
		return true
	default:
		return false
	}
}

func repositoryArchivePath(name, expectedRoot string) (string, string, error) {
	if strings.Contains(name, "\\") || strings.HasPrefix(name, "/") {
		return "", "", fmt.Errorf("repository archive contains an unsafe path %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", "", fmt.Errorf("repository archive contains an unsafe path %q", name)
	}
	parts := strings.Split(cleaned, "/")
	root := parts[0]
	if expectedRoot != "" && root != expectedRoot {
		return "", "", fmt.Errorf("repository archive contains multiple roots")
	}
	if len(parts) == 1 {
		return "", root, nil
	}
	relativePath := path.Clean(strings.Join(parts[1:], "/"))
	if relativePath == "." || relativePath == ".." || strings.HasPrefix(relativePath, "../") {
		return "", "", fmt.Errorf("repository archive contains an unsafe path %q", name)
	}
	return relativePath, root, nil
}

func pathWithinWorkspace(workspacePath, targetPath string) bool {
	relative, err := filepath.Rel(filepath.Clean(workspacePath), filepath.Clean(targetPath))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func safeRepositorySymlinkTarget(linkPath, linkTarget string) bool {
	if linkTarget == "" || strings.Contains(linkTarget, "\\") || strings.HasPrefix(linkTarget, "/") {
		return false
	}
	resolved := path.Clean(path.Join(path.Dir(linkPath), linkTarget))
	return resolved != ".." && !strings.HasPrefix(resolved, "../")
}

func scanRepositoryWorkspace(workspacePath string) ([]string, []string, error) {
	dependencyFiles := make([]string, 0, 8)
	codeCandidates := make([]string, 0, 16)

	err := filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".github", "__pycache__", "node_modules", ".venv", "venv", "dist", "build", "docs":
				return filepath.SkipDir
			}
			return nil
		}

		lowerName := strings.ToLower(name)
		switch lowerName {
		case "requirements.txt", "environment.yml", "environment.yaml", "pyproject.toml", "setup.py", "setup.cfg", "pipfile":
			dependencyFiles = append(dependencyFiles, path)
		}

		if strings.HasSuffix(lowerName, ".py") {
			codeCandidates = append(codeCandidates, path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan repository workspace: %w", err)
	}

	sort.SliceStable(codeCandidates, func(i, j int) bool {
		return codeFileScore(codeCandidates[i]) > codeFileScore(codeCandidates[j])
	})
	return dependencyFiles, codeCandidates, nil
}

func choosePreferredCodeFile(candidates []string) string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

func codeFileScore(path string) int {
	score := 0
	base := strings.ToLower(filepath.Base(path))
	full := strings.ToLower(path)

	switch base {
	case "the_annotated_transformer.py":
		score += 100
	case "main.py":
		score += 50
	case "train.py":
		score += 40
	case "run.py":
		score += 30
	}

	if strings.Contains(full, "annotated") && strings.Contains(full, "transformer") {
		score += 60
	}
	if strings.Contains(full, "attention") && strings.Contains(full, "transformer") {
		score += 30
	}
	if strings.Contains(full, "test") || strings.Contains(full, "example") || strings.Contains(full, "demo") {
		score -= 20
	}
	if strings.Contains(full, "tutorial") || strings.Contains(full, "notebook") {
		score -= 10
	}
	return score
}

func toWorkspaceRelativePaths(workspacePath string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		rel, err := filepath.Rel(workspacePath, value)
		if err != nil {
			out = append(out, value)
			continue
		}
		out = append(out, rel)
	}
	return out
}

func taskInputValue(task *models.Task, key string) string {
	if task == nil || task.Inputs == nil {
		return ""
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
