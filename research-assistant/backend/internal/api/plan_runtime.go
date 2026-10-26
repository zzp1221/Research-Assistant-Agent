package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/blobstore"
	"research-assistant-backend/internal/events"
	intentpkg "research-assistant-backend/internal/intent"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/sandbox"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"

	"github.com/gin-gonic/gin"
)

const planStreamEventName = "plan_event"

type planRuntime struct {
	planner        *planner.Planner
	store          store.PlanStore
	scheduler      *scheduler.Scheduler
	events         *events.Bus
	intentResolver *intentpkg.Resolver

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func newPlanRuntime(p *planner.Planner, planStore store.PlanStore, runner *scheduler.Scheduler, eventBus *events.Bus) *planRuntime {
	return &planRuntime{
		planner:        p,
		store:          planStore,
		scheduler:      runner,
		events:         eventBus,
		intentResolver: intentpkg.NewResolverFromEnv(),
		running:        map[string]context.CancelFunc{},
	}
}

// RegisterPlanRuntimeRoutes wires the graph planner to the real scheduler and
// agents used by the production API.
func RegisterPlanRuntimeRoutes(
	apiGroup *gin.RouterGroup,
	p *planner.Planner,
	librarian scheduler.AgentRunner,
	data scheduler.AgentRunner,
	coder scheduler.AgentRunner,
	specialized ...scheduler.AgentRunner,
) {
	var planStore store.PlanStore = store.NewMemoryPlanStore()
	if path := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); path != "" {
		fileStore, err := store.NewFilePlanStore(path)
		if err != nil {
			log.Printf("plan store init failed, falling back to memory: %v", err)
		} else {
			planStore = fileStore
			if err := store.RecoverInterruptedPlans(planStore); err != nil {
				log.Printf("plan store recovery failed: %v", err)
			}
		}
	}
	eventBus := events.NewBus()
	executor := scheduler.NewRoutedTaskExecutor(librarian, data, coder, specialized...)
	runner := scheduler.NewScheduler(planStore, executor, eventBus, 2)
	runner.SetFinalizer(&planner.PlanFinalizer{})
	// Plan-level sandbox cleanup: every terminal plan releases its persistent
	// runtimes so failed/canceled plans cannot leak containers.
	sandboxURL := strings.TrimSpace(os.Getenv("SANDBOX_URL"))
	if sandboxURL == "" {
		sandboxURL = "http://localhost:8082"
	}
	sandboxClient := sandbox.NewSandboxClient(sandboxURL)

	// Content-addressed artifact store keeps PlanGraph snapshots small.
	blobRoot := strings.TrimSpace(os.Getenv("BLOB_STORE_PATH"))
	if blobRoot == "" {
		if planPath := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); planPath != "" {
			blobRoot = planPath + ".blobs"
		} else {
			blobRoot = filepath.Join(os.TempDir(), "research-assistant-blobs")
		}
	}
	if blobStore, err := blobstore.New(blobRoot); err != nil {
		log.Printf("blob store init failed: %v", err)
	} else {
		runner.SetBlobStore(blobStore)
		planner.SetBlobLoader(blobStore.Get)
		registerArtifactRoutes(apiGroup, blobStore)
	}

	runner.SetSandboxCleanup(func(ctx context.Context, plan *models.PlanGraph) {
		if plan == nil || strings.TrimSpace(plan.ID) == "" {
			return
		}
		removed, err := sandboxClient.CleanupPlanSandboxes(ctx, plan.ID)
		if err != nil {
			log.Printf("[sandbox] cleanup plan %s failed: %v", plan.ID, err)
			return
		}
		if removed > 0 {
			log.Printf("[sandbox] cleanup plan %s removed %d container(s)", plan.ID, removed)
		}
	})
	runtime := newPlanRuntime(p, planStore, runner, eventBus)
	runtime.register(apiGroup)
}

