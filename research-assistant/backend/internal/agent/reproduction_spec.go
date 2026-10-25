package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"research-assistant-backend/internal/models"
)

type reproductionRepoManifest struct {
	RepoURL            string   `json:"repo_url"`
	RepositoryCommit   string   `json:"repository_commit"`
	WorkspacePath      string   `json:"workspace_path"`
	SelectedCodeFile   string   `json:"selected_code_file"`
	CodeFileCandidates []string `json:"code_file_candidates"`
	DependencyFiles    []string `json:"dependency_files"`
	ReproEntryKind     string   `json:"repro_entry_kind"`
	ReproductionMode   string   `json:"reproduction_mode"`
}

func (a *ResearchCodingAgent) executeReproductionSpec(ctx context.Context, task *models.Task) error {
	_ = ctx
	if task == nil {
		return fmt.Errorf("task is nil")
	}
	var manifest reproductionRepoManifest
	if raw := inputJSONString(task, "repo_manifest"); strings.TrimSpace(raw) != "" {
		_ = json.Unmarshal([]byte(raw), &manifest)
	}
	dependencies := packageSpecsFromDependencySpec(inputJSONString(task, "dependency_spec"))
	if len(dependencies) == 0 {
		dependencies = packageSpecsFromRequirements(manifest.WorkspacePath)
	}
	baseImage := strings.TrimSpace(os.Getenv("SANDBOX_DEFAULT_IMAGE"))
	if baseImage == "" {
		baseImage = "python:3.11-bullseye"
	}
	entry := filepath.Base(strings.TrimSpace(manifest.SelectedCodeFile))
	entryRelative := entry
	if entry == "" || entry == "." {
		entry = "research-assistant_repro_smoke.py"
		entryRelative = entry
	}
	if selected := strings.TrimSpace(manifest.SelectedCodeFile); selected != "" && strings.TrimSpace(manifest.WorkspacePath) != "" {
		relative, _, err := workspaceRelativeEntry(manifest.WorkspacePath, selected)
		if err != nil {
			if filepath.IsAbs(selected) {
				// An absolute entry outside the workspace is a security error, not
				// a legacy basename we can guess.
				return failResearchCodingTask(task, fmt.Errorf("reproduction entry rejected: %w", err))
			}
		} else {
			entryRelative = relative
			entry = filepath.Base(relative)
		}
	}
	runCommand := []string{"python3", filepath.ToSlash(entryRelative)}
	pythonPath := []string{}
	if manifest.ReproEntryKind == "apriori_frequent_itemsets" {
		runCommand = []string{"python3", "apriori.py", "-f", "INTEGRATED-DATASET.csv", "-s", "0.15", "-c", "0.6"}
		entry = "apriori.py"
		entryRelative = "apriori.py"
	} else if dir := filepath.ToSlash(filepath.Dir(entryRelative)); dir != "." && dir != "" {
		// Keep /workspace as the working directory so repository-relative data
		// paths still resolve, and add the entry directory to PYTHONPATH.
		pythonPath = append(pythonPath, "/workspace/"+dir)
	}
	spec := models.ReproductionRuntimeSpec{
		Version:            "reproduction.runtime/v1",
		Language:           "python",
		BaseImage:          baseImage,
		PythonVersion:      "3.11",
		SystemPackages:     []string{},
		PythonDependencies: dependencies,
		RunCommand:         runCommand,
		Entrypoint:         entry,
		EntrypointRelative: entryRelative,
		PythonPath:         pythonPath,
		WorkingDir:         "/workspace",
		NetworkAccess:      false,
		DiskGB:             1,
		MemoryGB:           2,
		TimeoutSec:         600,
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		return failResearchCodingTask(task, err)
	}
	task.Result = string(payload)
	task.Status = models.StatusCompleted
	setResearchCodingArtifacts(task, map[string]string{"reproduction_runtime_spec": string(payload)})
	return nil
}

func inputJSONString(task *models.Task, key string) string {
	if task == nil || task.Inputs == nil {
		return ""
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func packageSpecsFromDependencySpec(raw string) []models.PackageSpec {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var deps []string
	if json.Unmarshal([]byte(raw), &deps) != nil {
		return nil
	}
	out := make([]models.PackageSpec, 0, len(deps))
	for _, dep := range deps {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		if idx := strings.Index(dep, "=="); idx > 0 {
			out = append(out, models.PackageSpec{Name: strings.TrimSpace(dep[:idx]), Version: strings.TrimSpace(dep[idx+2:])})
			continue
		}
		out = append(out, models.PackageSpec{Name: dep})
	}
	return out
}

func packageSpecsFromRequirements(workspacePath string) []models.PackageSpec {
	if strings.TrimSpace(workspacePath) == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(workspacePath, "requirements.txt"))
	if err != nil {
		return nil
	}
	out := make([]models.PackageSpec, 0)
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		name := line
		version := ""
		if idx := strings.Index(line, "=="); idx > 0 {
			name = strings.TrimSpace(line[:idx])
			version = strings.TrimSpace(line[idx+2:])
		}
		switch strings.ToLower(name) {
		case "streamlit", "ruff", "pytest", "pytest-cov":
			continue
		}
		out = append(out, models.PackageSpec{Name: name, Version: version})
	}
	return out
}
