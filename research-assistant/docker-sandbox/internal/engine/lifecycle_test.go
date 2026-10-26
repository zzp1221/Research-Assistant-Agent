package engine

import (
	"os"
	"testing"
)

func TestRewriteImageWithRegistryMirror(t *testing.T) {
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "docker.m.daocloud.io")
	cases := map[string]string{
		"python:3.11-bullseye":              "docker.m.daocloud.io/library/python:3.11-bullseye",
		"library/python:3.11":               "docker.m.daocloud.io/library/python:3.11",
		"docker.m.daocloud.io/library/python:3.11": "docker.m.daocloud.io/library/python:3.11",
		"ghcr.io/org/image:tag":             "ghcr.io/org/image:tag",
	}
	for input, expected := range cases {
		if got := rewriteImageWithRegistryMirror(input); got != expected {
			t.Fatalf("%s: expected %s, got %s", input, expected, got)
		}
	}
}

func TestRewriteImageWithoutMirrorIsIdentity(t *testing.T) {
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	if got := rewriteImageWithRegistryMirror("python:3.11-bullseye"); got != "python:3.11-bullseye" {
		t.Fatalf("unexpected rewrite: %s", got)
	}
}

func TestSandboxIdentityValidation(t *testing.T) {
	for _, value := range []string{"abc-123", "a.b:c", "A_1"} {
		if !sandboxIdentityValid(value) {
			t.Fatalf("%q should be valid", value)
		}
	}
	for _, value := range []string{"", "a b", "a;rm -rf", "../x"} {
		if sandboxIdentityValid(value) {
			t.Fatalf("%q should be invalid", value)
		}
	}
	_ = os.Getenv("SANDBOX_IMAGE_ALLOWLIST")
}
