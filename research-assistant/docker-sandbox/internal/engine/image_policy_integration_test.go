package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Opt in explicitly: these tests create only uniquely named test-owned tags,
// containers and an internal network. They never retag a user's source image.
func requireDockerSecurityIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("RA_TEST_DOCKER_INTEGRATION") != "1" {
		t.Skip("set RA_TEST_DOCKER_INTEGRATION=1 with a real Docker daemon and python:3.11-bullseye installed")
	}
	if out, err := exec.Command(dockerCommand(), "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		t.Fatalf("requested Docker integration unavailable: %v (%s)", err, out)
	}
	t.Setenv("SANDBOX_DOCKER_GPUS", "none")
	t.Setenv("SANDBOX_REGISTRY_MIRROR", "")
	t.Setenv("SANDBOX_PIP_CACHE_DIR", "")
	t.Setenv("SANDBOX_SECURITY_PROFILE", "standard")
	t.Setenv("SANDBOX_CONTAINER_USER", "")
	t.Setenv("SANDBOX_READ_ONLY_ROOT", "false")
	t.Setenv("SANDBOX_NETWORK_MODE", "none")
	t.Setenv("SANDBOX_OFFLINE_NETWORK", fmt.Sprintf("ra-security-test-%d", time.Now().UnixNano()))
	network := offlineNetworkName()
	t.Cleanup(func() { _ = exec.Command(dockerCommand(), "network", "rm", network).Run() })
}

