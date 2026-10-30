package api

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"

	"github.com/gin-gonic/gin"
)

const (
	reproSchemaVersion = "repro.bundle/v1"
	planExportVersion  = "plan.export/v1"
	exportGenerator    = "research-assistant/backend"
)

// bundleFile is one file in a reproducible export bundle.
type bundleFile struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
	Data []byte `json:"-"`
}

type bundleManifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Kind   string `json:"kind"`
}

type bundleArtifactEntry struct {
	Key    string `json:"key"`
	Path   string `json:"path"`
	Type   string `json:"type,omitempty"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type bundleOmittedEntry struct {
	Path      string `json:"path"`
	Key       string `json:"key,omitempty"`
	Reason    string `json:"reason"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

type bundleEnvironment struct {
	Image       string `json:"image,omitempty"`
	ImageDigest string `json:"image_digest,omitempty"`
}

type bundleManifest struct {
	SchemaVersion   string                `json:"schema_version"`
	PlanID          string                `json:"plan_id"`
	Revision        int                   `json:"revision"`
	CreatedAt       time.Time             `json:"created_at"`
	Generator       string                `json:"generator"`
	Files           []bundleManifestFile  `json:"files"`
	Artifacts       []bundleArtifactEntry `json:"artifacts,omitempty"`
	Environment     bundleEnvironment     `json:"environment"`
	RunCommand      []string              `json:"run_command,omitempty"`
	Expected        map[string]string     `json:"expected,omitempty"`
	Omitted         []bundleOmittedEntry  `json:"omitted,omitempty"`
	Missing         []string              `json:"missing,omitempty"`
	Reproducibility string                `json:"reproducibility"`
	Notes           []string              `json:"notes,omitempty"`
}

type planBundle struct {
	Plan          *models.PlanGraph
	Files         []bundleFile
	Manifest      bundleManifest
	ManifestBytes []byte
	Checksums     []byte
	TotalBytes    int64
	BundleSHA256  string
}

type exportLimits struct {
	MaxFileBytes  int64
	MaxTotalBytes int64
}

func exportLimitsFromEnv() exportLimits {
	return exportLimits{
		MaxFileBytes:  int64(positiveEnvInt("EXPORT_MAX_FILE_MB", 16)) * 1024 * 1024,
		MaxTotalBytes: int64(positiveEnvInt("EXPORT_MAX_TOTAL_MB", 128)) * 1024 * 1024,
	}
}

// deleteSession removes every plan that belongs to one session. Running plans
// are refused so a delete can never orphan an active sandbox.
func (r *planRuntime) deleteSession(c *gin.Context) {
	userID := resolveUserID(c)
	sessionID := strings.TrimSpace(c.Param("id"))
	if sessionID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session id is required", "code": "session_required"})
		return
	}
	all, err := r.store.ListPlans()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot list plans", "code": "store_unavailable"})
		return
	}
	owned := make([]*models.PlanGraph, 0, 8)
	for _, plan := range all {
		if plan == nil || plan.OwnerID != userID || plan.SessionID != sessionID {
			continue
		}
		if plan.Status == models.StatusInProgress || plan.Status == models.StatusAwaitingApproval {
			c.JSON(http.StatusConflict, gin.H{
				"error":       "session has an active plan; cancel it before deleting the session",
				"code":        "session_has_active_plan",
				"remediation": errorRemediation("session_has_active_plan"),
				"plan_id":     plan.ID,
			})
			return
		}
		owned = append(owned, plan)
	}
	deleted := make([]string, 0, len(owned))
	for _, plan := range owned {
		if err := r.store.DeletePlan(plan.ID); err != nil {
			continue
		}
		_ = r.revisionStore().DeletePlan(plan.ID)
		r.scheduler.CleanupPlanSandboxes(c.Request.Context(), plan.ID)
		deleted = append(deleted, plan.ID)
	}
	recordAuditEvent("session_deleted", userID, sessionID, map[string]any{"plans": len(deleted)})
	c.JSON(http.StatusOK, gin.H{"deleted": len(deleted), "plan_ids": deleted, "session_id": sessionID})
}

