package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

const testManifestDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testConfigDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const changedConfigDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestImagePolicyExactNormalizedReferences(t *testing.T) {
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:3.11-bullseye,registry.example:5443/team/runtime:Release,ghcr.io/team/runtime@"+testManifestDigest)
	cases := []struct {
		image string
		allow bool
	}{
		{"python:3.11-bullseye", true},
		{"library/python:3.11-bullseye", true},
		{"docker.io/library/python:3.11-bullseye", true},
		{"index.docker.io/library/python:3.11-bullseye", true},
		{"registry-1.docker.io/library/python:3.11-bullseye", true},
		{"python:3.11-bullseye-evil", false},
		{"evil.example/python:3.11-bullseye", false},
		{"evil.example/library/python:3.11-bullseye", false},
		{"python", false},
		{"python:latest", false},
		{"python:3.11-bullseye@" + testManifestDigest, false},
		{"registry.example:5443/team/runtime:Release", true},
		{"REGISTRY.EXAMPLE:5443/team/runtime:Release", true},
		{"registry.example:5443/team/runtime:release", false},
		{"registry.example:5444/team/runtime:Release", false},
		{"registry.example:5443/team/runtime-evil:Release", false},
		{"ghcr.io/team/runtime@" + testManifestDigest, true},
		{"ghcr.io/team/runtime:forged@" + testManifestDigest, false},
		{"ghcr.io/team/runtime@" + changedConfigDigest, false},
		{"ghcr.io/other/runtime@" + testManifestDigest, false},
		{"ghcr.io/team/runtime@sha256:bad", false},
		{"--privileged", false},
	}
	for _, tc := range cases {
		t.Run(tc.image, func(t *testing.T) {
			if got := sandboxImageAllowed(tc.image); got != tc.allow {
				t.Fatalf("allowed(%q)=%v want %v", tc.image, got, tc.allow)
			}
		})
	}
}

func TestImagePolicyDefaultsAndConfigurationFailures(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	if err := os.Unsetenv("SANDBOX_IMAGE_ALLOWLIST"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateImagePolicy(); err != nil || !sandboxImageAllowed("python:3.11-bullseye") || sandboxImageAllowed("ubuntu:latest") {
		t.Fatalf("unset policy must use a finite trusted default: %v", err)
	}
	for _, invalid := range []string{"", " ", ",", "python", "python:3.11,", "registry.example/team/", "python@sha256:bad"} {
		t.Run("policy="+invalid, func(t *testing.T) {
			t.Setenv("SANDBOX_IMAGE_ALLOWLIST", invalid)
			if err := ValidateImagePolicy(); err == nil {
				t.Fatalf("invalid policy %q accepted", invalid)
			}
			if sandboxImageAllowed("python:3.11-bullseye") {
				t.Fatal("invalid policy must fail closed")
			}
		})
	}
}

func TestImagePolicyTrustedMirrorMapping(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:3.11-bullseye,ghcr.io/team/runtime:stable")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "mirror.example:5443/cache")
	cases := []struct {
		image, resolved string
	}{
		{"python:3.11-bullseye", "mirror.example:5443/cache/library/python:3.11-bullseye"},
		{"docker.io/library/python:3.11-bullseye", "mirror.example:5443/cache/library/python:3.11-bullseye"},
		{"mirror.example:5443/cache/library/python:3.11-bullseye", "mirror.example:5443/cache/library/python:3.11-bullseye"},
		{"ghcr.io/team/runtime:stable", "ghcr.io/team/runtime:stable"},
	}
	for _, tc := range cases {
		got, err := authorizeDockerImage(tc.image)
		if err != nil || got.resolved.named.String() != tc.resolved {
			t.Fatalf("mapping %q: %#v %v", tc.image, got, err)
		}
	}
	for _, rejected := range []string{
		"other.example/cache/library/python:3.11-bullseye",
		"mirror.example.evil:5443/cache/library/python:3.11-bullseye",
		"mirror.example:5443/cache-evil/library/python:3.11-bullseye",
		"mirror.example:5443/cache/library/python:3.11-bullseye-evil",
	} {
		if _, err := authorizeDockerImage(rejected); err == nil {
			t.Fatalf("untrusted mirror/source %q accepted", rejected)
		}
	}
	for _, invalid := range []string{"https://mirror.example", "mirror.example/", "user@mirror.example", "mirror.example?x=1", "mirror.example#x", "docker.io", "not-a-host", "mirror.example/../cache"} {
		t.Setenv("SANDBOX_REGISTRY_MIRROR", invalid)
		if err := ValidateImagePolicy(); err == nil {
			t.Fatalf("invalid mirror %q accepted", invalid)
		}
	}
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "mirror.example")
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "mirror.example/library/python:3.11-bullseye")
	if err := ValidateImagePolicy(); err == nil {
		t.Fatal("conflicting mirror destination rule must identify its logical source")
	}
}