func (r *planRuntime) register(apiGroup *gin.RouterGroup) {
	apiGroup.POST("/plan", r.createPlan)
	apiGroup.GET("/plans", r.listPlans)
	apiGroup.DELETE("/plans/:id", r.deletePlan)
	apiGroup.GET("/sessions", r.listSessions)
	apiGroup.GET("/plans/:id", r.getPlan)
	apiGroup.GET("/plans/:id/events", r.getPlanEvents)
	apiGroup.POST("/plans/:id/execute", r.executePlan)
	apiGroup.POST("/plans/:id/cancel", r.cancelPlan)
	apiGroup.POST("/plans/:id/approve", r.approvePlan)
	apiGroup.GET("/plans/:id/runtime-actions", r.getRuntimeActions)
	apiGroup.POST("/plans/:id/runtime-actions/:actionId/decision", r.decideRuntimeAction)
	apiGroup.POST("/plans/:id/tasks/:taskId/retry", r.retryTask)
	apiGroup.POST("/plans/:id/tasks/:taskId/reassign", r.reassignTask)
	apiGroup.POST("/plans/:id/tasks/:taskId/execute", r.executeTask)
	apiGroup.GET("/plans/:id/stream-ticket", r.streamTicket)
	apiGroup.GET("/plans/:id/stream", r.streamPlanEvents)
}

func (r *planRuntime) createPlan(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var payload RequestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := resolveUserID(c)
	sessionID := resolveSessionID(c)
	runtimePolicy, policyErr := buildRuntimePolicy(payload, userID)
	if policyErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": policyErr.Error()})
		return
	}
	ruleContext := buildRuleIntentContext(payload.Intent)
	intentContext := r.intentResolver.Resolve(c.Request.Context(), payload.Intent, ruleContext)
	attachments, err := resolvePlanUploads(userID, payload.Attachments)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if len(attachments) > 0 {
		if shouldRouteUploadedCustomBenchmark(intentContext.IntentType, payload.Intent, attachments) {
			intentContext.IntentType = "Custom_Benchmark"
			intentContext.Entities["needs_custom_benchmark"] = true
			removeAttachmentTextExcerpts(attachments)
		}
		intentContext.Entities["uploaded_files"] = attachments
		intentContext.Metadata["attachment_count"] = len(attachments)
		if intentContext.IntentType == "AutoResearch" && hasOnlyAutoResearchConfigAttachments(attachments) {
			intentContext.Entities["autoresearch_config_only"] = true
			delete(intentContext.Entities, "needs_dataset_research")
			delete(intentContext.Entities, "research_domain")
			delete(intentContext.Entities, "experiment_adapter")
		}
	}
	researchContext, contextErr := buildResearchContext(userID, sessionID, payload.Intent, attachments, intentContext.IntentType)
	if contextErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": contextErr.Error(),
			"missing_requirements": []models.MissingRequirement{{
				Kind:        "paper_metadata",
				Name:        "paper",
				Detail:      contextErr.Error(),
				Remediation: "请提供可提取文本的论文 PDF，或在请求中明确给出论文标题。",
			}},
		})
		return
	}
	if researchContext.Paper != nil {
		intentContext.Entities["paper_metadata"] = paperMetadataMap(researchContext.Paper)
		existingTitle, _ := intentContext.Entities["paper_title"].(string)
		if strings.TrimSpace(existingTitle) == "" {
			intentContext.Entities["paper_title"] = researchContext.Paper.Title
		}
		if existingQuery, _ := intentContext.Entities["paper_search_query"].(string); strings.TrimSpace(existingQuery) == "" {
			intentContext.Entities["paper_search_query"] = researchContext.Paper.Title
		}
	}
	plan, err := r.planner.BuildPlan(c.Request.Context(), intentContext)
	if err != nil {
		log.Printf("Error generating plan graph: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate plan"})
		return
	}
	if len(attachments) > 0 {
		plan.Context = researchContext
		if err := planner.ValidatePlanContextCoverage(plan, researchContext); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "missing_requirements": plan.MissingRequirements})
			return
		}
	}
	plan.RuntimePolicy = runtimePolicy
	configurePlanGovernance(plan, intentContext, userID, sessionID)
	if err := r.store.SavePlan(plan); err != nil {
		log.Printf("Error saving plan graph: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save plan"})
		return
	}
	_ = r.store.AppendEvent(plan.ID, models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_created",
		Payload: map[string]any{
			"actor":       userID,
			"session_id":  sessionID,
			"intent_type": plan.IntentType,
		},
		Timestamp: time.Now(),
	})

	response := gin.H{
		"message":        "Plan generated successfully",
		"plan_graph":     plan,
		"intent_context": publicIntentContext(intentContext),
		"session_id":     sessionID,
		"anon_user_id":   userID,
		"user_id":        userID,
	}
	if clarification, ok := buildPlanClarification(intentContext.IntentType, payload.Intent); ok {
		response["clarification"] = clarification
	}
	c.JSON(http.StatusOK, response)
}

