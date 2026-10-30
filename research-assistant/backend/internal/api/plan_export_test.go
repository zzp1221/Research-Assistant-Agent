package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"research-assistant-backend/internal/events"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
)

func newExportRouter(t *testing.T) (*gin.Engine, store.PlanStore) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	planStore := store.NewMemoryPlanStore()
	eventBus := events.NewBus()
	runtime := newPlanRuntime(planner.NewPlanner(), planStore, scheduler.NewScheduler(planStore, scheduler.NewDefaultTaskExecutor(), eventBus, 1), eventBus)
	router := gin.New()
	runtime.register(router.Group("/api"))
	return router, planStore
}

func exportTestPlan() *models.PlanGraph {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	plan := &models.PlanGraph{
		ID:         "export-plan",
		UserIntent: "Reproduce the attention experiment",
		IntentType: "Paper_Reproduction",
		Status:     models.StatusCompleted,
		OwnerID:    "owner-a",
		Nodes: []*models.TaskNode{
			{
				ID:     "n1",
				Name:   "prepare data",
				Type:   "execute_code",
				Status: models.StatusCompleted,
				Inputs: map[string]any{
					"api_key":       "sk-super-secret-value-123456",
					"Authorization": "Bearer abcdefghijklmnop",
					"note":          "safe text",
				},
				Usage: &models.TaskUsage{LLMPromptTokens: 100, LLMCompletionTokens: 50, LLMCostUSD: 0.02, DurationSeconds: 3},
			},
		},
		Edges:     []*models.TaskEdge{},
		Artifacts: map[string]models.Artifact{},
		Usage:     models.RunUsage{LLMPromptTokens: 100, LLMCompletionTokens: 50, LLMCostUSD: 0.02, SandboxSeconds: 3},
		EnvironmentManifest: &models.EnvironmentManifest{
			Version:        "v1",
			Image:          "python:3.11-bullseye",
			ImageDigest:    "sha256:deadbeef",
			PipFreeze:      []models.PackageSpec{{Name: "numpy", Version: "1.26.4"}, {Name: "torch", Version: "2.4.0"}},
			SystemPackages: []string{"git", "ffmpeg"},
			RunCommand:     []string{"python", "train.py", "--seed", "42"},
			WorkingDir:     "/workspace",
			DatasetHashes:  map[string]string{"wmt14": "abc123"},
			ModelHashes:    map[string]string{"transformer": "def456"},
			StartedAt:      now,
		},
		CreatedAt: now.Add(-time.Hour),
		UpdatedAt: now,
	}
	return plan
}

func exportRequest(router *gin.Engine, method, path string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set(userIDHeaderName, "owner-a")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func readZip(t *testing.T, response *httptest.ResponseRecorder) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if err != nil {
		t.Fatalf("read zip: %v", err)
	}
	files := map[string][]byte{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", file.Name, err)
		}
		data, err := io.ReadAll(handle)
		_ = handle.Close()
		if err != nil {
			t.Fatalf("read %s: %v", file.Name, err)
		}
		files[file.Name] = data
	}
	return files
}

