package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/project"
	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// planSummary is the lightweight list representation. It intentionally excludes
// nodes, artifacts and base64 images so the plan library stays small.
type planSummary struct {
	ID              string             `json:"id"`
	Title           string             `json:"title"`
	IntentType      string             `json:"intent_type"`
	Status          string             `json:"status"`
	Outcome         string             `json:"outcome,omitempty"`
	SessionID       string             `json:"session_id,omitempty"`
	ProjectID       string             `json:"project_id,omitempty"`
	TotalNodes      int                `json:"total_nodes"`
	CompletedNodes  int                `json:"completed_nodes"`
	FailedNodes     int                `json:"failed_nodes"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	FinishedAt      *time.Time         `json:"finished_at,omitempty"`
	Paused          bool               `json:"paused,omitempty"`
	Archived        bool               `json:"archived"`
	RunTrigger      string             `json:"run_trigger,omitempty"`
	Usage           models.RunUsage    `json:"usage,omitempty"`
	Review          models.ReviewState `json:"review,omitempty"`
	CurrentRevision int                `json:"current_revision,omitempty"`
}

func planDisplayTitle(plan *models.PlanGraph) string {
	if plan == nil {
		return ""
	}
	if title := strings.TrimSpace(plan.Title); title != "" {
		return title
	}
	title := strings.TrimSpace(plan.UserIntent)
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60]) + "…"
	}
	return title
}

func summarizePlan(plan *models.PlanGraph) planSummary {
	return summarizePlanRecord(store.SummarizePlanRecord(plan))
}

func summarizePlanRecord(record store.PlanSummaryRecord) planSummary {
	return planSummary{
		ID:             record.ID,
		Title:          planRecordTitle(record),
		IntentType:     record.IntentType,
		Status:         record.Status,
		Outcome:        record.Outcome,
		SessionID:      record.SessionID,
		ProjectID:      record.ProjectID,
		TotalNodes:     record.TotalNodes,
		CompletedNodes: record.CompletedNodes,
		FailedNodes:    record.FailedNodes,
		CreatedAt:      record.CreatedAt,
		UpdatedAt:      record.UpdatedAt,
		FinishedAt:     record.FinishedAt,
		Paused:         record.Paused,
		Archived:       record.Archived,
		RunTrigger:     record.RunTrigger,
		Usage:          record.Usage,
		Review:         record.Review,
	}
}

// planBudgetStillExceeded reports whether any configured limit is still at or
// over 100%. Used when a budget is raised so a paused plan can resume.
func validatePlanBudgetPatch(patch *planBudgetPatch) error {
	if patch == nil {
		return nil
	}
	if patch.MaxCostUSD != nil && *patch.MaxCostUSD < 0 {
		return fmt.Errorf("max_cost_usd must be non-negative")
	}
	if patch.MaxTokens != nil && *patch.MaxTokens < 0 {
		return fmt.Errorf("max_tokens must be non-negative")
	}
	if patch.MaxSandboxSeconds != nil && *patch.MaxSandboxSeconds < 0 {
		return fmt.Errorf("max_sandbox_seconds must be non-negative")
	}
	if patch.MaxDownloadBytes != nil && *patch.MaxDownloadBytes < 0 {
		return fmt.Errorf("max_download_bytes must be non-negative")
	}
	if patch.Policy != nil {
		policy := strings.ToLower(strings.TrimSpace(*patch.Policy))
		if policy != "soft" && policy != "hard" {
			return fmt.Errorf("policy must be soft or hard")
		}
	}
	return nil
}

func planBudgetStillExceeded(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	if plan.Budget.MaxCostUSD > 0 && plan.Usage.LLMCostUSD >= plan.Budget.MaxCostUSD {
		return true
	}
	if plan.Budget.MaxTokens > 0 && plan.Usage.LLMPromptTokens+plan.Usage.LLMCompletionTokens >= plan.Budget.MaxTokens {
		return true
	}
	if plan.Budget.MaxSandboxSeconds > 0 && plan.Usage.SandboxSeconds >= plan.Budget.MaxSandboxSeconds {
		return true
	}
	return false
}

func planRecordTitle(record store.PlanSummaryRecord) string {
	if title := strings.TrimSpace(record.Title); title != "" {
		return title
	}
	title := strings.TrimSpace(record.UserIntent)
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60]) + "…"
	}
	return title
}

type planListCursor struct {
	SortKey int64  `json:"s"`
	ID      string `json:"id"`
}

func encodePlanCursor(sortKey int64, id string) string {
	raw, _ := json.Marshal(planListCursor{SortKey: sortKey, ID: id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePlanCursor(value string) (planListCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return planListCursor{}, false
	}
	var cursor planListCursor
	if err := json.Unmarshal(raw, &cursor); err != nil || cursor.ID == "" {
		return planListCursor{}, false
	}
	return cursor, true
}

type planListFilters struct {
	Query        string
	Statuses     map[string]bool
	IntentType   string
	ProjectID    string
	SessionID    string
	Archived     string
	Sort         string
	Limit        int
	Cursor       planListCursor
	HasCursor    bool
	LegacyOffset int
}

func parsePlanListFilters(c *gin.Context) planListFilters {
	filters := planListFilters{
		Query:      strings.ToLower(strings.TrimSpace(c.Query("q"))),
		IntentType: strings.TrimSpace(c.Query("intent_type")),
		ProjectID:  strings.TrimSpace(c.Query("project_id")),
		SessionID:  strings.TrimSpace(c.Query("session_id")),
		Archived:   strings.ToLower(strings.TrimSpace(c.DefaultQuery("archived", "false"))),
		Sort:       strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort", "updated_desc"))),
		Limit:      20,
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			filters.Limit = parsed
		}
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		filters.Statuses = map[string]bool{}
		for _, part := range strings.Split(raw, ",") {
			if value := strings.TrimSpace(part); value != "" {
				filters.Statuses[value] = true
			}
		}
	}
	if filters.Archived != "true" && filters.Archived != "all" {
		filters.Archived = "false"
	}
	switch filters.Sort {
	case "created_desc", "cost_desc":
	default:
		filters.Sort = "updated_desc"
	}
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		if cursor, ok := decodePlanCursor(raw); ok {
			filters.Cursor = cursor
			filters.HasCursor = true
		} else if offset, err := strconv.Atoi(raw); err == nil && offset >= 0 {
			// Legacy clients sent a numeric offset. Keep them working until the
			// next major API version by converting it to an offset cursor.
			filters.LegacyOffset = offset
		}
	}
	return filters
}

func planSortKey(record store.PlanSummaryRecord, sortBy string) int64 {
	switch sortBy {
	case "created_desc":
		return record.CreatedAt.UnixNano()
	case "cost_desc":
		return int64(record.CostUSD * 1e6)
	default:
		return record.UpdatedAt.UnixNano()
	}
}

func planMatchesFilters(record store.PlanSummaryRecord, filters planListFilters) bool {
	if filters.Statuses != nil && !filters.Statuses[record.Status] {
		return false
	}
	if filters.IntentType != "" && !strings.EqualFold(record.IntentType, filters.IntentType) {
		return false
	}
	if filters.ProjectID != "" {
		if strings.EqualFold(filters.ProjectID, "inbox") {
			if strings.TrimSpace(record.ProjectID) != "" {
				return false
			}
		} else if record.ProjectID != filters.ProjectID {
			return false
		}
	}
	if filters.SessionID != "" && record.SessionID != filters.SessionID {
		return false
	}
	switch filters.Archived {
	case "true":
		if !record.Archived {
			return false
		}
	case "all":
	default:
		if record.Archived {
			return false
		}
	}
	if filters.Query != "" {
		haystack := strings.ToLower(record.Title + "\n" + record.UserIntent)
		if !strings.Contains(haystack, filters.Query) {
			return false
		}
	}
	return true
}

func planAfterCursor(record store.PlanSummaryRecord, filters planListFilters) bool {
	if !filters.HasCursor {
		return true
	}
	key := planSortKey(record, filters.Sort)
	if key < filters.Cursor.SortKey {
		return true
	}
	if key > filters.Cursor.SortKey {
		return false
	}
	return record.ID < filters.Cursor.ID
}

// listPlans serves the plan library with owner scoping, search/filter and a
// keyset cursor that stays stable while new plans are created.
func (r *planRuntime) listPlans(c *gin.Context) {
	userID := resolveUserID(c)
	filters := parsePlanListFilters(c)
	var records []store.PlanSummaryRecord
	if source, ok := r.store.(store.PlanSummarySource); ok {
		items, err := source.ListPlanSummaries()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot list plans", "code": "store_unavailable"})
			return
		}
		records = items
	} else {
		plans, err := r.store.ListPlans()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot list plans", "code": "store_unavailable"})
			return
		}
		records = make([]store.PlanSummaryRecord, 0, len(plans))
		for _, plan := range plans {
			records = append(records, store.SummarizePlanRecord(plan))
		}
	}

	owned := make([]store.PlanSummaryRecord, 0, len(records))
	for _, record := range records {
		if record.OwnerID != userID || !planMatchesFilters(record, filters) {
			continue
		}
		owned = append(owned, record)
	}
	sort.Slice(owned, func(i, j int) bool {
		left := planSortKey(owned[i], filters.Sort)
		right := planSortKey(owned[j], filters.Sort)
		if left == right {
			return owned[i].ID > owned[j].ID
		}
		return left > right
	})

	total := len(owned)
	start := 0
	if filters.HasCursor {
		for start < len(owned) && !planAfterCursor(owned[start], filters) {
			start++
		}
	} else if filters.LegacyOffset > 0 {
		start = filters.LegacyOffset
	}
	if start > len(owned) {
		start = len(owned)
	}
	end := start + filters.Limit
	if end > len(owned) {
		end = len(owned)
	}
	items := make([]planSummary, 0, end-start)
	for _, record := range owned[start:end] {
		items = append(items, summarizePlanRecord(record))
	}
	nextCursor := ""
	if end < len(owned) {
		last := owned[end-1]
		nextCursor = encodePlanCursor(planSortKey(last, filters.Sort), last.ID)
	}
	c.JSON(http.StatusOK, gin.H{"plans": items, "total": total, "next_cursor": nextCursor})
}

type projectPayload struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Archived    *bool   `json:"archived"`
}

func (r *planRuntime) listProjects(c *gin.Context) {
	userID := resolveUserID(c)
	includeArchived := strings.EqualFold(strings.TrimSpace(c.Query("include_archived")), "true") ||
		strings.EqualFold(strings.TrimSpace(c.Query("archived")), "all")
	projects, err := r.projects.List(userID, includeArchived)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot list projects", "code": "store_unavailable"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"projects": projects})
}

func (r *planRuntime) createProject(c *gin.Context) {
	userID := resolveUserID(c)
	var payload projectPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project payload", "code": "invalid_project"})
		return
	}
	name := ""
	if payload.Name != nil {
		name = *payload.Name
	}
	description := ""
	if payload.Description != nil {
		description = *payload.Description
	}
	created, err := r.projects.Create(userID, name, description)
	if err != nil {
		status := http.StatusBadRequest
		if strings.Contains(err.Error(), "already exists") {
			status = http.StatusConflict
		}
		c.JSON(status, gin.H{"error": err.Error(), "code": "invalid_project"})
		return
	}
	recordAuditEvent("project_created", userID, created.ID, map[string]any{"name": created.Name})
	c.JSON(http.StatusOK, gin.H{"project": created})
}

func (r *planRuntime) updateProject(c *gin.Context) {
	userID := resolveUserID(c)
	var payload projectPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid project payload", "code": "invalid_project"})
		return
	}
	updated, err := r.projects.Update(userID, c.Param("id"), project.Patch{
		Name:        payload.Name,
		Description: payload.Description,
		Archived:    payload.Archived,
	})
	if err != nil {
		if err == project.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "project not found", "code": "project_not_found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_project"})
		return
	}
	recordAuditEvent("project_updated", userID, updated.ID, map[string]any{"name": updated.Name, "archived": updated.Archived})
	c.JSON(http.StatusOK, gin.H{"project": updated})
}

func (r *planRuntime) deleteProject(c *gin.Context) {
	userID := resolveUserID(c)
	projectID := strings.TrimSpace(c.Param("id"))
	if _, err := r.projects.Get(userID, projectID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found", "code": "project_not_found"})
		return
	}
	count, err := r.countPlansInProject(userID, projectID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot inspect project plans", "code": "store_unavailable"})
		return
	}
	if count > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error": "project still contains plans; move or archive them before deleting",
			"code":  "project_not_empty",
			"count": count,
		})
		return
	}
	if err := r.projects.Delete(userID, projectID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "project not found", "code": "project_not_found"})
		return
	}
	recordAuditEvent("project_deleted", userID, projectID, nil)
	c.JSON(http.StatusOK, gin.H{"deleted": true, "project_id": projectID})
}

func (r *planRuntime) countPlansInProject(userID, projectID string) (int, error) {
	if source, ok := r.store.(store.PlanSummarySource); ok {
		records, err := source.ListPlanSummaries()
		if err != nil {
			return 0, err
		}
		count := 0
		for _, record := range records {
			if record.OwnerID == userID && record.ProjectID == projectID {
				count++
			}
		}
		return count, nil
	}
	plans, err := r.store.ListPlans()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, plan := range plans {
		if plan.OwnerID == userID && plan.ProjectID == projectID {
			count++
		}
	}
	return count, nil
}

type planBudgetPatch struct {
	MaxCostUSD        *float64 `json:"max_cost_usd"`
	MaxTokens         *int     `json:"max_tokens"`
	MaxSandboxSeconds *float64 `json:"max_sandbox_seconds"`
	MaxDownloadBytes  *int64   `json:"max_download_bytes"`
	Policy            *string  `json:"policy"`
}

type planPatchPayload struct {
	Title     *string          `json:"title"`
	ProjectID *string          `json:"project_id"`
	Archived  *bool            `json:"archived"`
	Budget    *planBudgetPatch `json:"budget,omitempty"`
}

func (r *planRuntime) patchPlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	var payload planPatchPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid plan payload", "code": "invalid_plan"})
		return
	}
	userID := resolveUserID(c)
	projectID := ""
	if payload.ProjectID != nil {
		projectID = strings.TrimSpace(*payload.ProjectID)
		if projectID != "" {
			if _, err := r.projects.Get(userID, projectID); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "project not found", "code": "project_not_found"})
				return
			}
		}
	}
	title := ""
	if payload.Title != nil {
		title = strings.TrimSpace(*payload.Title)
		if len([]rune(title)) > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "title is longer than 200 characters", "code": "invalid_plan"})
			return
		}
	}
	if payload.Budget != nil {
		if err := validatePlanBudgetPatch(payload.Budget); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "invalid_budget"})
			return
		}
	}
	now := time.Now().UTC()
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		if payload.Title != nil {
			current.Title = title
		}
		if payload.ProjectID != nil {
			current.ProjectID = projectID
		}
		if payload.Archived != nil {
			if *payload.Archived && !current.Archived {
				archivedAt := now
				current.ArchivedAt = &archivedAt
			}
			if !*payload.Archived {
				current.ArchivedAt = nil
			}
			current.Archived = *payload.Archived
		}
		if payload.Budget != nil {
			budgetPatch := payload.Budget
			if budgetPatch.MaxCostUSD != nil {
				current.Budget.MaxCostUSD = *budgetPatch.MaxCostUSD
			}
			if budgetPatch.MaxTokens != nil {
				current.Budget.MaxTokens = *budgetPatch.MaxTokens
			}
			if budgetPatch.MaxSandboxSeconds != nil {
				current.Budget.MaxSandboxSeconds = *budgetPatch.MaxSandboxSeconds
			}
			if budgetPatch.MaxDownloadBytes != nil {
				current.Budget.MaxDownloadBytes = *budgetPatch.MaxDownloadBytes
			}
			if budgetPatch.Policy != nil {
				current.Budget.Policy = strings.ToLower(strings.TrimSpace(*budgetPatch.Policy))
			}
			if current.Paused && current.Usage.BudgetStatus == "exceeded" {
				// Raising a budget is the documented way to recover a soft-paused
				// plan; clear the pause only when the new limits actually allow it.
				if !planBudgetStillExceeded(current) {
					current.Paused = false
					current.PausedAt = nil
					current.Usage.BudgetStatus = "ok"
					current.Usage.BudgetWarnings = nil
				}
			}
		}
		current.UpdatedAt = now
		return nil
	}); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found", "code": "plan_not_found"})
		return
	}
	detail := map[string]any{}
	if payload.Title != nil {
		detail["title"] = title
		recordAuditEvent("plan_renamed", userID, plan.ID, detail)
	}
	if payload.ProjectID != nil {
		detail = map[string]any{"project_id": projectID}
		recordAuditEvent("plan_moved", userID, plan.ID, detail)
	}
	if payload.Archived != nil {
		detail = map[string]any{"archived": *payload.Archived}
		action := "plan_unarchived"
		if *payload.Archived {
			action = "plan_archived"
		}
		recordAuditEvent(action, userID, plan.ID, detail)
	}
	if payload.Budget != nil {
		recordAuditEvent("plan_budget_updated", userID, plan.ID, map[string]any{"budget": payload.Budget})
	}
	updated, err := r.store.GetPlan(plan.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found", "code": "plan_not_found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"plan": summarizePlan(updated)})
}

// duplicatePlan creates an editable copy of a plan. Execution state, artifacts
// and usage are reset so the copy can be re-run independently.
func (r *planRuntime) duplicatePlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	userID := resolveUserID(c)
	now := time.Now().UTC()
	copyID := uuid.NewString()
	duplicate := &models.PlanGraph{
		ID:            copyID,
		Title:         duplicateTitle(planDisplayTitle(plan)),
		ProjectID:     plan.ProjectID,
		UserIntent:    plan.UserIntent,
		IntentType:    plan.IntentType,
		Status:        models.StatusPending,
		Outcome:       "",
		OwnerID:       userID,
		SessionID:     plan.SessionID,
		TraceID:       "trace_" + strings.ReplaceAll(uuid.NewString(), "-", ""),
		Approval:      models.ApprovalState{Required: plan.Approval.Required, Status: "not_required"},
		Review:        plan.Review,
		Budget:        plan.Budget,
		Context:       plan.Context,
		RuntimePolicy: plan.RuntimePolicy,
		Nodes:         duplicateNodes(plan.Nodes),
		Edges:         duplicateEdges(plan.Edges),
		Artifacts:     map[string]models.Artifact{},
		ModelSnapshot: plan.ModelSnapshot,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if duplicate.Approval.Required {
		duplicate.Approval.Status = "pending"
		duplicate.Status = models.StatusAwaitingApproval
	}
	// A copy of a review-gated plan is gated too: duplicating must never be a
	// way to skip the review that the source plan still owes.
	if plan.Review.Required {
		duplicate.Review = models.ReviewState{
			Required:     true,
			Status:       models.ReviewPending,
			Reason:       plan.Review.Reason,
			Risk:         plan.Review.Risk,
			EstimatedUSD: plan.Review.EstimatedUSD,
		}
		if !duplicate.Approval.Required {
			duplicate.Status = models.StatusAwaitingReview
		}
	}
	refreshPlanMeta(duplicate)
	// A copy starts with its own revision 1 so its edit history is independent.
	if created, revErr := r.revisionStore().Create(&models.PlanRevision{
		PlanID:    duplicate.ID,
		Graph:     duplicate,
		Status:    models.RevisionDraft,
		CreatedBy: userID,
		Reason:    "duplicated from " + plan.ID,
	}, 0); revErr == nil && created != nil {
		duplicate.CurrentRevision = created.Revision
	}
	if err := r.store.SavePlan(duplicate); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot save duplicated plan", "code": "duplicate_failed"})
		return
	}
	recordAuditEvent("plan_duplicated", userID, duplicate.ID, map[string]any{"source_plan_id": plan.ID})
	_ = r.store.AppendEvent(duplicate.ID, models.PlanEvent{
		PlanID:    duplicate.ID,
		EventType: "plan_duplicated",
		Payload:   map[string]any{"source_plan_id": plan.ID, "actor": userID},
		Timestamp: now,
	})
	c.JSON(http.StatusOK, gin.H{"plan": summarizePlan(duplicate)})
}

func duplicateTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Untitled plan"
	}
	const prefix = "Copy of "
	if len([]rune(prefix+title)) > 200 {
		return string([]rune(prefix + title)[:200])
	}
	return prefix + title
}

func duplicateNodes(nodes []*models.TaskNode) []*models.TaskNode {
	out := make([]*models.TaskNode, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			out = append(out, nil)
			continue
		}
		copyNode := *node
		copyNode.Status = models.StatusPending
		copyNode.Outcome = ""
		copyNode.Result = ""
		copyNode.Code = ""
		copyNode.StructuredData = ""
		copyNode.ImageBase64 = ""
		copyNode.ImageURL = ""
		copyNode.Error = ""
		copyNode.Metadata = nil
		copyNode.Usage = nil
		copyNode.StartedAt = nil
		copyNode.FinishedAt = nil
		copyNode.LeaseOwner = ""
		copyNode.LeaseExpiresAt = nil
		copyNode.ExecutionID = ""
		copyNode.ExecutionEpoch = 0
		copyNode.RunCount = 0
		copyNode.Verification = nil
		copyNode.Inputs = cloneAnyMap(node.Inputs)
		copyNode.Dependencies = append([]string(nil), node.Dependencies...)
		copyNode.RequiredArtifacts = append([]string(nil), node.RequiredArtifacts...)
		copyNode.OutputArtifacts = append([]string(nil), node.OutputArtifacts...)
		copyNode.Contract.InputArtifacts = append([]string(nil), node.Contract.InputArtifacts...)
		copyNode.Contract.OutputArtifacts = append([]string(nil), node.Contract.OutputArtifacts...)
		copyNode.Contract.AllowedTools = append([]string(nil), node.Contract.AllowedTools...)
		out = append(out, &copyNode)
	}
	return out
}

func duplicateEdges(edges []*models.TaskEdge) []*models.TaskEdge {
	out := make([]*models.TaskEdge, 0, len(edges))
	for _, edge := range edges {
		if edge == nil {
			out = append(out, nil)
			continue
		}
		copyEdge := *edge
		out = append(out, &copyEdge)
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

// refreshPlanMeta recomputes the lightweight graph counters after a copy or
// status reset. It mirrors scheduler.fillMeta without importing the scheduler.
func refreshPlanMeta(plan *models.PlanGraph) {
	if plan == nil {
		return
	}
	completed, failed, blocked, inProgress, ready := 0, 0, 0, 0, 0
	for _, node := range plan.Nodes {
		if node == nil {
			continue
		}
		switch node.Status {
		case models.StatusCompleted:
			completed++
		case models.StatusFailed:
			failed++
		case models.StatusBlocked:
			blocked++
		case models.StatusInProgress:
			inProgress++
		case models.StatusReady:
			ready++
		}
	}
	plan.Meta.TotalNodes = len(plan.Nodes)
	plan.Meta.CompletedNodes = completed
	plan.Meta.FailedNodes = failed
	plan.Meta.BlockedNodes = blocked
	plan.Meta.InProgressNodes = inProgress
	plan.Meta.ReadyNodes = ready
}
