// Package lockfile implements the small advisory lock used by the JSONL
// stores. It records the owning PID so a crashed process cannot permanently
// block the next start (which would silently fall back to an in-memory store).
package lockfile

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Acquire creates path exclusively, waiting up to timeout. A lock whose PID is
// no longer alive is treated as stale and removed.
func Acquire(path string, timeout time.Duration) (*os.File, error) {
	deadline := time.Now().Add(timeout)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(file, "%d\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			return file, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
		if stale(path) {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("lock is held by another live process: %s", path)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// Release closes and removes the lock file.
func Release(file *os.File, path string) error {
	if file != nil {
		_ = file.Close()
	}
	return os.Remove(path)
}

func stale(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) == 0 {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || pid <= 0 || pid == os.Getpid() {
		return false
	}
	return !processAlive(pid)
}
