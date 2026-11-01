//go:build windows

package winpath

import (
	"path/filepath"
	"strings"
)

const (
	extendedPrefix    = "\\\\?\\"
	extendedUNCPrefix = "\\\\?\\UNC\\"
)

func longPath(path string) string {
	if strings.TrimSpace(path) == "" {
		return path
	}
	if strings.HasPrefix(path, extendedPrefix) {
		return path
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if len(abs) < 260 {
		return abs
	}
	if strings.HasPrefix(abs, "\\\\") {
		return extendedUNCPrefix + strings.TrimPrefix(abs, "\\\\")
	}
	return extendedPrefix + abs
}
