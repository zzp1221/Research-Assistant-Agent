package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"research-assistant-backend/internal/models"
)

func normalizeAutoResearchPaths(workspacePath string, paths []string, maximum, maxBytes int, allowResearchAssistant bool) ([]string, error) {
	if len(paths) > maximum {
		return nil, fmt.Errorf("at most %d files are allowed", maximum)
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, candidate := range paths {
		relative, _, err := autoResearchExistingFile(workspacePath, candidate, maxBytes, allowResearchAssistant)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[relative]; duplicate {
			continue
		}
		seen[relative] = struct{}{}
		out = append(out, relative)
	}
	sort.Strings(out)
	return out, nil
}

func autoResearchExistingFile(workspacePath, candidate string, maxBytes int, allowResearchAssistant bool) (string, string, error) {
	workspacePath = filepath.Clean(workspacePath)
	candidate = strings.TrimSpace(candidate)
	if strings.HasPrefix(filepath.ToSlash(candidate), "/workspace/") {
		candidate = strings.TrimPrefix(filepath.ToSlash(candidate), "/workspace/")
	}
	if candidate == "" {
		return "", "", fmt.Errorf("empty AutoResearch file path")
	}
	var relative string
	if filepath.IsAbs(candidate) {
		var err error
		relative, err = filepath.Rel(workspacePath, filepath.Clean(candidate))
		if err != nil {
			return "", "", err
		}
	} else {
		relative = filepath.Clean(filepath.FromSlash(candidate))
	}
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("AutoResearch path escapes workspace: %s", candidate)
	}
	relativeSlash := filepath.ToSlash(relative)
	if strings.HasPrefix(relativeSlash, ".git/") || (!allowResearchAssistant && strings.HasPrefix(relativeSlash, ".research-assistant/")) {
		return "", "", fmt.Errorf("AutoResearch path is outside the allowed scope: %s", relativeSlash)
	}
	current := workspacePath
	parts := strings.Split(relativeSlash, "/")
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", "", fmt.Errorf("AutoResearch file is unavailable: %s", relativeSlash)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("AutoResearch path contains a symbolic link: %s", relativeSlash)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return "", "", fmt.Errorf("AutoResearch path parent is not a directory: %s", relativeSlash)
		}
		if index == len(parts)-1 && (!info.Mode().IsRegular() || info.Size() > int64(maxBytes)) {
			return "", "", fmt.Errorf("AutoResearch file %s must be regular and no larger than %d bytes", relativeSlash, maxBytes)
		}
	}
	return relativeSlash, current, nil
}

func snapshotAutoResearchFiles(workspacePath string, files []string, maxBytes int, allowResearchAssistant bool) ([]autoResearchFileSnapshot, error) {
	snapshots := make([]autoResearchFileSnapshot, 0, len(files))
	for _, relative := range files {
		normalized, path, err := autoResearchExistingFile(workspacePath, relative, maxBytes, allowResearchAssistant)
		if err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, autoResearchFileSnapshot{Workspace: workspacePath, Relative: normalized, Path: path, Content: raw, Mode: info.Mode()})
	}
	return snapshots, nil
}

func restoreAutoResearchSnapshots(snapshots []autoResearchFileSnapshot) error {
	for _, snapshot := range snapshots {
		if err := prepareAutoResearchRestorePath(snapshot); err != nil {
			return err
		}
		if info, err := os.Lstat(snapshot.Path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
			if err := os.RemoveAll(snapshot.Path); err != nil {
				return err
			}
		}
		if err := writePaperDebugFile(snapshot.Path, snapshot.Content, snapshot.Mode); err != nil {
			return err
		}
	}
	return nil
}

func restoreAutoResearchGroups(groups ...[]autoResearchFileSnapshot) error {
	for _, snapshots := range groups {
		if err := restoreAutoResearchSnapshots(snapshots); err != nil {
			return err
		}
	}
	return nil
}

func restoreAutoResearchWorkspace(workspacePath string, editableFiles []string, immutableSnapshots []autoResearchFileSnapshot, groups ...[]autoResearchFileSnapshot) error {
	expected := make(map[string]struct{}, len(immutableSnapshots))
	for _, snapshot := range immutableSnapshots {
		expected[snapshot.Relative] = struct{}{}
	}
	currentFiles, err := autoResearchImmutableSourceFiles(workspacePath, editableFiles)
	if err != nil {
		return err
	}
	for _, file := range currentFiles {
		if _, existed := expected[file.relative]; existed {
			continue
		}
		boundary := autoResearchFileSnapshot{Workspace: workspacePath, Relative: file.relative, Path: file.path}
		if err := prepareAutoResearchRestorePath(boundary); err != nil {
			return err
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := restoreAutoResearchSnapshots(immutableSnapshots); err != nil {
		return err
	}
	return restoreAutoResearchGroups(groups...)
}

func prepareAutoResearchRestorePath(snapshot autoResearchFileSnapshot) error {
	workspacePath := filepath.Clean(snapshot.Workspace)
	if workspacePath == "." || snapshot.Relative == "" {
		return fmt.Errorf("AutoResearch restore snapshot is missing its workspace boundary")
	}
	expectedPath := filepath.Join(workspacePath, filepath.FromSlash(snapshot.Relative))
	if filepath.Clean(expectedPath) != filepath.Clean(snapshot.Path) {
		return fmt.Errorf("AutoResearch restore path does not match its workspace-relative snapshot")
	}
	current := workspacePath
	parts := strings.Split(filepath.ToSlash(snapshot.Relative), "/")
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("AutoResearch restore parent is not a regular directory: %s", filepath.ToSlash(current))
		}
	}
	return nil
}

func hashAutoResearchFiles(workspacePath string, files []string, maxBytes int, allowResearchAssistant bool) ([]models.ResearchFileHash, error) {
	hashes := make([]models.ResearchFileHash, 0, len(files))
	for _, candidate := range files {
		relative, path, err := autoResearchExistingFile(workspacePath, candidate, maxBytes, allowResearchAssistant)
		if err != nil {
			return nil, err
		}
		hash, err := sha256File(path)
		if err != nil {
			return nil, err
		}
		hashes = append(hashes, models.ResearchFileHash{Path: relative, SHA256: hash})
	}
	sort.Slice(hashes, func(i, j int) bool { return hashes[i].Path < hashes[j].Path })
	return hashes, nil
}

func verifyAutoResearchSpecFiles(workspacePath string, spec models.ResearchSpec) error {
	current, err := hashAutoResearchFiles(workspacePath, spec.ProtectedFiles, autoResearchMaxProtectedBytes, true)
	if err != nil {
		return fmt.Errorf("%w: protected file unavailable: %v", errAutoResearchIntegrity, err)
	}
	if !equalAutoResearchHashes(current, spec.FrozenProtected) {
		return fmt.Errorf("%w: protected AutoResearch evaluator or data changed", errAutoResearchIntegrity)
	}
	return nil
}

func equalAutoResearchHashes(left, right []models.ResearchFileHash) bool {
	if len(left) != len(right) {
		return false
	}
	leftMap := make(map[string]string, len(left))
	for _, item := range left {
		if _, duplicate := leftMap[item.Path]; duplicate {
			return false
		}
		leftMap[item.Path] = item.SHA256
	}
	rightPaths := make(map[string]struct{}, len(right))
	for _, item := range right {
		if _, duplicate := rightPaths[item.Path]; duplicate {
			return false
		}
		rightPaths[item.Path] = struct{}{}
		if leftMap[item.Path] != item.SHA256 {
			return false
		}
	}
	return true
}
