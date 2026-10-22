package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UploadRoot returns the only directory tree from which uploaded files may be
// read by agents. When UPLOAD_ROOT is not configured we also allow the OS temp
// directory so local tests and single-binary development keep working.
func UploadRoots() []string {
	roots := make([]string, 0, 2)
	if configured := strings.TrimSpace(os.Getenv("UPLOAD_ROOT")); configured != "" {
		roots = append(roots, configured)
	} else {
		roots = append(roots, os.TempDir())
	}
	return normalizeRoots(roots)
}

// WorkspaceRoots returns the allowed roots for task workspaces. The sandbox
// service uses the same SANDBOX_WORKSPACE_ROOTS setting for its bind mounts.
func WorkspaceRoots() []string {
	raw := strings.TrimSpace(os.Getenv("SANDBOX_WORKSPACE_ROOTS"))
	if raw == "" {
		return normalizeRoots([]string{os.TempDir()})
	}
	return normalizeRoots(filepath.SplitList(raw))
}

// ValidateUploadedFilePath resolves an uploaded-file path and verifies that it
// is a regular file inside one of the configured upload roots. Symlinks are
// rejected so a malicious upload directory cannot point at /etc or a home dir.
func ValidateUploadedFilePath(raw string) (string, error) {
	return validatePath(raw, UploadRoots(), false)
}

// ValidateWorkspacePath resolves a workspace path and verifies that it is a
// directory inside one of the configured sandbox workspace roots.
func ValidateWorkspacePath(raw string) (string, error) {
	return validatePath(raw, WorkspaceRoots(), true)
}

// ValidateWorkspaceFilePath verifies that a file consumed from a workspace
// (for example code_file_path) is a regular file inside an allowed workspace
// root. It applies the same symlink and traversal checks as the directory
// variant.
func ValidateWorkspaceFilePath(raw string) (string, error) {
	return validatePath(raw, WorkspaceRoots(), false)
}

func validatePath(raw string, roots []string, requireDir bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	abs = filepath.Clean(abs)

	info, err := os.Lstat(abs)
	if err != nil {
		return "", fmt.Errorf("path is unavailable: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("path must not be a symbolic link")
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve path symlinks: %w", err)
	}
	resolved = filepath.Clean(resolved)
	info, err = os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("inspect path: %w", err)
	}
	if requireDir && !info.IsDir() {
		return "", fmt.Errorf("path is not a directory")
	}
	if !requireDir && !info.Mode().IsRegular() {
		return "", fmt.Errorf("path is not a regular file")
	}

	for _, root := range roots {
		relative, relErr := filepath.Rel(root, resolved)
		if relErr != nil {
			continue
		}
		if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("path is outside the allowed roots")
}

func normalizeRoots(raw []string) []string {
	roots := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, candidate := range raw {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = filepath.Clean(resolved)
		}
		if _, exists := seen[abs]; exists {
			continue
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}
	return roots
}
