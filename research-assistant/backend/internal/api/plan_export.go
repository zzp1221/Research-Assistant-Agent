package api

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"

	"github.com/gin-gonic/gin"
)

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
		r.scheduler.CleanupPlanSandboxes(c.Request.Context(), plan.ID)
		deleted = append(deleted, plan.ID)
	}
	recordAuditEvent("session_deleted", userID, sessionID, map[string]any{"plans": len(deleted)})
	c.JSON(http.StatusOK, gin.H{"deleted": len(deleted), "plan_ids": deleted, "session_id": sessionID})
}

// exportPlan produces a portable reproduction package. Markdown is the default,
// JSON keeps machine-readable detail, and ZIP bundles the markdown plus the
// plan snapshot, events and artifact blobs.
func (r *planRuntime) exportPlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	format := strings.ToLower(strings.TrimSpace(c.DefaultQuery("format", "md")))
	baseName := "plan-" + plan.ID
	switch format {
	case "json":
		payload, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot encode plan", "code": "export_failed"})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", baseName+".json"))
		c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
	case "md", "markdown":
		markdown := renderPlanMarkdown(plan, r.loadPlanEvents(plan.ID))
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", baseName+".md"))
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

func renderPlanMarkdown(plan *models.PlanGraph, events []models.PlanEvent) string {
	var builder strings.Builder
	builder.WriteString("# " + strings.TrimSpace(plan.UserIntent) + "\n\n")
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
	builder.WriteString("| # | Node | Type | Agent | Status | Duration |\n|---|---|---|---|---|---|\n")
	for index, node := range plan.Nodes {
		if node == nil {
			continue
		}
		duration := ""
		if node.Usage != nil {
			duration = fmt.Sprintf("%.1fs", node.Usage.DurationSeconds)
		}
		fmt.Fprintf(&builder, "| %d | %s | %s | %s | %s | %s |\n", index+1, node.Name, node.Type, node.AssignedTo, node.Status, duration)
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
			result := node.Result
			if len(result) > 8000 {
				result = result[:8000] + "\n...[truncated]"
			}
			builder.WriteString("```\n" + result + "\n```\n\n")
		}
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

func (r *planRuntime) writePlanZip(c *gin.Context, plan *models.PlanGraph, filename string) {
	events := r.loadPlanEvents(plan.ID)
	buffer := &zipBuffer{}
	zipWriter := zip.NewWriter(buffer)

	add := func(name string, data []byte) error {
		writer, err := zipWriter.Create(name)
		if err != nil {
			return err
		}
		_, err = writer.Write(data)
		return err
	}
	markdown := renderPlanMarkdown(plan, events)
	_ = add("README.md", []byte(markdown))
	if encoded, err := json.MarshalIndent(plan, "", "  "); err == nil {
		_ = add("plan.json", encoded)
	}
	var eventBuilder strings.Builder
	for _, event := range events {
		if encoded, err := json.Marshal(event); err == nil {
			eventBuilder.Write(encoded)
			eventBuilder.WriteByte('\n')
		}
	}
	_ = add("events.jsonl", []byte(eventBuilder.String()))

	written := 0
	for key, artifact := range plan.Artifacts {
		if strings.TrimSpace(artifact.BlobRef) == "" {
			continue
		}
		data, err := planner.LoadBlobForResponse(artifact.BlobRef)
		if err != nil || len(data) == 0 {
			continue
		}
		extension := ".bin"
		if strings.Contains(strings.ToLower(artifact.Type), "image") {
			extension = ".png"
		} else if strings.Contains(strings.ToLower(artifact.Type), "json") {
			extension = ".json"
		} else if strings.Contains(strings.ToLower(artifact.Type), "report") {
			extension = ".md"
		}
		name := path.Join("artifacts", sanitizeZipName(key)+extension)
		if err := add(name, data); err == nil {
			written++
		}
	}
	if written == 0 {
		_ = add("artifacts/README.txt", []byte("Artifacts are referenced by /api/artifacts/<ref>; none had local blobs at export time.\n"))
	}
	if err := zipWriter.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot finalize zip", "code": "export_failed"})
		return
	}
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Data(http.StatusOK, "application/zip", buffer.Bytes())
}

func sanitizeZipName(value string) string {
	clean := path.Base(strings.ReplaceAll(strings.TrimSpace(value), "\\", "/"))
	if clean == "" || clean == "." || clean == "/" {
		return "artifact"
	}
	return clean
}

type zipBuffer struct {
	data []byte
}

func (b *zipBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

func (b *zipBuffer) Bytes() []byte {
	return b.data
}
