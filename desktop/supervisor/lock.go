package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// InstanceLock is a cross-platform exclusive lock file used for single
// instance startup. The file is removed on Release.
type InstanceLock struct {
	path string
	file *os.File
}

// AcquireInstanceLock creates the lock file. A lock left by a process that
// no longer runs is stale: the next launch replaces it instead of refusing to
// start forever after a crash.
func AcquireInstanceLock(path string) (*InstanceLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = file.WriteString(fmt.Sprintf("%d", os.Getpid()))
			return &InstanceLock{path: path, file: file}, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if lockOwnerAlive(path) {
			return nil, fmt.Errorf("another ResearchAssistant instance is already running")
		}
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, fmt.Errorf("remove stale lock: %w", removeErr)
		}
	}
	return nil, fmt.Errorf("another ResearchAssistant instance is already running")
}

// lockOwnerAlive reports whether the PID recorded in path is still running.
// An unreadable or malformed lock file is stale.
func lockOwnerAlive(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return false
	}
	return processAlive(pid)
}

// Release removes the lock file.
func (l *InstanceLock) Release() error {
	if l == nil {
		return nil
	}
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
	err := os.Remove(l.path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
