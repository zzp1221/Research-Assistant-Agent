//go:build linux

package lockfile

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

// Run with LOCKFILE_READONLY_TEST_PATH pointing to a stale lock on an actual
// read-only mount. Root can bypass chmod, but cannot bypass the mount flag.
func TestReadOnlyMountRecovery(t *testing.T) {
	path := os.Getenv("LOCKFILE_READONLY_TEST_PATH")
	if path == "" {
		t.Skip("requires a read-only mount")
	}
	start := time.Now()
	_, err := Acquire(path, 40*time.Millisecond)
	if err == nil || (!errors.Is(err, syscall.EROFS) && !errors.Is(err, os.ErrPermission)) {
		t.Fatalf("expected read-only cleanup failure: %v", err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("read-only recovery exceeded 500ms")
	}
	t.Logf("read-only stale cleanup returned in %s: %v", time.Since(start), err)
}