func (r *planRuntime) getPlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	hydratePlanForResponse(plan)
	c.JSON(http.StatusOK, gin.H{"plan_graph": plan})
}

// planSummary is the lightweight list representation. It intentionally excludes
// nodes, artifacts and base64 images so the plan library stays small.
type planSummary struct {
	ID             string     `json:"id"`
	Title          string     `json:"title"`
	IntentType     string     `json:"intent_type"`
	Status         string     `json:"status"`
	Outcome        string     `json:"outcome,omitempty"`
	SessionID      string     `json:"session_id,omitempty"`
	TotalNodes     int        `json:"total_nodes"`
	CompletedNodes int        `json:"completed_nodes"`
	FailedNodes    int        `json:"failed_nodes"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

func summarizePlan(plan *models.PlanGraph) planSummary {
	title := strings.TrimSpace(plan.UserIntent)
	if len([]rune(title)) > 60 {
		title = string([]rune(title)[:60]) + "…"
	}
	return planSummary{
		ID:             plan.ID,
		Title:          title,
		IntentType:     plan.IntentType,
		Status:         string(plan.Status),
		Outcome:        string(plan.Outcome),
		SessionID:      plan.SessionID,
		TotalNodes:     len(plan.Nodes),
		CompletedNodes: plan.Meta.CompletedNodes,
		FailedNodes:    plan.Meta.FailedNodes,
		CreatedAt:      plan.CreatedAt,
		UpdatedAt:      plan.UpdatedAt,
		FinishedAt:     plan.Usage.FinishedAt,
	}
}

func (r *planRuntime) listPlans(c *gin.Context) {
	userID := resolveUserID(c)
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}
	offset := 0
	if raw := strings.TrimSpace(c.Query("cursor")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	sessionFilter := strings.TrimSpace(c.Query("session_id"))
	all, err := r.store.ListPlans()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	owned := make([]*models.PlanGraph, 0, len(all))
	for _, plan := range all {
		if plan == nil || plan.OwnerID != userID {
			continue
		}
		if sessionFilter != "" && plan.SessionID != sessionFilter {
			continue
		}
		owned = append(owned, plan)
	}
	sort.Slice(owned, func(i, j int) bool {
		if owned[i].UpdatedAt.Equal(owned[j].UpdatedAt) {
			return owned[i].ID > owned[j].ID
		}
		return owned[i].UpdatedAt.After(owned[j].UpdatedAt)
	})
	start := offset
	if start > len(owned) {
		start = len(owned)
	}
	end := start + limit
	if end > len(owned) {
		end = len(owned)
	}
	items := make([]planSummary, 0, end-start)
	for _, plan := range owned[start:end] {
		items = append(items, summarizePlan(plan))
	}
	nextCursor := ""
	if end < len(owned) {
		nextCursor = strconv.Itoa(end)
	}
	c.JSON(http.StatusOK, gin.H{
		"plans":       items,
		"total":       len(owned),
		"next_cursor": nextCursor,
	})
}

func (r *planRuntime) deletePlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if plan.Status == models.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is running; cancel it before deleting"})
		return
	}
	if err := r.store.DeletePlan(plan.ID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	r.scheduler.CleanupPlanSandboxes(context.Background(), plan.ID)
	c.JSON(http.StatusOK, gin.H{"deleted": true, "plan_id": plan.ID})
}

func (r *planRuntime) listSessions(c *gin.Context) {
	userID := resolveUserID(c)
	all, err := r.store.ListPlans()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	type sessionSummary struct {
		SessionID  string    `json:"session_id"`
		PlanCount  int       `json:"plan_count"`
		LastActive time.Time `json:"last_active"`
	}
	byID := map[string]*sessionSummary{}
	for _, plan := range all {
		if plan == nil || plan.OwnerID != userID || strings.TrimSpace(plan.SessionID) == "" {
			continue
		}
		item, ok := byID[plan.SessionID]
		if !ok {
			item = &sessionSummary{SessionID: plan.SessionID}
			byID[plan.SessionID] = item
		}
		item.PlanCount++
		if plan.UpdatedAt.After(item.LastActive) {
			item.LastActive = plan.UpdatedAt
		}
	}
	items := make([]sessionSummary, 0, len(byID))
	for _, item := range byID {
		items = append(items, *item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].LastActive.After(items[j].LastActive) })
	c.JSON(http.StatusOK, gin.H{"sessions": items})
}

// hydratePlanForResponse restores large artifact payloads and node images from
// the content-addressed blob store for API consumers. The persisted PlanGraph
// keeps only references, so snapshots stay small while clients still receive
// the complete data they render.
func hydratePlanForResponse(plan *models.PlanGraph) {
	if plan == nil {
		return
	}
	for key, artifact := range plan.Artifacts {
		if strings.TrimSpace(artifact.Value) != "" || strings.TrimSpace(artifact.BlobRef) == "" {
			continue
		}
		if data, err := planner.LoadBlobForResponse(artifact.BlobRef); err == nil {
			if planner.IsImageBytes(data) {
				artifact.Value = base64.StdEncoding.EncodeToString(data)
			} else {
				artifact.Value = string(data)
			}
			plan.Artifacts[key] = artifact
		}
	}
	for _, node := range plan.Nodes {
		if node == nil || node.Metadata == nil {
			continue
		}
		ref, _ := node.Metadata["image_blob_ref"].(string)
		if strings.TrimSpace(ref) == "" {
			continue
		}
		node.ImageURL = "/api/artifacts/" + ref
		if strings.TrimSpace(node.ImageBase64) == "" {
			if data, err := planner.LoadBlobForResponse(ref); err == nil {
				node.ImageBase64 = base64.StdEncoding.EncodeToString(data)
			}
		}
	}
}

func registerArtifactRoutes(apiGroup *gin.RouterGroup, blobs *blobstore.Store) {
	apiGroup.GET("/artifacts/:ref", func(c *gin.Context) {
		ref := c.Param("ref")
		if blobs == nil || !blobs.Exists(ref) {
			c.JSON(http.StatusNotFound, gin.H{"error": "artifact not found"})
			return
		}
		path, err := blobs.Path(ref)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.File(path)
	})
}

func (r *planRuntime) getPlanEvents(c *gin.Context) {
	if _, ok := r.authorizedPlan(c); !ok {
		return
	}
	afterSeq := parseAfterSeq(c)
	events, err := r.store.ListEventsAfter(c.Param("id"), afterSeq)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events, "after_seq": afterSeq})
}

// parseAfterSeq reads the incremental replay cursor from ?after_seq= or the
// standard Last-Event-ID header.
func parseAfterSeq(c *gin.Context) int64 {
	if raw := strings.TrimSpace(c.Query("after_seq")); raw != "" {
		if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value > 0 {
			return value
		}
	}
	if raw := strings.TrimSpace(c.GetHeader("Last-Event-ID")); raw != "" {
		if value, err := strconv.ParseInt(raw, 10, 64); err == nil && value > 0 {
			return value
		}
	}
	return 0
}

func (r *planRuntime) streamTicket(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	ticket, expiresAt := issueStreamTicket(plan.ID, time.Now())
	c.JSON(http.StatusOK, gin.H{
		"ticket":     ticket,
		"expires_at": expiresAt,
		"expires_in": int(streamTicketTTL.Seconds()),
	})
}

func (r *planRuntime) executePlan(c *gin.Context) {
	planID := c.Param("id")
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if plan.Status != models.StatusPending && plan.Status != models.StatusReady {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("plan cannot be started from status %s", plan.Status)})
		return
	}

	r.mu.Lock()
	if _, exists := r.running[planID]; exists {
		r.mu.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "plan is already running"})
		return
	}
	// The execution budget is enforced inside the scheduler so approval waits can
	// be excluded from it; the context only carries cancellation.
	ctx, cancel := context.WithCancel(context.Background())
	r.running[planID] = cancel
	r.mu.Unlock()

	go func(runCtx context.Context, runCancel context.CancelFunc) {
		defer func() {
			runCancel()
			r.mu.Lock()
			delete(r.running, planID)
			r.mu.Unlock()
		}()

		if err := r.scheduler.ExecutePlan(runCtx, planID); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("plan execution failed plan_id=%s: %v", planID, err)
		}
	}(ctx, cancel)

	_ = r.store.AppendEvent(planID, models.PlanEvent{
		PlanID:    planID,
		EventType: "plan_execution_requested",
		Payload:   map[string]any{"actor": resolveUserID(c)},
		Timestamp: time.Now(),
	})
	c.JSON(http.StatusAccepted, gin.H{
		"message": "Plan execution started",
		"plan_id": planID,
	})
}

func (r *planRuntime) cancelPlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	r.mu.Lock()
	cancel := r.running[plan.ID]
	r.mu.Unlock()
	actor := resolveUserID(c)
	if err := r.scheduler.CancelPlanWithActor(plan.ID, "canceled by user", actor); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	if cancel != nil {
		cancel()
	}
	c.JSON(http.StatusOK, gin.H{"message": "Plan canceled", "plan_id": plan.ID})
}

func (r *planRuntime) approvePlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	now := time.Now()
	userID := resolveUserID(c)
	if err := r.store.UpdatePlan(plan.ID, func(current *models.PlanGraph) error {
		if current.Status != models.StatusAwaitingApproval {
			return fmt.Errorf("plan cannot be approved from status %s", current.Status)
		}
		if !current.Approval.Required {
			return fmt.Errorf("plan does not require approval")
		}
		if current.Approval.Status == "approved" {
			return fmt.Errorf("plan is already approved")
		}
		current.Approval.Status = "approved"
		current.Approval.ApprovedBy = userID
		current.Approval.ApprovedAt = &now
		current.Status = models.StatusPending
		current.UpdatedAt = now
		return nil
	}); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	_ = r.store.AppendEvent(plan.ID, models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_approved",
		Payload:   map[string]any{"actor": userID},
		Timestamp: now,
	})
	c.JSON(http.StatusOK, gin.H{"message": "Plan approved", "plan_id": plan.ID})
}

func (r *planRuntime) retryTask(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if task := findPlanTask(plan, c.Param("taskId")); task != nil && task.Status == models.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"error": "task is currently running; wait for termination before retrying"})
		return
	}
	if plan.Status == models.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is currently running; use a background retry after it terminates"})
		return
	}
	if err := r.scheduler.RetryTask(plan.ID, c.Param("taskId")); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Task reset for retry", "plan_id": plan.ID, "task_id": c.Param("taskId")})
}

func (r *planRuntime) reassignTask(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if task := findPlanTask(plan, c.Param("taskId")); task != nil && task.Status == models.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"error": "task is currently running; reassignment is disabled"})
		return
	}
	if plan.Status == models.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is currently running; reassignment is disabled"})
		return
	}
	var payload struct {
		AssignedTo string `json:"assigned_to" binding:"required"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := r.scheduler.ReassignTask(plan.ID, c.Param("taskId"), payload.AssignedTo); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Task reassigned", "plan_id": plan.ID, "task_id": c.Param("taskId"), "assigned_to": payload.AssignedTo})
}