func dockerIntegrationOutput(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, dockerCommand(), args...).CombinedOutput()
	if err != nil {
		t.Fatalf("Docker %v: %v (%s)", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestImagePolicyRealDockerDigestAndImmutableExecution(t *testing.T) {
	requireDockerSecurityIntegration(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	const source = "python:3.11-bullseye"
	var repoDigests []string
	if err := json.Unmarshal([]byte(dockerIntegrationOutput(t, "image", "inspect", source, "--format", "{{json .RepoDigests}}")), &repoDigests); err != nil || len(repoDigests) == 0 {
		t.Fatal("real repository digest required; pull python:3.11-bullseye before this test")
	}
	ref, err := parseImageReference(repoDigests[0])
	if err != nil || ref.digest == "" {
		t.Fatal("installed image has no valid repository digest")
	}
	image := "docker.io/library/python@" + ref.digest.String()
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", image)
	e := NewNativeDockerEngine()
	info, err := e.EnsureImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	if info.RepoDigest == "" || info.Digest != ref.digest.String() || info.DigestSource != "repository_manifest" || info.Platform != "linux/amd64" {
		t.Fatalf("real provenance does not bind expected source/content/platform: %#v", info)
	}
	id, err := e.Create(ctx, CreateOptions{Image: image, Labels: map[string]string{tenantLabelKey: "security-integration", taskLabelKey: "digest"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Delete(context.Background(), id) })
	actual := dockerIntegrationOutput(t, "inspect", "--format", "{{.Image}}|{{.Config.Image}}", id)
	if actual != info.ImageID+"|"+info.ImageID {
		t.Fatal("container did not execute the inspected immutable image ID")
	}
	result, err := e.ExecutePython(ctx, id, "import sys; print('security-runtime-'+str(sys.version_info.major)+'.'+str(sys.version_info.minor))")
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, "security-runtime-3.11") {
		t.Fatalf("real approved runtime failed: %#v %v", result, err)
	}
	t.Logf("real digest=%s image_id=%s platform=%s; approved CPython execution passed", info.Digest, info.ImageID, info.Platform)
}

func TestImagePolicyRealDockerTagDriftRunsPreparedContent(t *testing.T) {
	requireDockerSecurityIntegration(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	name := fmt.Sprintf("ra-security-%d:approved", time.Now().UnixNano())
	dockerIntegrationOutput(t, "tag", "python:3.11-bullseye", name)
	t.Cleanup(func() { _ = exec.Command(dockerCommand(), "image", "rm", name).Run() })
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", name)
	e := NewNativeDockerEngine()
	before, err := e.EnsureImage(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	dockerIntegrationOutput(t, "tag", "alpine:latest", name)
	changed := dockerIntegrationOutput(t, "image", "inspect", name, "--format", "{{.Id}}")
	if changed == before.ImageID {
		t.Fatal("positive control did not change the mutable tag")
	}
	id, err := e.Create(ctx, CreateOptions{Image: name, Labels: map[string]string{tenantLabelKey: "security-integration", taskLabelKey: "tag-drift"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Delete(context.Background(), id) })
	if actual := dockerIntegrationOutput(t, "inspect", "--format", "{{.Image}}", id); actual != before.ImageID {
		t.Fatal("moved tag replaced the prepared content")
	}
	result, err := e.ExecutePython(ctx, id, "print('original-python-content')")
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, "original-python-content") {
		t.Fatalf("prepared Python content was lost after tag drift: %#v %v", result, err)
	}
	t.Log("real mutable tag changed to a different image; execution retained the prepared Python image")
}

func TestImagePolicyRealTrustedRegistryPullAndDigest(t *testing.T) {
	requireDockerSecurityIntegration(t)
	// Use a registry started in the daemon's network namespace, bound only to
	// 127.0.0.1. No global daemon trust/insecure-registry configuration changes.
	registry := strings.TrimSpace(os.Getenv("RA_TEST_DOCKER_REGISTRY"))
	if registry == "" {
		t.Skip("set RA_TEST_DOCKER_REGISTRY to a test-owned localhost registry")
	}
	if !strings.HasPrefix(registry, "127.0.0.1:") {
		t.Fatal("integration registry must be bound to loopback")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	tag := fmt.Sprintf("security-%d", time.Now().UnixNano())
	transport := registry + "/library/python:" + tag
	dockerIntegrationOutput(t, "tag", "python:3.11-bullseye", transport)
	t.Cleanup(func() { _ = exec.Command(dockerCommand(), "image", "rm", transport).Run() })
	dockerIntegrationOutput(t, "push", transport)
	// Remove only our temporary tag so the product has to perform a real
	// registry pull; all of the user's original image tags remain untouched.
	dockerIntegrationOutput(t, "image", "rm", transport)
	logical := "python:" + tag
	t.Setenv("SANDBOX_REGISTRY_MIRROR", registry)
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", logical)
	e := NewNativeDockerEngine()
	info, err := e.EnsureImage(ctx, logical)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Pulled || info.ResolvedImage != transport || info.RepoDigest == "" || info.DigestSource != "repository_manifest" {
		t.Fatalf("trusted mirror did not pull and bind real repository digest: %#v", info)
	}
	approved := "python@" + info.Digest
	t.Setenv("SANDBOX_IMAGE_ALLOWLIST", logical+","+approved)
	digestInfo, err := e.EnsureImage(ctx, approved)
	if err != nil || digestInfo.ImageID != info.ImageID || digestInfo.Digest != info.Digest {
		t.Fatalf("mirror digest did not resolve to approved content: %#v %v", digestInfo, err)
	}
	id, err := e.Create(ctx, CreateOptions{Image: approved, Labels: map[string]string{tenantLabelKey: "security-integration", taskLabelKey: "trusted-registry"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Delete(context.Background(), id) })
	result, err := e.ExecutePython(ctx, id, "print('trusted-registry-content')")
	if err != nil || result.ExitCode != 0 || !strings.Contains(result.Stdout, "trusted-registry-content") {
		t.Fatalf("real mirror-pinned runtime did not execute: %#v %v", result, err)
	}
	t.Logf("logical=%s resolved=%s digest=%s platform=%s; actual mirror pull and execution passed", info.Image, info.ResolvedImage, info.Digest, info.Platform)
}
