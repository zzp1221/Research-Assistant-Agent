package memory

import (
	"errors"
	"os"
	"path/filepath"
	"research-assistant-backend/internal/lockfile"
	"testing"
	"time"
)

func TestInitializationLockRecoveryFailureIsBounded(t *testing.T) {
	dir := t.TempDir()
	// An unreadable/malformed lock cannot be proven stale and must be preserved.
	path := filepath.Join(dir, ".memory.lock")
	if err := os.WriteFile(path, []byte("unknown owner"), 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	opened, err := NewFileStore(dir)
	if opened != nil || !errors.Is(err, lockfile.ErrTimeout) {
		t.Fatalf("store=%v err=%v", opened, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("initialization exceeded lock deadline")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("failed init deleted another lock", err)
	}
}
