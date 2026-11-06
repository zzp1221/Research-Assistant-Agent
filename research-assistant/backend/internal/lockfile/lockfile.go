// Package lockfile implements the small advisory lock used by the JSONL
// stores. It records the owning PID so a crashed process cannot permanently
// block the next start (which would silently fall back to an in-memory store).
package lockfile

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// filesystemOps is per-acquisition injection; tests never change global I/O.
type lockWriter interface {
	io.Writer
	io.Closer
}
type filesystemOps struct {
	create func(string) (lockWriter, error)
	read   func(string) ([]byte, error)
	remove func(string) error
	alive  func(int) bool
}

var ErrTimeout = errors.New("lock acquisition timed out")

// Acquire creates path exclusively. Nonpositive timeouts attempt creation once.
// Positive timeouts bound retries/backoff, not a blocking kernel filesystem call.
func Acquire(path string, timeout time.Duration) (*os.File, error) {
	writer, err := acquire(path, timeout, filesystemOps{
		create: func(path string) (lockWriter, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		},
		read: os.ReadFile, remove: os.Remove, alive: processAlive,
	})
	if err != nil {
		return nil, err
	}
	return writer.(*os.File), nil
}
func acquire(path string, timeout time.Duration, ops filesystemOps) (lockWriter, error) {
	started := time.Now()
	deadline := started.Add(timeout)
	cleanupLogged := false
	for first := true; ; first = false {
		if !first && !time.Now().Before(deadline) {
			return nil, fmt.Errorf("acquire lock %q after %s: %w", path, time.Since(started), ErrTimeout)
		}
		file, err := ops.create(path)
		if err == nil {
			if _, err := fmt.Fprintf(file, "%d\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
				closeErr := file.Close()
				removeErr := ops.remove(path)
				return nil, fmt.Errorf("write lock information %q: %w", path, errors.Join(err, closeErr, removeErr))
			}
			return file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create lock %q: %w", path, err)
		}
		if timeout <= 0 {
			return nil, fmt.Errorf("acquire lock %q without waiting: %w", path, ErrTimeout)
		}
		if staleWith(path, ops.read, ops.alive) {
			err := ops.remove(path)
			if !cleanupLogged || (err != nil && !errors.Is(err, os.ErrNotExist)) {
				log.Printf("lock cleanup path=%q elapsed=%s result=%v", path, time.Since(started), err)
				cleanupLogged = true
			}
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("remove stale lock %q after %s: %w", path, time.Since(started), err)
			}
			continue
		}
		remaining := time.Until(deadline)
		if remaining > 0 {
			time.Sleep(min(25*time.Millisecond, remaining))
		}
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
	return staleWith(path, os.ReadFile, processAlive)
}

func staleWith(path string, read func(string) ([]byte, error), alive func(int) bool) bool {
	raw, err := read(path)
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
	return !alive(pid)
}
