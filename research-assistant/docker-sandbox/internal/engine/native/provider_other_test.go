//go:build !windows

package native

import (
	"errors"
	"testing"
)

func TestNewProviderUnavailableOnNonWindows(t *testing.T) {
	_, err := NewProvider(Config{DataDir: t.TempDir()})
	if !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("err = %v, want ErrUnsupportedPlatform", err)
	}
}