type planTaskOutcome struct {
	result *models.TaskExecutionResult
	err    error
}

// executeTask runs one task belonging to a stored plan. The request body is
// intentionally ignored: task inputs and artifacts are loaded from the plan
// store so callers cannot inject host paths or bypass plan governance.
func (r *planRuntime) executeTask(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	task := findPlanTask(plan, c.Param("taskId"))
	if task == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "task not found"})
		return
	}
	if plan.Status == models.StatusCanceled {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is canceled"})
		return
	}
	if plan.Status == models.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is already running"})
		return
	}
	if plan.Approval.Required && plan.Approval.Status != "approved" {
		c.JSON(http.StatusConflict, gin.H{"error": "plan requires approval before task execution"})
		return
	}
	if task.Status == models.StatusInProgress {
		c.JSON(http.StatusConflict, gin.H{"error": "task is already running"})
		return
	}
	if plan.Budget.MaxTaskAttempts > 0 && plan.Usage.TaskAttempts >= plan.Budget.MaxTaskAttempts {
		c.JSON(http.StatusConflict, gin.H{"error": "task attempt budget exceeded"})
		return
	}
	if plan.Budget.MaxDurationSec > 0 && plan.Usage.StartedAt != nil &&
		time.Since(*plan.Usage.StartedAt) > time.Duration(plan.Budget.MaxDurationSec)*time.Second {
		c.JSON(http.StatusConflict, gin.H{"error": "plan duration budget exceeded"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	logChannel := make(chan string, 128)
	outcomeChannel := make(chan planTaskOutcome, 1)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	ctx = context.WithValue(ctx, "logChannel", logChannel)

	go func() {
		result, err := r.scheduler.ExecuteTask(ctx, plan.ID, task.ID)
		outcomeChannel <- planTaskOutcome{result: result, err: err}
	}()

	c.Stream(func(_ io.Writer) bool {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case line := <-logChannel:
				c.SSEvent("log", line)
				return true
			case <-ticker.C:
				c.SSEvent("heartbeat", "keep-alive")
				return true
			case outcome := <-outcomeChannel:
				for len(logChannel) > 0 {
					c.SSEvent("log", <-logChannel)
				}
				if outcome.err != nil {
					c.SSEvent("error", outcome.err.Error())
					return false
				}
				if outcome.result == nil {
					c.SSEvent("error", "task execution returned no result")
					return false
				}
				if outcome.result.Status == models.StatusFailed {
					c.SSEvent("error", chooseNonEmptyString(outcome.result.Error, "task failed"))
					return false
				}
				c.SSEvent("result", gin.H{
					"result":          outcome.result.Result,
					"code":            outcome.result.Code,
					"structured_data": outcome.result.StructuredData,
					"image_base64":    outcome.result.ImageBase64,
					"outputs": buildExecuteOutputs(&models.Task{
						Result:         outcome.result.Result,
						Code:           outcome.result.Code,
						StructuredData: outcome.result.StructuredData,
						ImageBase64:    outcome.result.ImageBase64,
					}),
				})
				return false
			case <-c.Request.Context().Done():
				cancel()
				return false
			}
		}
	})
}

