// Package paths resolves per-user desktop directories.
package paths

import (
	"os"
	"path/filepath"
	"strings"
)

// InstallDir is the per-user installation directory.
func InstallDir() string {
	if configured := strings.TrimSpace(os.Getenv("RA_INSTALL_DIR")); configured != "" {
		return filepath.Clean(configured)
	}
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			base = dir
		} else {
			base = "."
		}
	}
	return filepath.Join(base, "Programs", "ResearchAssistant")
}

// DataDir is the writable application data directory.
func DataDir() string {
	if configured := strings.TrimSpace(os.Getenv("APP_DATA_DIR")); configured != "" {
		return filepath.Clean(configured)
	}
	base := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	if base == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			base = dir
		} else {
			base = "."
		}
	}
	return filepath.Join(base, "ResearchAssistant")
}

// LogDir is DataDir/logs unless LOG_DIR overrides it.
func LogDir() string {
	if configured := strings.TrimSpace(os.Getenv("LOG_DIR")); configured != "" {
		return filepath.Clean(configured)
	}
	return filepath.Join(DataDir(), "logs")
}

// Ensure creates a directory with 0755 permissions.
func Ensure(dir string) error { return os.MkdirAll(dir, 0o755) }
