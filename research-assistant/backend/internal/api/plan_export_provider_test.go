package api

import (
	"testing"
	"time"

	"research-assistant-backend/internal/models"
)

func TestBuildPlanBundleNativeEnvironment(t *testing.T) {
	plan := &models.PlanGraph{
		ID: "p-native", CurrentRevision: 1, UpdatedAt: time.Now(),
		EnvironmentManifest: &models.EnvironmentManifest{
			Version: "environment.manifest/v1", SandboxProvider: "native", Isolation: "lowil",
			RuntimeHash: "abc123", BaseOS: "windows", NetworkEnforced: "none",
			RunCommand: []string{"python", "main.py"}, Status: "ready",
		},
	}
	bundle, err := buildPlanBundle(plan, nil, exportLimits{})
	if err != nil {
		t.Fatalf("buildPlanBundle: %v", err)
	}
	environment := bundle.Manifest.Environment
	if environment.SandboxProvider != "native" || environment.RuntimeHash != "abc123" || environment.Isolation != "lowil" {
		t.Fatalf("environment = %#v", environment)
	}
	if stringSliceContains(bundle.Manifest.Missing, "image_digest") {
		t.Fatalf("native bundle should not require image digest: %#v", bundle.Manifest.Missing)
	}
	found := false
	for _, file := range bundle.Files {
		if file.Path == "environment/manifest.json" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("environment/manifest.json missing from bundle: %#v", bundle.Files)
	}
}

func TestBuildPlanBundleDockerRequiresImageDigest(t *testing.T) {
	plan := &models.PlanGraph{
		ID: "p-docker", CurrentRevision: 1, UpdatedAt: time.Now(),
		EnvironmentManifest: &models.EnvironmentManifest{
			Version: "environment.manifest/v1", SandboxProvider: "docker", Isolation: "container",
			Image: "python:3.11-bullseye", Status: "ready",
		},
	}
	bundle, err := buildPlanBundle(plan, nil, exportLimits{})
	if err != nil {
		t.Fatalf("buildPlanBundle: %v", err)
	}
	if !stringSliceContains(bundle.Manifest.Missing, "image_digest") {
		t.Fatalf("docker bundle should require image digest: %#v", bundle.Manifest.Missing)
	}
}