func findPlanTask(plan *models.PlanGraph, taskID string) *models.TaskNode {
	if plan == nil {
		return nil
	}
	for _, task := range plan.Nodes {
		if task != nil && task.ID == taskID {
			return task
		}
	}
	return nil
}

func chooseNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (r *planRuntime) streamPlanEvents(c *gin.Context) {
	planID := c.Param("id")
	if _, ok := r.authorizedPlan(c); !ok {
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	subscription := r.events.Subscribe(planID)
	defer r.events.Unsubscribe(planID, subscription)

	// Incremental replay: only events after the client cursor are sent, so a
	// reconnect never re-transfers the entire plan history.
	lastSeq := parseAfterSeq(c)
	seen := map[string]struct{}{}
	replayUnseen := func() bool {
		history, err := r.store.ListEventsAfter(planID, lastSeq)
		if err != nil {
			writePlanSSE(c, "error", gin.H{"error": err.Error()})
			return false
		}
		for _, event := range history {
			fingerprint := planEventFingerprint(event)
			if _, duplicate := seen[fingerprint]; duplicate {
				continue
			}
			seen[fingerprint] = struct{}{}
			if event.Seq > lastSeq {
				lastSeq = event.Seq
			}
			if !writePlanSSEWithID(c, planStreamEventName, event.Seq, event) || isTerminalPlanEvent(event) {
				return false
			}
		}
		return true
	}
	if !replayUnseen() {
		return
	}

	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case event, ok := <-subscription:
			if !ok {
				return
			}
			fingerprint := planEventFingerprint(event)
			if _, duplicate := seen[fingerprint]; duplicate {
				continue
			}
			seen[fingerprint] = struct{}{}
			if !writePlanSSE(c, planStreamEventName, event) {
				return
			}
			if isTerminalPlanEvent(event) {
				return
			}
		case <-heartbeat.C:
			// The in-memory bus is intentionally non-blocking. Replaying the durable
			// history heals any live event dropped for a slow SSE subscriber.
			if !replayUnseen() {
				return
			}
			if !writePlanSSE(c, "heartbeat", "keep-alive") {
				return
			}
		case <-c.Request.Context().Done():
			return
		}
	}
}