func TestExportBundleContainsManifestChecksumsAndRecipe(t *testing.T) {
	router, planStore := newExportRouter(t)
	plan := exportTestPlan()
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	response := exportRequest(router, http.MethodGet, "/api/plans/export-plan/export?format=zip")
	if response.Code != http.StatusOK {
		t.Fatalf("export status=%d body=%s", response.Code, response.Body.String())
	}
	files := readZip(t, response)
	for _, required := range []string{"README.md", "manifest.json", "checksums.sha256", "plan.json", "events.jsonl", "environment/requirements.lock", "environment/system-packages.txt", "environment/Dockerfile", "environment/run.sh", "provenance/model-snapshot.json", "provenance/runtime-policy.json", "provenance/dataset-hashes.json", "provenance/model-hashes.json"} {
		if _, ok := files[required]; !ok {
			t.Fatalf("bundle missing %s; files=%v", required, keysOf(files))
		}
	}
	manifest := bundleManifest{}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != reproSchemaVersion || manifest.PlanID != plan.ID || manifest.Reproducibility != "complete" {
		t.Fatalf("bad manifest: %#v", manifest)
	}
	hashByPath := map[string]string{}
	for _, file := range manifest.Files {
		hashByPath[file.Path] = file.SHA256
		if got := sha256Hex(files[file.Path]); got != file.SHA256 {
			t.Fatalf("hash mismatch for %s: manifest=%s actual=%s", file.Path, file.SHA256, got)
		}
	}
	for _, line := range strings.Split(strings.TrimSpace(string(files["checksums.sha256"])), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "  ", 2)
		if len(parts) != 2 {
			t.Fatalf("bad checksums line %q", line)
		}
		if parts[1] == "manifest.json" {
			if sha256Hex(files["manifest.json"]) != parts[0] {
				t.Fatalf("manifest hash mismatch")
			}
			continue
		}
		if sha256Hex(files[parts[1]]) != parts[0] {
			t.Fatalf("checksum mismatch for %s", parts[1])
		}
	}
	requirements := string(files["environment/requirements.lock"])
	if !strings.Contains(requirements, "numpy==1.26.4") || !strings.Contains(requirements, "torch==2.4.0") {
		t.Fatalf("requirements.lock bad: %s", requirements)
	}
	dockerfile := string(files["environment/Dockerfile"])
	if !strings.Contains(dockerfile, "FROM python:3.11-bullseye@sha256:deadbeef") {
		t.Fatalf("dockerfile not pinned: %s", dockerfile)
	}
	runScript := string(files["environment/run.sh"])
	if !strings.Contains(runScript, "exec python train.py --seed 42") {
		t.Fatalf("run.sh command bad: %s", runScript)
	}
	if !strings.Contains(runScript, "PYTHONHASHSEED=0") {
		t.Fatalf("run.sh missing seed")
	}
}

func TestExportBundleRedactsSecrets(t *testing.T) {
	router, planStore := newExportRouter(t)
	plan := exportTestPlan()
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	response := exportRequest(router, http.MethodGet, "/api/plans/export-plan/export?format=zip")
	files := readZip(t, response)
	combined := string(files["plan.json"]) + string(files["README.md"]) + string(files["events.jsonl"])
	for _, secret := range []string{"sk-super-secret-value-123456", "abcdefghijklmnop"} {
		if strings.Contains(combined, secret) {
			t.Fatalf("secret %q leaked into bundle", secret)
		}
	}
	if !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("expected redaction marker")
	}
	jsonResponse := exportRequest(router, http.MethodGet, "/api/plans/export-plan/export?format=json")
	if strings.Contains(jsonResponse.Body.String(), "sk-super-secret") {
		t.Fatalf("json export leaked secret")
	}
	mdResponse := exportRequest(router, http.MethodGet, "/api/plans/export-plan/export?format=md")
	if strings.Contains(mdResponse.Body.String(), "sk-super-secret") {
		t.Fatalf("markdown export leaked secret")
	}
	if !strings.Contains(mdResponse.Body.String(), "How to reproduce") {
		t.Fatalf("markdown missing how-to")
	}
}