// exportPlan produces a portable reproduction package. Markdown is the default,
// JSON keeps machine-readable detail, and ZIP bundles the markdown plus the
// plan snapshot, events, environment recipe and artifact blobs.
func (r *planRuntime) exportPlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "md")))
	baseName := "plan-" + plan.ID
	events := r.loadPlanEvents(plan.ID)
	switch format {
	case "json":
		payload, err := marshalRedactedPlan(plan, true)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot encode plan", "code": "export_failed"})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", baseName+".json"))
		c.Header("X-Repro-Schema", planExportVersion)
		recordAuditEvent("plan_exported", resolveUserID(c), plan.ID, map[string]any{"format": "json", "bytes": len(payload), "schema": planExportVersion})
		metrics.RecordPlanExport("json", "complete")
		c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
	case "md", "markdown":
		markdown := renderPlanMarkdown(redactedPlanCopy(plan), events)
		markdown = redactString(markdown)
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", baseName+".md"))
		c.Header("X-Repro-Schema", planExportVersion)
		recordAuditEvent("plan_exported", resolveUserID(c), plan.ID, map[string]any{"format": "md", "bytes": len(markdown), "schema": planExportVersion})
		metrics.RecordPlanExport("md", "complete")
		c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(markdown))
	case "zip":
		r.writePlanZip(c, plan, baseName+".zip")
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "format must be md, json or zip", "code": "invalid_format"})
	}
}

func (r *planRuntime) loadPlanEvents(planID string) []models.PlanEvent {
	events, err := r.store.ListEventsAfter(planID, 0)
	if err != nil {
		return nil
	}
	return events
}

func marshalRedactedPlan(plan *models.PlanGraph, indent bool) ([]byte, error) {
	redacted := redactedPlanCopy(plan)
	if indent {
		return json.MarshalIndent(redacted, "", "  ")
	}
	return json.Marshal(redacted)
}

// redactedPlanCopy deep-copies a plan through JSON so sensitive map keys can be
// stripped without mutating the stored graph.
func redactedPlanCopy(plan *models.PlanGraph) *models.PlanGraph {
	if plan == nil {
		return nil
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		copy := *plan
		return &copy
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		copy := *plan
		return &copy
	}
	redacted, err := json.Marshal(redactJSONValue(decoded, 0))
	if err != nil {
		copy := *plan
		return &copy
	}
	var out models.PlanGraph
	if err := json.Unmarshal(redacted, &out); err != nil {
		copy := *plan
		return &copy
	}
	// Inline base64 images are huge and already live in artifacts/; the export
	// plan.json keeps a pointer instead of duplicating megabytes.
	for _, node := range out.Nodes {
		if node == nil {
			continue
		}
		if len(node.ImageBase64) > 512 {
			node.ImageBase64 = "[omitted: inline image stored as artifact]"
		}
	}
	for key, artifact := range out.Artifacts {
		if len(artifact.Value) > 4096 && strings.HasPrefix(strings.TrimSpace(artifact.Value), "data:image") {
			artifact.Value = "[omitted: inline image stored as artifact]"
			out.Artifacts[key] = artifact
		}
	}
	return &out
}

func renderRedactedEvents(events []models.PlanEvent) []byte {
	var builder strings.Builder
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			continue
		}
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			if cleaned, err := json.Marshal(redactJSONValue(decoded, 0)); err == nil {
				builder.Write(cleaned)
				builder.WriteByte('\n')
				continue
			}
		}
		builder.Write(raw)
		builder.WriteByte('\n')
	}
	return []byte(builder.String())
}