func (r *planRuntime) authorizedPlan(c *gin.Context) (*models.PlanGraph, bool) {
	plan, err := r.store.GetPlan(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "plan not found"})
		return nil, false
	}
	// Ownership is mandatory: a plan without an owner is not readable by any
	// client, and a mismatching principal is always rejected.
	if plan.OwnerID == "" || plan.OwnerID != resolveUserID(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "plan belongs to another user"})
		return nil, false
	}
	return plan, true
}

func configurePlanGovernance(plan *models.PlanGraph, intent models.IntentContext, userID, sessionID string) {
	plan.OwnerID = userID
	plan.SessionID = sessionID
	plan.Budget.MaxTaskAttempts = positiveEnvInt("PLAN_MAX_TASK_ATTEMPTS", plan.Budget.MaxTaskAttempts)
	plan.Budget.MaxDurationSec = positiveEnvInt("PLAN_MAX_DURATION_SECONDS", plan.Budget.MaxDurationSec)
	requiresApproval := strings.EqualFold(strings.TrimSpace(os.Getenv("REQUIRE_PLAN_APPROVAL")), "true") ||
		strings.EqualFold(strings.TrimSpace(fmt.Sprint(intent.Constraints["reproduction_mode"])), "full") ||
		planRequestsFullReproduction(plan)
	plan.Approval.Required = requiresApproval
	if requiresApproval {
		plan.Approval.Status = "pending"
		plan.Approval.Reason = "high-risk or full reproduction plan"
		plan.Status = models.StatusAwaitingApproval
	} else {
		plan.Approval.Status = "not_required"
	}
}

func positiveEnvInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func planRequestsFullReproduction(plan *models.PlanGraph) bool {
	if plan == nil {
		return false
	}
	for _, node := range plan.Nodes {
		if node == nil || node.Inputs == nil {
			continue
		}
		if mode, ok := node.Inputs["requested_reproduction_mode"]; ok &&
			strings.EqualFold(strings.TrimSpace(fmt.Sprint(mode)), "full") {
			return true
		}
		if requested, ok := node.Inputs["full_reproduction_requested"].(bool); ok && requested {
			return true
		}
	}
	return false
}

func planTimeout(plan *models.PlanGraph) time.Duration {
	if plan != nil && plan.Budget.MaxDurationSec > 0 {
		return time.Duration(plan.Budget.MaxDurationSec) * time.Second
	}
	return 35 * time.Minute
}

func buildRuleIntentContext(rawIntent string) models.IntentContext {
	intentType := DetectIntentType(rawIntent)
	context := models.IntentContext{
		RawIntent:   rawIntent,
		IntentType:  intentType,
		Entities:    map[string]any{},
		Constraints: map[string]any{},
		Metadata: map[string]any{
			"normalized_intent": strings.ToLower(strings.TrimSpace(rawIntent)),
		},
		Confidence: 0.8,
		Reasoning:  "deterministic API fallback classifier",
		Source:     "rule_fallback",
	}
	if repoURL := routeGitHubRepoURLRe.FindString(rawIntent); repoURL != "" {
		context.Entities["preferred_repo_url"] = strings.TrimSuffix(repoURL, ".git")
	}
	if intentType == "AutoResearch" {
		context.Entities["needs_autoresearch"] = true
		if containsAny(rawIntent, []string{"rag", "RAG", "retrieval", "BM25", "GraphRAG", "检索", "知识库问答"}) {
			context.Entities["needs_dataset_research"] = true
			context.Entities["research_domain"] = "retrieval"
			context.Entities["experiment_adapter"] = "retrieval.v1"
		} else if containsAny(rawIntent, []string{"上传数据", "数据集", "策略", "超参数", "参数搜索", "方法选择", "最优配置", "strategy", "hyperparameter", "parameter search"}) {
			context.Entities["needs_dataset_research"] = true
		}
		if match := routeRepositoryRevisionRe.FindStringSubmatch(rawIntent); len(match) > 1 {
			context.Entities["repository_revision"] = strings.ToLower(match[1])
		}
		if match := regexp.MustCompile(`(?i)([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+\.json|autoresearch\.json)`).FindStringSubmatch(rawIntent); len(match) > 1 {
			context.Entities["autoresearch_spec_path"] = match[1]
		}
	}
	if intentpkg.ContainsPlotIntent(rawIntent) {
		context.Entities["needs_plot"] = true
		context.Entities["output_mode"] = "plot"
	}
	if intentType == "Paper_Reproduction" {
		for key, value := range collectPaperSearchFields(context, rawIntent) {
			context.Entities[key] = value
		}
		if containsAny(rawIntent, []string{"smoke", "最小实验", "最小验证", "快速验证"}) {
			context.Entities["smoke_reproduction"] = true
			context.Constraints["reproduction_mode"] = "smoke"
		}
		if containsAny(rawIntent, []string{"debug", "fix", "修复", "排查", "不一致", "重跑"}) {
			context.Entities["needs_fix"] = true
		}
		if containsAny(rawIntent, []string{"ablation", "消融", "参数敏感性", "模块移除", "随机种子", "seed stability", "运行成本对照"}) {
			context.Entities["needs_ablation"] = true
		}
	}
	return context
}

