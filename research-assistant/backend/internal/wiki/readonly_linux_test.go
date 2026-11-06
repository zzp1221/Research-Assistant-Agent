//go:build linux

package wiki

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestReadOnlyMountInitializationFailure(t *testing.T) {
	root := os.Getenv("STORE_READONLY_TEST_DIR")
	if root == "" {
		t.Skip("requires a read-only mount with stale store lock files")
	}
	start := time.Now()
	opened, err := NewPaperStore(root)
	if opened != nil || err == nil || (!errors.Is(err, syscall.EROFS) && !errors.Is(err, os.ErrPermission)) {
		t.Fatalf("store=%v err=%v", opened, err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("store initialization did not propagate cleanup failure")
	}
	t.Logf("initialization failed after %s: %v", time.Since(start), err)
}