func TestImagePolicyTagAndDigestAreBothExact(t *testing.T) {
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "ghcr.io/team/runtime:approved@"+testManifestDigest)
	if !sandboxImageAllowed("ghcr.io/team/runtime:approved@" + testManifestDigest) {
		t.Fatal("exact tag+digest rule rejected")
	}
	for _, raw := range []string{"ghcr.io/team/runtime:changed@" + testManifestDigest, "ghcr.io/team/runtime@" + testManifestDigest, "ghcr.io/team/runtime:approved@" + changedConfigDigest} {
		if sandboxImageAllowed(raw) {
			t.Fatalf("tag+digest constraint weakened for %q", raw)
		}
	}
}

func fakeInspectedImage(imageID string, digests ...string) []byte {
	raw, _ := json.Marshal(map[string]any{"Id": imageID, "RepoDigests": digests, "Size": 1234, "Os": "linux", "Architecture": "amd64"})
	return raw
}

func TestImageRejectionPrecedesAllDockerSideEffects(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:3.11-bullseye")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	calls := 0
	e := NewNativeDockerEngine()
	e.commandOutput = func(context.Context, ...string) ([]byte, error) {
		calls++
		return nil, fmt.Errorf("must not call Docker")
	}
	for _, rejected := range []string{"evil.example/python:3.11-bullseye", "python:3.11-bullseye-evil", "python@sha256:bad"} {
		if _, err := e.EnsureImage(context.Background(), rejected); err == nil {
			t.Fatalf("ensure accepted %q", rejected)
		}
		if _, err := e.Create(context.Background(), CreateOptions{Image: rejected, Limits: Limits{MaxImageBytes: 1}}); err == nil {
			t.Fatalf("create accepted %q", rejected)
		}
	}
	if calls != 0 {
		t.Fatalf("rejection made %d Docker calls", calls)
	}
}

func TestImageEnsurePinsMutableTagForCreateAndReuse(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:3.11-bullseye")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "mirror.example")
	t.Setenv("SANDBOX_ALLOW_NETWORK", "true")
	t.Setenv("SANDBOX_PIP_CACHE_DIR", "")
	var calls [][]string
	tagMoved := false
	e := NewNativeDockerEngine()
	e.commandOutput = func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		if args[0] == "image" {
			if args[len(args)-1] == testConfigDigest {
				return fakeInspectedImage(testConfigDigest), nil
			}
			if tagMoved {
				return fakeInspectedImage(changedConfigDigest, "mirror.example/library/python@"+changedConfigDigest), nil
			}
			return fakeInspectedImage(testConfigDigest, "mirror.example/library/python@"+testManifestDigest), nil
		}
		if args[0] == "run" {
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "--pull=never -- "+testConfigDigest+" sleep infinity") {
				t.Fatalf("create must run the prepared immutable image: %s", joined)
			}
			return []byte("0123456789ab"), nil
		}
		return nil, fmt.Errorf("unexpected Docker call: %v", args)
	}
	info, err := e.EnsureImage(context.Background(), "python:3.11-bullseye")
	if err != nil {
		t.Fatal(err)
	}
	if info.Image != "docker.io/library/python:3.11-bullseye" || info.ResolvedImage != "mirror.example/library/python:3.11-bullseye" || info.Digest != testManifestDigest || info.DigestSource != "repository_manifest" || info.ImageID != testConfigDigest || info.Platform != "linux/amd64" {
		t.Fatalf("missing provenance: %#v", info)
	}
	tagMoved = true
	for range 2 {
		id, err := e.Create(context.Background(), CreateOptions{Image: "python:3.11-bullseye", NetworkMode: NetworkBridge})
		if err != nil || id != "0123456789ab" {
			t.Fatalf("create failed: %s %v", id, err)
		}
	}
	if len(calls) != 5 {
		t.Fatalf("expected first inspect plus two pinned inspect/run pairs, got %v", calls)
	}
}

