package engine

import (
	"strings"
	"testing"
)

func TestDefaultNetworkIsNone(t *testing.T) {
	t.Setenv("SANDBOX_NETWORK_MODE", "")
	args, err := secureDockerRunArgs("python:3.11-slim", nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--network research-assistant-offline") {
		t.Fatalf("default sandbox must use the internal offline network: %s", joined)
	}
	if strings.Contains(joined, "--network bridge") || strings.Contains(joined, "--network none") {
		t.Fatalf("default sandbox must not reach the network: %s", joined)
	}
}

func TestBridgeNetworkRequiresExplicitRequestAndPolicy(t *testing.T) {
	t.Setenv("SANDBOX_NETWORK_MODE", "")
	t.Setenv("SANDBOX_ALLOW_NETWORK", "true")
	args, err := secureDockerRunArgs("python:3.11-slim", nil, CreateOptions{NetworkMode: NetworkBridge})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "--network bridge") {
		t.Fatalf("explicit bridge request was ignored: %s", strings.Join(args, " "))
	}
	t.Setenv("SANDBOX_ALLOW_NETWORK", "false")
	if _, err := secureDockerRunArgs("python:3.11-slim", nil, CreateOptions{NetworkMode: NetworkBridge}); err == nil {
		t.Fatal("bridge network must be rejected when SANDBOX_ALLOW_NETWORK=false")
	}
}

func TestUnsupportedNetworkModeRejected(t *testing.T) {
	if _, err := secureDockerRunArgs("python:3.11-slim", nil, CreateOptions{NetworkMode: "host"}); err == nil {
		t.Fatal("host network must not be accepted")
	}
}

func TestDiskLimitAddsTmpfsOnlyWithoutHostMount(t *testing.T) {
	args, err := secureDockerRunArgs("python:3.11-slim", nil, CreateOptions{Limits: Limits{MaxDiskGB: 2}})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--tmpfs /workspace:rw,exec,size=2g") {
		t.Fatalf("expected tmpfs workspace limit: %s", joined)
	}
	args, err = secureDockerRunArgs("python:3.11-slim", nil, CreateOptions{MountPath: "C:\\tmp", Limits: Limits{MaxDiskGB: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(args, " "), "--tmpfs /workspace") {
		t.Fatal("host-mounted workspace must not be replaced by tmpfs")
	}
}

func TestRuntimeAllowlist(t *testing.T) {
	t.Setenv("SANDBOX_ALLOWED_RUNTIMES", "runc,runsc")
	args, err := secureDockerRunArgs("python:3.11-slim", nil, CreateOptions{Runtime: "runsc"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, " "), "--runtime runsc") {
		t.Fatal("allowed runtime was not applied")
	}
	if _, err := secureDockerRunArgs("python:3.11-slim", nil, CreateOptions{Runtime: "evil-runtime"}); err == nil {
		t.Fatal("unlisted runtime must be rejected")
	}
}

func TestParseDockerBytes(t *testing.T) {
	cases := map[string]int64{
		"0B":     0,
		"512B":   512,
		"1.5kB":  1500,
		"2MiB":   2 * 1024 * 1024,
		"1.25GB": 1250000000,
		"3 GiB":  3 * 1024 * 1024 * 1024,
	}
	for raw, want := range cases {
		got, err := parseDockerBytes(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if got != want {
			t.Fatalf("parse %q=%d want %d", raw, got, want)
		}
	}
	if _, err := parseDockerBytes("two megabytes"); err == nil {
		t.Fatal("expected invalid unit to fail")
	}
}

func TestStrictSecurityProfileAppliesNonRootReadOnly(t *testing.T) {
	t.Setenv("SANDBOX_SECURITY_PROFILE", "strict")
	t.Setenv("SANDBOX_CONTAINER_USER", "")
	t.Setenv("SANDBOX_READ_ONLY_ROOT", "")
	args, err := secureDockerRunArgs("python:3.11-slim", nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, expected := range []string{"--read-only", "--user 65534:65534", "--tmpfs /root/.cache:rw,size=1g", "--network research-assistant-offline"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("strict profile missing %q: %s", expected, joined)
		}
	}
}
