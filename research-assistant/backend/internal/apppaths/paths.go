// Package apppaths resolves runtime directories for the desktop and server
// entrypoints. Desktop builds inject APP_DATA_DIR / LOG_DIR / RA_CONFIG_PATH;
// development keeps the historical repository-relative fallback.
package apppaths

import (
	"os"
	"path/filepath"
	"strings"
)

// RepoRoot walks up from the working directory looking for the repository
// layout (backend + docker-sandbox siblings). It returns the working directory
// when no layout is found.
func RepoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	candidates := []string{wd, filepath.Dir(wd), filepath.Dir(filepath.Dir(wd))}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if isRepoRoot(candidate) {
			return candidate
		}
	}
	return wd
}

func isRepoRoot(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, "backend")); err != nil || !info.IsDir() {
		return false
	}
	if info, err := os.Stat(filepath.Join(dir, "docker-sandbox")); err != nil || !info.IsDir() {
		return false
	}
	return true
}

// DataDir returns the writable application data directory.
func DataDir() string {
	if configured := strings.TrimSpace(os.Getenv("APP_DATA_DIR")); configured != "" {
		return filepath.Clean(configured)
	}
	return filepath.Join(RepoRoot(), "data")
}

// LogDir returns the log directory. LOG_DIR wins, then APP_DATA_DIR/logs.
func LogDir() string {
	if configured := strings.TrimSpace(os.Getenv("LOG_DIR")); configured != "" {
		return filepath.Clean(configured)
	}
	return filepath.Join(DataDir(), "logs")
}

// ConfigPath returns the explicitly configured config.toml path, if any.
func ConfigPath() string {
	return strings.TrimSpace(os.Getenv("RA_CONFIG_PATH"))
}

// EnsureDir creates dir with 0755 permissions.
func EnsureDir(dir string) error {
	return os.MkdirAll(dir, 0o755)
}