func renderPlanMarkdown(plan *models.PlanGraph, events []models.PlanEvent) string {
	var builder strings.Builder
	builder.WriteString("# " + strings.TrimSpace(plan.UserIntent) + "\n\n")
	fmt.Fprintf(&builder, "- Export schema: `%s`\n", planExportVersion)
	fmt.Fprintf(&builder, "- Plan ID: `%s`\n", plan.ID)
	fmt.Fprintf(&builder, "- Intent type: %s\n", plan.IntentType)
	fmt.Fprintf(&builder, "- Status: %s (outcome: %s)\n", plan.Status, plan.Outcome)
	fmt.Fprintf(&builder, "- Created: %s\n", plan.CreatedAt.Format(time.RFC3339))
	if plan.Usage.FinishedAt != nil {
		fmt.Fprintf(&builder, "- Finished: %s\n", plan.Usage.FinishedAt.Format(time.RFC3339))
	}
	if plan.Paused {
		builder.WriteString("- Paused: true\n")
	}
	if plan.RunTrigger != "" {
		fmt.Fprintf(&builder, "- Run trigger: %s\n", plan.RunTrigger)
	}
	fmt.Fprintf(&builder, "- LLM usage: %d calls, %d prompt tokens, %d completion tokens, $%.4f%s\n",
		plan.Usage.LLMCalls, plan.Usage.LLMPromptTokens, plan.Usage.LLMCompletionTokens, plan.Usage.LLMCostUSD,
		estimatedSuffix(plan.Usage.LLMEstimated))
	fmt.Fprintf(&builder, "- Sandbox time: %.1fs\n\n", plan.Usage.SandboxSeconds)

	builder.WriteString("## How to reproduce\n\n")
	builder.WriteString("1. Unpack the bundle and verify every file: `sha256sum -c checksums.sha256`.\n")
	builder.WriteString("2. Build the environment: `docker build -f environment/Dockerfile -t plan-" + plan.ID + " .`.\n")
	builder.WriteString("3. Run the recorded command: `docker run --rm -v \"$PWD\":/workspace plan-" + plan.ID + "`.\n")
	builder.WriteString("4. Compare the results against `manifest.json` expected outputs and the artifact hashes below.\n")
	builder.WriteString("\nThe bundle is statically verifiable; a full replay in a clean container is a separate step.\n\n")

	if len(plan.MissingRequirements) > 0 {
		builder.WriteString("## Missing requirements\n\n")
		for _, missing := range plan.MissingRequirements {
			fmt.Fprintf(&builder, "- **%s** %s: %s\n  - Fix: %s\n", missing.Kind, missing.Name, missing.Detail, missing.Remediation)
		}
		builder.WriteString("\n")
	}

	if plan.EnvironmentManifest != nil {
		builder.WriteString("## Environment manifest\n\n")
		encoded, _ := json.MarshalIndent(plan.EnvironmentManifest, "", "  ")
		builder.WriteString("```json\n" + string(encoded) + "\n```\n\n")
	}

	builder.WriteString("## Nodes\n\n")
	builder.WriteString("| # | Node | Type | Agent | Status | Duration | Tokens | Cost |\n|---|---|---|---|---|---|---|---|\n")
	for index, node := range plan.Nodes {
		if node == nil {
			continue
		}
		duration := ""
		tokens := ""
		cost := ""
		if node.Usage != nil {
			duration = fmt.Sprintf("%.1fs", node.Usage.DurationSeconds)
			tokens = fmt.Sprintf("%d", node.Usage.LLMPromptTokens+node.Usage.LLMCompletionTokens)
			cost = fmt.Sprintf("$%.4f", node.Usage.LLMCostUSD)
		}
		fmt.Fprintf(&builder, "| %d | %s | %s | %s | %s | %s | %s | %s |\n", index+1, node.Name, node.Type, node.AssignedTo, node.Status, duration, tokens, cost)
	}
	builder.WriteString("\n")
	for index, node := range plan.Nodes {
		if node == nil {
			continue
		}
		if strings.TrimSpace(node.Code) == "" && strings.TrimSpace(node.Result) == "" && node.Error == "" {
			continue
		}
		fmt.Fprintf(&builder, "### %d. %s\n\n", index+1, node.Name)
		if node.Error != "" {
			fmt.Fprintf(&builder, "Error: %s\n\n", node.Error)
		}
		if strings.TrimSpace(node.Code) != "" {
			builder.WriteString("```python\n" + node.Code + "\n```\n\n")
		}
		if strings.TrimSpace(node.Result) != "" {
			result := truncateRunes(node.Result, 8000)
			builder.WriteString("```\n" + result + "\n```\n\n")
		}
	}

	if len(plan.AgentSnapshot) > 0 {
		builder.WriteString("## Agent / Prompt versions\n\n")
		builder.WriteString("| AgentProfile | PromptVersion | Prompt v | Prompt hash | ModelProfile | Model rev |\n|---|---|---|---|---|---|\n")
		for _, snapshot := range plan.AgentSnapshot {
			fmt.Fprintf(&builder, "| %s | %s | %d | %s | %s | %d |\n",
				snapshot.AgentProfileID, snapshot.PromptVersionID, snapshot.PromptVersion, snapshot.PromptHash, snapshot.ModelProfileID, snapshot.ModelRevision)
		}
		builder.WriteString("\n")
	}

	if len(plan.ToolCalls) > 0 {
		builder.WriteString("## Tool calls\n\n")
		builder.WriteString("| Call | Tool | Version | Status | Duration | Approval | Args hash | Result hash |\n|---|---|---|---|---|---|---|---|\n")
		for _, call := range plan.ToolCalls {
			fmt.Fprintf(&builder, "| %s | %s | %s | %s | %dms | %s | %s | %s |\n",
				call.ID, call.ToolID, call.ToolVersion, call.Status, call.DurationMS, call.ApprovalID, call.ArgumentsHash, call.ResultHash)
		}
		builder.WriteString("\n")
	}

	builder.WriteString("## Artifacts\n\n")
	keys := make([]string, 0, len(plan.Artifacts))
	for key := range plan.Artifacts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		artifact := plan.Artifacts[key]
		location := artifact.Location
		if strings.TrimSpace(location) == "" && strings.TrimSpace(artifact.BlobRef) != "" {
			location = "/api/artifacts/" + artifact.BlobRef
		}
		fmt.Fprintf(&builder, "- `%s` (%s) %s\n", key, artifact.Type, location)
	}
	builder.WriteString("\n## Event summary\n\n")
	counts := map[string]int{}
	for _, event := range events {
		counts[event.EventType]++
	}
	types := make([]string, 0, len(counts))
	for eventType := range counts {
		types = append(types, eventType)
	}
	sort.Strings(types)
	for _, eventType := range types {
		fmt.Fprintf(&builder, "- %s: %d\n", eventType, counts[eventType])
	}
	return builder.String()
}

