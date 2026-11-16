package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/sandbox"
)

func provenanceTestImage() sandbox.SandboxImageInfo {
	return sandbox.SandboxImageInfo{
		Image: "docker.io/library/python:3.11-bullseye", ResolvedImage: "mirror.example/library/python:3.11-bullseye",
		Digest: "sha256:" + strings.Repeat("a", 64), DigestSource: "repository_manifest",
		RepoDigest: "mirror.example/library/python@sha256:" + strings.Repeat("a", 64),
		ImageID:    "sha256:" + strings.Repeat("b", 64), Platform: "linux/amd64", SizeBytes: 1234,
	}
}

func TestImagePreparePreservesActualProvenanceAndFrozenDigest(t *testing.T) {
	info := provenanceTestImage()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/images/ensure" {
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(info)
	}))
	defer server.Close()
	coder := &CoderAgent{Sandbox: sandbox.NewSandboxClient(server.URL)}
	for _, correct := range []bool{true, false} {
		spec := models.ReproductionRuntimeSpec{BaseImage: info.Image, ImageDigest: info.Digest}
		if !correct {
			spec.ImageDigest = info.ImageID
		}
		rawSpec, _ := json.Marshal(spec)
		task := &models.Task{Inputs: map[string]any{"reproduction_runtime_spec": string(rawSpec)}}
		if err := coder.imagePrepare(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		if !correct {
			if task.Status != models.StatusFailed || !strings.Contains(task.Error, "image_digest_mismatch") {
				t.Fatalf("frozen mismatch accepted: %#v", task)
			}
			continue
		}
		if task.Status != models.StatusCompleted {
			t.Fatalf("image prepare failed: %#v", task)
		}
		var recorded sandbox.SandboxImageInfo
		if err := json.Unmarshal([]byte(task.Metadata["runtime_image_provenance"].(string)), &recorded); err != nil || recorded != info {
			t.Fatalf("provenance lost: %#v %v", recorded, err)
		}
		artifacts := task.Metadata["artifact_values"].(map[string]string)
		if artifacts["runtime_image"] != info.Image || artifacts["image_digest"] != info.Digest {
			t.Fatalf("wrong actual runtime artifacts: %v", artifacts)
		}
	}
}

func TestRuntimeManifestPreservesBoundImageProvenance(t *testing.T) {
	info := provenanceTestImage()
	task := &models.Task{}
	recordRuntimeImageProvenance(task, &info)
	manifest := models.EnvironmentManifest{Image: info.Image, ImageDigest: info.Digest}
	applyRuntimeImageProvenance(task, &manifest)
	if manifest.ImageResolved != info.ResolvedImage || manifest.ImageRepoDigest != info.RepoDigest || manifest.ImageID != info.ImageID || manifest.ImageDigestSource != info.DigestSource || manifest.ImagePlatform != info.Platform {
		t.Fatalf("manifest lost actual source: %#v", manifest)
	}
	other := models.EnvironmentManifest{Image: "other.example/runtime:latest", ImageDigest: info.Digest}
	applyRuntimeImageProvenance(task, &other)
	if other.ImageID != "" {
		t.Fatalf("attached another runtime's provenance: %#v", other)
	}
}