func TestExportBundlePartialWhenDigestMissingAndVerify(t *testing.T) {
	router, planStore := newExportRouter(t)
	plan := exportTestPlan()
	plan.EnvironmentManifest.ImageDigest = ""
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	response := exportRequest(router, http.MethodGet, "/api/plans/export-plan/export?format=zip")
	if got := response.Header().Get("X-Repro-Status"); got != "partial" {
		t.Fatalf("status header=%q, want partial", got)
	}
	files := readZip(t, response)
	manifest := bundleManifest{}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Reproducibility != "partial" || !stringSliceContains(manifest.Missing, "image_digest") {
		t.Fatalf("manifest not partial: %#v", manifest)
	}
	verify := exportRequest(router, http.MethodPost, "/api/plans/export-plan/reproduce/verify")
	if verify.Code != http.StatusOK {
		t.Fatalf("verify status=%d body=%s", verify.Code, verify.Body.String())
	}
	var verdict struct {
		Status     string `json:"status"`
		StaticOnly bool   `json:"static_only"`
		Checks     []struct {
			Check string `json:"check"`
			OK    bool   `json:"ok"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(verify.Body.Bytes(), &verdict); err != nil {
		t.Fatal(err)
	}
	if verdict.Status != "partial" || !verdict.StaticOnly {
		t.Fatalf("verdict=%#v", verdict)
	}
	for _, check := range verdict.Checks {
		if !check.OK {
			t.Fatalf("check failed: %#v", check)
		}
	}
}

func TestExportBundleDeterministic(t *testing.T) {
	router, planStore := newExportRouter(t)
	plan := exportTestPlan()
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	first := exportRequest(router, http.MethodGet, "/api/plans/export-plan/export?format=zip")
	second := exportRequest(router, http.MethodGet, "/api/plans/export-plan/export?format=zip")
	firstFiles := readZip(t, first)
	secondFiles := readZip(t, second)
	if len(firstFiles) != len(secondFiles) {
		t.Fatalf("file count differs")
	}
	for name, data := range firstFiles {
		if !bytes.Equal(data, secondFiles[name]) {
			t.Fatalf("file %s differs between identical exports", name)
		}
	}
}

func TestExportBundleOmitsOversizedArtifact(t *testing.T) {
	t.Setenv("EXPORT_MAX_FILE_MB", "1")
	router, planStore := newExportRouter(t)
	plan := exportTestPlan()
	large := bytes.Repeat([]byte("x"), 2*1024*1024)
	planner.SetBlobLoader(func(ref string) ([]byte, error) {
		if ref == "blob:big" {
			return large, nil
		}
		return nil, io.EOF
	})
	defer planner.SetBlobLoader(nil)
	plan.Artifacts["big"] = models.Artifact{Key: "big", Type: "text", BlobRef: "blob:big"}
	if err := planStore.SavePlan(plan); err != nil {
		t.Fatal(err)
	}
	response := exportRequest(router, http.MethodGet, "/api/plans/export-plan/export?format=zip")
	files := readZip(t, response)
	manifest := bundleManifest{}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Omitted) != 1 || manifest.Omitted[0].Reason != "file_too_large" {
		t.Fatalf("omitted=%#v", manifest.Omitted)
	}
	if manifest.Reproducibility != "partial" {
		t.Fatalf("want partial, got %s", manifest.Reproducibility)
	}
}

func TestExportBundleRedactsTextArtifactContent(t *testing.T) {
	plan := exportTestPlan()
	plan.Artifacts["run.log"] = models.Artifact{Key: "run.log", Type: "log", BlobRef: "blob:secret-log"}
	planner.SetBlobLoader(func(ref string) ([]byte, error) {
		if ref == "blob:secret-log" {
			return []byte("Authorization: Bearer abcdefghijklmnop\napi_key=sk-super-secret-value-123456\n"), nil
		}
		return nil, io.EOF
	})
	defer planner.SetBlobLoader(nil)
	bundle, err := buildPlanBundle(plan, nil, exportLimits{})
	if err != nil {
		t.Fatal(err)
	}
	var logData []byte
	paths := []string{}
	for _, file := range bundle.Files {
		paths = append(paths, file.Path)
		if file.Path == "artifacts/run.log.txt" {
			logData = file.Data
		}
	}
	if logData == nil {
		t.Fatalf("artifact missing; files=%v", paths)
	}
	if strings.Contains(string(logData), "sk-super-secret-value-123456") || strings.Contains(string(logData), "abcdefghijklmnop") {
		t.Fatalf("artifact secret leaked: %s", string(logData))
	}
	if !strings.Contains(string(logData), "[REDACTED]") {
		t.Fatalf("artifact was not redacted: %s", string(logData))
	}
}

func TestExportManifestRevisionIsNumeric(t *testing.T) {
	plan := exportTestPlan()
	plan.CurrentRevision = 3
	bundle, err := buildPlanBundle(plan, nil, exportLimits{})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Manifest.Revision != 3 {
		t.Fatalf("manifest revision=%d, want 3", bundle.Manifest.Revision)
	}
	var raw map[string]any
	if err := json.Unmarshal(bundle.ManifestBytes, &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["revision"].(float64); !ok {
		t.Fatalf("revision must be a JSON number, got %T", raw["revision"])
	}
}

func keysOf(files map[string][]byte) []string {
	out := make([]string, 0, len(files))
	for key := range files {
		out = append(out, key)
	}
	return out
}

func stringSliceContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
