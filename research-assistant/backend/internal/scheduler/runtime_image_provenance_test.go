package scheduler

import (
	"encoding/json"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestRuntimeImageArtifactsPreserveProvenance(t *testing.T) {
	const provenance = `{"image":"docker.io/library/python:3.11-bullseye","image_id":"sha256:test","digest_source":"image_config"}`
	node := &models.TaskNode{ID: "prepare", OutputArtifacts: []string{"runtime_image", "image_digest", "unrelated"}}
	task := &models.Task{Metadata: map[string]any{
		"runtime_image_provenance": provenance,
		"artifact_values":          map[string]string{"runtime_image": "docker.io/library/python:3.11-bullseye", "image_digest": "sha256:test"},
	}}
	for _, artifact := range buildArtifacts(node, task) {
		value, present := artifact.Metadata["runtime_image_provenance"]
		if artifact.Key == "unrelated" {
			if present {
				t.Fatal("unrelated artifact inherited runtime provenance")
			}
		} else if !present || value != provenance {
			t.Fatalf("source metadata lost: %#v", artifact)
		}
	}
}

func TestImageApprovalIncludesFrozenDigest(t *testing.T) {
	spec := models.ReproductionRuntimeSpec{BaseImage: "docker.io/library/python:3.11-bullseye", ImageDigest: "sha256:frozen"}
	raw, _ := json.Marshal(spec)
	plan := &models.PlanGraph{Artifacts: map[string]models.Artifact{"reproduction_runtime_spec": {Value: string(raw)}}}
	_, _, details, _, gated := runtimeActionForTask(plan, &models.TaskNode{Type: "image_prepare"})
	if !gated || details["expected_image_digest"] != spec.ImageDigest || details["image"] != spec.BaseImage {
		t.Fatalf("approval lost frozen source contract: %#v", details)
	}
}
