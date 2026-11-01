package proxyenv

import (
	"os"
	"testing"
)

func TestApplyClearsProxyByDefault(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://corp:8080")
	t.Setenv("RA_KEEP_SYSTEM_PROXY", "")
	Apply()
	if got := os.Getenv("HTTP_PROXY"); got != "" {
		t.Fatalf("HTTP_PROXY = %q, want empty", got)
	}
	if got := os.Getenv("NO_PROXY"); got == "" {
		t.Fatalf("NO_PROXY should be populated")
	}
}

func TestApplyKeepsProxyWhenRequested(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://corp:8080")
	t.Setenv("RA_KEEP_SYSTEM_PROXY", "1")
	Apply()
	if got := os.Getenv("HTTP_PROXY"); got != "http://corp:8080" {
		t.Fatalf("HTTP_PROXY = %q, want preserved", got)
	}
}
