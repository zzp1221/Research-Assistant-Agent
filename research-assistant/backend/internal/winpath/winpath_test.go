//go:build !windows

package winpath

import "testing"

func TestLongPathIsNoopOnNonWindows(t *testing.T) {
	path := "/tmp/some/path"
	if got := LongPath(path); got != path {
		t.Fatalf("LongPath() = %q, want %q", got, path)
	}
}
