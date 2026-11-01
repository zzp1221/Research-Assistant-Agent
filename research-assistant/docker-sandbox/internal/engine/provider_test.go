package engine

import "testing"

func TestDockerProviderName(t *testing.T) {
	if got := NewDockerProvider().Name(); got != "docker" {
		t.Fatalf("Name() = %q", got)
	}
}