func estimatedSuffix(estimated bool) string {
	if estimated {
		return " (estimated)"
	}
	return ""
}

// buildPlanBundle assembles every file of the reproducible export in memory so
// hashes and the manifest are computed before a single byte is streamed.
func buildPlanBundle(plan *models.PlanGraph, events []models.PlanEvent, limits exportLimits) (*planBundle, error) {
	if plan == nil {
		return nil, fmt.Errorf("plan is nil")
	}
	bundle := &planBundle{Plan: plan}
	manifest := bundleManifest{
		SchemaVersion:   reproSchemaVersion,
		PlanID:          plan.ID,
		Revision:        plan.CurrentRevision,
		CreatedAt:       plan.UpdatedAt.UTC(),
		Generator:       exportGenerator,
		Reproducibility: "complete",
		Expected:        map[string]string{"report": "README.md"},
	}
	missing := map[string]bool{}
	omitted := make([]bundleOmittedEntry, 0)

	redactedPlan := redactedPlanCopy(plan)
	planJSON, err := json.MarshalIndent(redactedPlan, "", "  ")
	if err != nil {
		return nil, err
	}
	eventsJSONL := renderRedactedEvents(events)
	markdown := redactString(renderPlanMarkdown(redactedPlan, events))

	add := func(filePath, kind string, data []byte) {
		bundle.Files = append(bundle.Files, bundleFile{Path: filePath, Kind: kind, Data: data})
	}

	add("README.md", "report", []byte(markdown))
	add("plan.json", "plan", planJSON)
	add("events.jsonl", "events", eventsJSONL)

	env := plan.EnvironmentManifest
	if env == nil {
		missing["environment_manifest"] = true
		manifest.Notes = append(manifest.Notes, "Environment manifest is absent; environment recipe is incomplete.")
		env = &models.EnvironmentManifest{}
	}
	requirements := renderRequirementsLock(env.PipFreeze)
	systemPackages := renderSystemPackages(env.SystemPackages)
	dockerfile := renderDockerfile(env)
	runScript, runErr := renderRunScript(env)
	if runErr != nil {
		missing["run_command"] = true
		manifest.Notes = append(manifest.Notes, "No run command was recorded; run.sh only explains what is missing.")
	}
	add("environment/requirements.lock", "environment", requirements)
	add("environment/system-packages.txt", "environment", systemPackages)
	add("environment/Dockerfile", "environment", dockerfile)
	add("environment/run.sh", "environment", runScript)

	modelSnapshot, _ := json.MarshalIndent(plan.ModelSnapshot, "", "  ")
	if plan.ModelSnapshot == nil {
		modelSnapshot = []byte("{}")
	}
	runtimePolicy, _ := json.MarshalIndent(plan.RuntimePolicy, "", "  ")
	datasetHashes, _ := json.MarshalIndent(env.DatasetHashes, "", "  ")
	modelHashes, _ := json.MarshalIndent(env.ModelHashes, "", "  ")
	add("provenance/model-snapshot.json", "provenance", modelSnapshot)
	add("provenance/runtime-policy.json", "provenance", runtimePolicy)
	add("provenance/dataset-hashes.json", "provenance", datasetHashes)
	add("provenance/model-hashes.json", "provenance", modelHashes)

	manifest.Environment = bundleEnvironment{Image: env.Image, ImageDigest: env.ImageDigest}
	manifest.RunCommand = append([]string(nil), env.RunCommand...)
	if strings.TrimSpace(env.Image) == "" {
		missing["environment_image"] = true
	}
	if strings.TrimSpace(env.ImageDigest) == "" {
		missing["image_digest"] = true
		manifest.Notes = append(manifest.Notes, "Image digest is missing; the base image is not pinned.")
	}

	artifactKeys := make([]string, 0, len(plan.Artifacts))
	for key := range plan.Artifacts {
		artifactKeys = append(artifactKeys, key)
	}
	sort.Strings(artifactKeys)
	usedNames := map[string]int{}
	totalBytes := int64(0)
	for _, key := range artifactKeys {
		artifact := plan.Artifacts[key]
		if strings.TrimSpace(artifact.BlobRef) == "" {
			continue
		}
		data, err := planner.LoadBlobForResponse(artifact.BlobRef)
		if err != nil || len(data) == 0 {
			missing["artifact:"+key] = true
			continue
		}
		// Artifacts are an export boundary too: text-like logs/reports can carry
		// bearer tokens or API keys even when plan.json is clean. Redact before
		// hashing and writing so checksums describe the shipped bytes.
		if artifactIsTextual(artifact.Type) {
			data = []byte(redactString(string(data)))
		}
		if !artifactTypeAllowed(artifact.Type) {
			omitted = append(omitted, bundleOmittedEntry{Path: "artifacts/" + sanitizeZipName(key), Key: key, Reason: "artifact_type_not_whitelisted", SizeBytes: int64(len(data))})
			continue
		}
		if limits.MaxFileBytes > 0 && int64(len(data)) > limits.MaxFileBytes {
			omitted = append(omitted, bundleOmittedEntry{Path: "artifacts/" + sanitizeZipName(key), Key: key, Reason: "file_too_large", SizeBytes: int64(len(data))})
			continue
		}
		if limits.MaxTotalBytes > 0 && totalBytes+int64(len(data)) > limits.MaxTotalBytes {
			omitted = append(omitted, bundleOmittedEntry{Path: "artifacts/" + sanitizeZipName(key), Key: key, Reason: "bundle_size_limit", SizeBytes: int64(len(data))})
			continue
		}
		extension := artifactExtension(artifact.Type)
		baseName := sanitizeZipName(key)
		if count := usedNames[baseName]; count > 0 {
			usedNames[baseName] = count + 1
			baseName = fmt.Sprintf("%s-%d", baseName, count+1)
		} else {
			usedNames[baseName] = 1
		}
		artifactPath := path.Join("artifacts", baseName+extension)
		add(artifactPath, "artifact", data)
		totalBytes += int64(len(data))
		manifest.Artifacts = append(manifest.Artifacts, bundleArtifactEntry{Key: key, Path: artifactPath, Type: artifact.Type, Size: int64(len(data)), SHA256: sha256Hex(data)})
		if strings.Contains(strings.ToLower(key+" "+artifact.Type), "metric") {
			manifest.Expected["metrics"] = artifactPath
		}
	}
	if len(omitted) > 0 {
		manifest.Omitted = omitted
		manifest.Notes = append(manifest.Notes, "Some artifacts were omitted from the bundle; see omitted entries.")
	}

	fileEntries := make([]bundleManifestFile, 0, len(bundle.Files))
	for _, file := range bundle.Files {
		fileEntries = append(fileEntries, bundleManifestFile{Path: file.Path, Size: int64(len(file.Data)), SHA256: sha256Hex(file.Data), Kind: file.Kind})
	}
	sort.Slice(fileEntries, func(i, j int) bool { return fileEntries[i].Path < fileEntries[j].Path })
	manifest.Files = fileEntries
	missingList := make([]string, 0, len(missing))
	for key := range missing {
		missingList = append(missingList, key)
	}
	sort.Strings(missingList)
	manifest.Missing = missingList
	if len(missingList) > 0 || len(omitted) > 0 {
		manifest.Reproducibility = "partial"
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	bundle.Manifest = manifest
	bundle.ManifestBytes = manifestBytes
	bundle.Checksums = renderChecksums(manifestBytes, bundle.Files)
	bundle.BundleSHA256 = sha256Hex(manifestBytes)
	for _, file := range bundle.Files {
		bundle.TotalBytes += int64(len(file.Data))
	}
	bundle.TotalBytes += int64(len(manifestBytes) + len(bundle.Checksums))
	return bundle, nil
}

func renderChecksums(manifestBytes []byte, files []bundleFile) []byte {
	entries := make([]struct{ Path, Hash string }, 0, len(files)+1)
	entries = append(entries, struct{ Path, Hash string }{Path: "manifest.json", Hash: sha256Hex(manifestBytes)})
	for _, file := range files {
		entries = append(entries, struct{ Path, Hash string }{Path: file.Path, Hash: sha256Hex(file.Data)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var builder strings.Builder
	for _, entry := range entries {
		builder.WriteString(entry.Hash)
		builder.WriteString("  ")
		builder.WriteString(entry.Path)
		builder.WriteByte('\n')
	}
	return []byte(builder.String())
}

func renderRequirementsLock(packages []models.PackageSpec) []byte {
	lines := make([]string, 0, len(packages))
	for _, pkg := range packages {
		name := strings.TrimSpace(pkg.Name)
		if name == "" {
			continue
		}
		version := strings.TrimSpace(pkg.Version)
		if version == "" {
			lines = append(lines, name)
		} else {
			lines = append(lines, name+"=="+version)
		}
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return []byte("# No Python dependencies were recorded in the environment manifest.\n")
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func renderSystemPackages(packages []string) []byte {
	lines := make([]string, 0, len(packages))
	for _, pkg := range packages {
		if value := strings.TrimSpace(pkg); value != "" {
			lines = append(lines, value)
		}
	}
	sort.Strings(lines)
	if len(lines) == 0 {
		return []byte("# No system packages were recorded in the environment manifest.\n")
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func renderDockerfile(env *models.EnvironmentManifest) []byte {
	image := strings.TrimSpace(env.Image)
	if image == "" {
		image = "debian:bookworm-slim"
	}
	from := image
	if digest := strings.TrimSpace(env.ImageDigest); digest != "" {
		from = image + "@" + digest
	}
	var builder strings.Builder
	builder.WriteString("# Generated by " + exportGenerator + ".\n")
	builder.WriteString("FROM " + from + "\n")
	builder.WriteString("ENV DEBIAN_FRONTEND=noninteractive\n")
	packages := make([]string, 0, len(env.SystemPackages))
	for _, pkg := range env.SystemPackages {
		if value := strings.TrimSpace(pkg); value != "" {
			packages = append(packages, value)
		}
	}
	if len(packages) > 0 {
		builder.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends " + strings.Join(packages, " ") + " && rm -rf /var/lib/apt/lists/*\n")
	}
	builder.WriteString("COPY environment/requirements.lock /tmp/requirements.lock\n")
	builder.WriteString("RUN if grep -Eqv '^[[:space:]]*(#|$)' /tmp/requirements.lock; then pip install --no-cache-dir -r /tmp/requirements.lock; fi\n")
	builder.WriteString("WORKDIR /workspace\n")
	builder.WriteString("COPY . /workspace\n")
	builder.WriteString("RUN chmod +x /workspace/environment/run.sh\n")
	builder.WriteString("CMD [\"bash\", \"/workspace/environment/run.sh\"]\n")
	return []byte(builder.String())
}

func renderRunScript(env *models.EnvironmentManifest) ([]byte, error) {
	var builder strings.Builder
	builder.WriteString("#!/usr/bin/env bash\n")
	builder.WriteString("set -euo pipefail\n")
	builder.WriteString("# Generated by " + exportGenerator + ".\n")
	builder.WriteString("cd \"$(dirname \"$0\")/..\"\n")
	if env != nil && len(env.DatasetHashes) > 0 {
		keys := make([]string, 0, len(env.DatasetHashes))
		for key := range env.DatasetHashes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&builder, "# dataset %s sha256=%s\n", key, env.DatasetHashes[key])
		}
	}
	builder.WriteString("export PYTHONHASHSEED=0\n")
	if env == nil || len(env.RunCommand) == 0 {
		builder.WriteString("echo \"No run command was recorded in the environment manifest; see README.md.\" >&2\n")
		builder.WriteString("exit 2\n")
		return []byte(builder.String()), fmt.Errorf("run command missing")
	}
	if workdir := strings.TrimSpace(env.WorkingDir); workdir != "" {
		builder.WriteString("mkdir -p " + shellQuote(workdir) + "\n")
		builder.WriteString("cd " + shellQuote(workdir) + "\n")
	}
	builder.WriteString("exec")
	for _, arg := range env.RunCommand {
		builder.WriteString(" " + shellQuote(arg))
	}
	builder.WriteByte('\n')
	return []byte(builder.String()), nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	// Leave ordinary command tokens readable; quote only when the shell would
	// interpret them differently.
	safe := true
	for _, char := range value {
		if !((char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("_./:=+-@%", char)) {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// artifactIsTextual reports whether an artifact can be treated as UTF-8 text
// for redaction. Binary image/plot artifacts are left byte-identical.
func artifactIsTextual(artifactType string) bool {
	lower := strings.ToLower(strings.TrimSpace(artifactType))
	for _, kind := range []string{"text", "log", "report", "markdown", "json", "csv", "yaml", "code", "python"} {
		if strings.Contains(lower, kind) {
			return true
		}
	}
	return false
}

func artifactTypeAllowed(artifactType string) bool {
	lower := strings.ToLower(strings.TrimSpace(artifactType))
	if lower == "" {
		return true
	}
	for _, allowed := range []string{"image", "json", "text", "report", "markdown", "csv", "table", "plot", "code", "python", "yaml", "log"} {
		if strings.Contains(lower, allowed) {
			return true
		}
	}
	return false
}

func artifactExtension(artifactType string) string {
	lower := strings.ToLower(artifactType)
	switch {
	case strings.Contains(lower, "image"):
		return ".png"
	case strings.Contains(lower, "json"):
		return ".json"
	case strings.Contains(lower, "report"), strings.Contains(lower, "markdown"):
		return ".md"
	case strings.Contains(lower, "csv"):
		return ".csv"
	case strings.Contains(lower, "yaml"):
		return ".yaml"
	case strings.Contains(lower, "text"), strings.Contains(lower, "log"):
		return ".txt"
	case strings.Contains(lower, "code"), strings.Contains(lower, "python"):
		return ".py"
	default:
		return ".bin"
	}
}

// truncateRunes cuts text at a rune boundary so multi-byte UTF-8 characters
// are never split by the export report.
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "\n...[truncated]"
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// sanitizeZipName keeps bundle entry names relative, flat and free of path
// traversal sequences.
func sanitizeZipName(value string) string {
	clean := path.Base(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	clean = strings.ReplaceAll(clean, "..", "_")
	if clean == "" || clean == "." || clean == "/" {
		return "artifact"
	}
	return clean
}

type countingWriter struct {
	writer io.Writer
	bytes  int64
}

func (w *countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	w.bytes += int64(n)
	return n, err
}

func (r *planRuntime) writePlanZip(c *gin.Context, plan *models.PlanGraph, filename string) {
	events := r.loadPlanEvents(plan.ID)
	bundle, err := buildPlanBundle(plan, events, exportLimitsFromEnv())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot build export bundle", "code": "export_failed", "detail": err.Error()})
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("Content-Type", "application/zip")
	c.Header("X-Repro-Schema", reproSchemaVersion)
	c.Header("X-Repro-Status", bundle.Manifest.Reproducibility)
	writer := &countingWriter{writer: c.Writer}
	zipWriter := zip.NewWriter(writer)
	add := func(filePath string, data []byte) error {
		entry, err := zipWriter.Create(filePath)
		if err != nil {
			return err
		}
		_, err = entry.Write(data)
		return err
	}
	for _, file := range bundle.Files {
		if err := add(file.Path, file.Data); err != nil {
			_ = zipWriter.Close()
			return
		}
	}
	if err := add("manifest.json", bundle.ManifestBytes); err != nil {
		_ = zipWriter.Close()
		return
	}
	if err := add("checksums.sha256", bundle.Checksums); err != nil {
		_ = zipWriter.Close()
		return
	}
	if err := zipWriter.Close(); err != nil {
		return
	}
	omittedBytes := int64(0)
	for _, entry := range bundle.Manifest.Omitted {
		omittedBytes += entry.SizeBytes
	}
	recordAuditEvent("plan_exported", resolveUserID(c), plan.ID, map[string]any{
		"format":          "zip",
		"bytes":           writer.bytes,
		"bundle_sha256":   bundle.BundleSHA256,
		"omitted_count":   len(bundle.Manifest.Omitted),
		"omitted_bytes":   omittedBytes,
		"reproducibility": bundle.Manifest.Reproducibility,
	})
	metrics.RecordPlanExport("zip", bundle.Manifest.Reproducibility)
	metrics.RecordExportOmittedBytes(omittedBytes)
}

// verifyReproduction performs a static verification of the generated bundle:
// required files, manifest hashes, checksum manifest and artifact mapping.
// It deliberately does not build or run a container; the response says so.
func (r *planRuntime) verifyReproduction(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	events := r.loadPlanEvents(plan.ID)
	bundle, err := buildPlanBundle(plan, events, exportLimitsFromEnv())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot build export bundle", "code": "verification_failed", "detail": err.Error()})
		return
	}
	checks := make([]gin.H, 0)
	passed := true
	fileData := map[string][]byte{}
	for _, file := range bundle.Files {
		fileData[file.Path] = file.Data
	}
	fileData["manifest.json"] = bundle.ManifestBytes
	fileData["checksums.sha256"] = bundle.Checksums
	required := []string{"README.md", "manifest.json", "checksums.sha256", "plan.json", "events.jsonl", "environment/requirements.lock", "environment/system-packages.txt", "environment/Dockerfile", "environment/run.sh"}
	for _, requiredPath := range required {
		_, exists := fileData[requiredPath]
		checks = append(checks, gin.H{"check": "required_file:" + requiredPath, "ok": exists})
		if !exists {
			passed = false
		}
	}
	for _, entry := range bundle.Manifest.Files {
		data, exists := fileData[entry.Path]
		ok := exists && int64(len(data)) == entry.Size && sha256Hex(data) == entry.SHA256
		checks = append(checks, gin.H{"check": "sha256:" + entry.Path, "ok": ok})
		if !ok {
			passed = false
		}
	}
	checksumOK := true
	for _, line := range strings.Split(strings.TrimSpace(string(bundle.Checksums)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			checksumOK = false
			break
		}
		data, exists := fileData[parts[1]]
		if !exists || sha256Hex(data) != parts[0] {
			checksumOK = false
			break
		}
	}
	checks = append(checks, gin.H{"check": "checksums.sha256", "ok": checksumOK})
	if !checksumOK {
		passed = false
	}
	status := "passed"
	if !passed {
		status = "failed"
	} else if bundle.Manifest.Reproducibility == "partial" {
		status = "partial"
	}
	c.JSON(http.StatusOK, gin.H{
		"status":          status,
		"static_only":     true,
		"reproducibility": bundle.Manifest.Reproducibility,
		"checks":          checks,
		"missing":         bundle.Manifest.Missing,
		"omitted":         bundle.Manifest.Omitted,
		"bundle_sha256":   bundle.BundleSHA256,
		"note":            "Static verification only: checks file presence, sizes and SHA-256 hashes. It does not build or run the environment.",
	})
}
