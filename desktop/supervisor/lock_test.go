package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestInstanceLockExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := AcquireInstanceLock(path); err == nil {
		t.Fatal("second acquire should fail while the first holds the lock")
	}
	if err := first.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	second, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	_ = second.Release()
}

func TestInstanceLockReplacesStalePID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, []byte("99999999"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatalf("acquire over stale lock: %v", err)
	}
	_ = lock.Release()
}

func TestInstanceLockRejectsLiveOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("%d", os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireInstanceLock(path); err == nil {
		t.Fatal("acquire should refuse a lock held by a live process")
	}
}

func TestInstanceLockReplacesMalformedLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(path, []byte("not-a-pid"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireInstanceLock(path)
	if err != nil {
		t.Fatalf("acquire over malformed lock: %v", err)
	}
	_ = lock.Release()
}