func TestImageDigestMismatchCannotRunOrBecomePinned(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python@"+testManifestDigest)
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	e := NewNativeDockerEngine()
	e.commandOutput = func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] != "image" {
			t.Fatalf("digest mismatch must not pull or run: %v", args)
		}
		return fakeInspectedImage(testConfigDigest, "docker.io/library/python@"+changedConfigDigest), nil
	}
	if _, err := e.EnsureImage(context.Background(), "python@"+testManifestDigest); err == nil || !strings.Contains(err.Error(), "image_digest_mismatch") {
		t.Fatalf("digest mismatch accepted: %v", err)
	}
	if len(e.images) != 0 {
		t.Fatal("mismatched image became pinned")
	}
}

func TestImagePullAuthorizesTransportAndValidatesDigest(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python@"+testManifestDigest)
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "mirror.example")
	e := NewNativeDockerEngine()
	inspected, pulls := 0, 0
	e.commandOutput = func(_ context.Context, args ...string) ([]byte, error) {
		if args[len(args)-1] != "mirror.example/library/python@"+testManifestDigest {
			t.Fatalf("wrong image source: %v", args)
		}
		if args[0] == "pull" {
			pulls++
			return nil, nil
		}
		inspected++
		if inspected == 1 {
			return nil, fmt.Errorf("image not found")
		}
		return fakeInspectedImage(testConfigDigest, "mirror.example/library/python@"+testManifestDigest), nil
	}
	info, err := e.EnsureImage(context.Background(), "python@"+testManifestDigest)
	if err != nil || !info.Pulled || pulls != 1 || info.Digest != testManifestDigest {
		t.Fatalf("pull failed: %#v %v pulls=%d", info, err, pulls)
	}
}

func TestImageTagDigestMustMatchIndependentTag(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:approved@"+testManifestDigest)
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	for _, sameContent := range []bool{true, false} {
		e := NewNativeDockerEngine()
		e.commandOutput = func(_ context.Context, args ...string) ([]byte, error) {
			if args[0] != "image" {
				t.Fatalf("unexpected side effect: %v", args)
			}
			if args[len(args)-1] == "docker.io/library/python:approved" && !sameContent {
				return fakeInspectedImage(changedConfigDigest), nil
			}
			return fakeInspectedImage(testConfigDigest, "docker.io/library/python@"+testManifestDigest), nil
		}
		_, err := e.EnsureImage(context.Background(), "python:approved@"+testManifestDigest)
		if (err == nil) != sameContent {
			t.Fatalf("same tag content %v: %v", sameContent, err)
		}
	}
}

func TestPinnedImageMissingFailsInsteadOfResolvingMovedTag(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:3.11-bullseye")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	e := NewNativeDockerEngine()
	missing := false
	e.commandOutput = func(_ context.Context, args ...string) ([]byte, error) {
		if missing {
			if args[0] != "image" || args[len(args)-1] != testConfigDigest {
				t.Fatalf("must not resolve/re-pull the mutable tag: %v", args)
			}
			return nil, fmt.Errorf("prepared image removed")
		}
		return fakeInspectedImage(testConfigDigest), nil
	}
	if _, err := e.EnsureImage(context.Background(), "python:3.11-bullseye"); err != nil {
		t.Fatal(err)
	}
	missing = true
	if _, err := e.EnsureImage(context.Background(), "python:3.11-bullseye"); err == nil || !strings.Contains(err.Error(), "image_pin_unavailable") {
		t.Fatalf("missing pin must stop execution: %v", err)
	}
}

func TestOpenSandboxCannotBypassImagePolicy(t *testing.T) {
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", "python:3.11-bullseye")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"sandbox"}`))
	}))
	defer server.Close()
	e := NewOpenSandboxEngine(server.URL)
	if _, err := e.Create(context.Background(), "evil.example/python:3.11-bullseye", ""); err == nil {
		t.Fatal("remote/fallback accepted forbidden source")
	}
	if calls != 0 {
		t.Fatal("policy rejection reached remote service")
	}
}