func shouldRouteUploadedCustomBenchmark(intentType, rawIntent string, attachments []map[string]any) bool {
	return intentType != "AutoResearch" &&
		hasBenchmarkDatasetAttachment(attachments) &&
		containsAny(rawIntent, []string{"benchmark", "基准测试", "评测", "测评", "跑分"})
}

func hasOnlyAutoResearchConfigAttachments(attachments []map[string]any) bool {
	if len(attachments) == 0 {
		return false
	}
	for _, attachment := range attachments {
		name := strings.ToLower(strings.TrimSpace(fmt.Sprint(attachment["name"])))
		switch name {
		case "autoresearch.json", "evaluator.py", "holdout_evaluator.py":
		default:
			return false
		}
	}
	return true
}

func hasBenchmarkDatasetAttachment(attachments []map[string]any) bool {
	for _, attachment := range attachments {
		name := strings.ToLower(strings.TrimSpace(fmt.Sprint(attachment["name"])))
		for _, extension := range []string{".csv", ".tsv", ".json", ".jsonl"} {
			if strings.HasSuffix(name, extension) {
				return true
			}
		}
	}
	return false
}

func removeAttachmentTextExcerpts(attachments []map[string]any) {
	for _, attachment := range attachments {
		delete(attachment, "text_excerpt")
	}
}

func writePlanSSEWithID(c *gin.Context, eventName string, id int64, payload any) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if id > 0 {
		if _, err := fmt.Fprintf(c.Writer, "id: %d\n", id); err != nil {
			return false
		}
	}
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventName, encoded); err != nil {
		return false
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}

func writePlanSSE(c *gin.Context, eventName string, payload any) bool {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", eventName, encoded); err != nil {
		return false
	}
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}
	return true
}

func planEventFingerprint(event models.PlanEvent) string {
	keys := make([]string, 0, len(event.Payload))
	for key := range event.Payload {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s|%s|%s|%s|%v", event.PlanID, event.EventType, event.TaskID, event.Timestamp.UTC().Format(time.RFC3339Nano), keys)
}

func isTerminalPlanEvent(event models.PlanEvent) bool {
	return event.EventType == "plan_completed" || event.EventType == "plan_failed" || event.EventType == "plan_canceled"
}
