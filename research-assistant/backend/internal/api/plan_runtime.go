package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	"unicode/utf8"

	"research-assistant-backend/internal/agentprofile"
	"research-assistant-backend/internal/blobstore"
	"research-assistant-backend/internal/budget"
	"research-assistant-backend/internal/events"
	intentpkg "research-assistant-backend/internal/intent"
	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/planner"
	"research-assistant-backend/internal/project"
	"research-assistant-backend/internal/revision"
	"research-assistant-backend/internal/sandbox"
	"research-assistant-backend/internal/scheduler"
	"research-assistant-backend/internal/store"
	"research-assistant-backend/internal/tools"

	"github.com/gin-gonic/gin"
)

const planStreamEventName = "plan_event"

type planRuntime struct {
	planner          *planner.Planner
	store            store.PlanStore
	projects         project.Store
	scheduler        *scheduler.Scheduler
	events           *events.Bus
	intentResolver   *intentpkg.Resolver
	complexityRouter *planner.ComplexityRouter
	budget           *budget.Manager
	revisions        revision.Store
	toolRegistry     *tools.Registry
	toolInvoker      *tools.Invoker
	profileRegistry  *agentprofile.Registry

	mu          sync.Mutex
	running     map[string]context.CancelFunc
	planLimiter *rateLimiter
	idempotency *idempotencyCache
}

func newPlanRuntime(p *planner.Planner, planStore store.PlanStore, runner *scheduler.Scheduler, eventBus *events.Bus) *planRuntime {
	return newPlanRuntimeWithProjects(p, planStore, nil, runner, eventBus)
}

func newPlanRuntimeWithProjects(p *planner.Planner, planStore store.PlanStore, projects project.Store, runner *scheduler.Scheduler, eventBus *events.Bus) *planRuntime {
	return newPlanRuntimeWithStores(p, planStore, projects, nil, runner, eventBus)
}

func newPlanRuntimeWithStores(p *planner.Planner, planStore store.PlanStore, projects project.Store, budgetManager *budget.Manager, runner *scheduler.Scheduler, eventBus *events.Bus) *planRuntime {
	if projects == nil {
		projects = project.NewMemoryStore()
	}
	if budgetManager == nil {
		budgetManager = budget.NewManager(budget.NewMemoryStore())
	}
	if runner != nil {
		runner.SetBudgetManager(budgetManager)
	}
	return &planRuntime{
		planner:          p,
		store:            planStore,
		projects:         projects,
		scheduler:        runner,
		events:           eventBus,
		intentResolver:   intentpkg.NewResolverFromEnv(),
		complexityRouter: planner.NewComplexityRouterFromEnv(),
		budget:           budgetManager,
		revisions:        revision.NewMemoryStore(),
		running:          map[string]context.CancelFunc{},
		planLimiter:      planRateLimitFromEnv(),
		idempotency:      newIdempotencyCache(24 * time.Hour),
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
	planStorePath := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH"))
	var planStore store.PlanStore = store.NewMemoryPlanStore()
	if path := planStorePath; path != "" {
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
	// Projects are a lightweight owner-scoped metadata store. They live next to
	// the plan store by default so a restart keeps the plan library grouping.
	var projectStore project.Store = project.NewMemoryStore()
	projectPath := strings.TrimSpace(os.Getenv("PROJECT_STORE_PATH"))
	if projectPath == "" {
		if planPath := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); planPath != "" {
			projectPath = planPath + ".projects.json"
		}
	}
	if projectPath != "" {
		if fileStore, err := project.NewFileStore(projectPath); err != nil {
			log.Printf("project store init failed, falling back to memory: %v", err)
		} else {
			projectStore = fileStore
		}
	}
	// Cost ledger: one JSON file next to the plan store keeps reservations and
	// settled entries across restarts. Without a plan store path the ledger is
	// in-memory, which is fine for tests and single-process dev.
	budgetPath := strings.TrimSpace(os.Getenv("BUDGET_STORE_PATH"))
	if budgetPath == "" {
		if planPath := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); planPath != "" {
			budgetPath = planPath + ".budget.json"
		}
	}
	var budgetManager *budget.Manager
	if budgetPath != "" {
		if fileBudget, err := budget.NewFileStore(budgetPath); err != nil {
			log.Printf("budget store init failed, falling back to memory: %v", err)
			budgetManager = budget.NewManager(budget.NewMemoryStore())
		} else {
			budgetManager = budget.NewManager(fileBudget)
		}
	} else {
		budgetManager = budget.NewManager(budget.NewMemoryStore())
	}
	// Revision snapshots live in their own append-only file. The plan snapshot
	// stays small and keeps serving the live execution state.
	revisionPath := strings.TrimSpace(os.Getenv("REVISION_STORE_PATH"))
	if revisionPath == "" {
		if planPath := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); planPath != "" {
			revisionPath = planPath + ".revisions.json"
		}
	}
	var revisionStore revision.Store = revision.NewMemoryStore()
	if revisionPath != "" {
		if fileRevisions, err := revision.NewFileStore(revisionPath); err != nil {
			log.Printf("revision store init failed, falling back to memory: %v", err)
		} else {
			revisionStore = fileRevisions
		}
	}
	eventBus := events.NewBus()
	toolRuntime := buildToolAgentRuntime(planStorePath)
	executor := scheduler.NewRoutedTaskExecutor(librarian, data, coder, specialized...)
	executor.SetAgentRuntime(toolRuntime.profiles, toolRuntime.profileRunner, toolRuntime.invoker)
	runner := scheduler.NewScheduler(planStore, executor, eventBus, 2)
	runner.SetFinalizer(&planner.PlanFinalizer{})
	runner.SetToolGate(makeToolGate(toolRuntime.tools, toolRuntime.profiles))
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
	runtime := newPlanRuntimeWithStores(p, planStore, projectStore, budgetManager, runner, eventBus)
	runtime.revisions = revisionStore
	runtime.toolRegistry = toolRuntime.tools
	runtime.toolInvoker = toolRuntime.invoker
	runtime.profileRegistry = toolRuntime.profiles
	runtime.register(apiGroup)
	RegisterToolRoutes(apiGroup, toolRuntime.tools, toolRuntime.invoker, toolRuntime.profiles)
	RegisterAgentProfileRoutes(apiGroup, toolRuntime.profiles, toolRuntime.tools)
}

func (r *planRuntime) register(apiGroup *gin.RouterGroup) {
	apiGroup.POST("/plan", r.createPlan)
	apiGroup.GET("/plans", r.listPlans)
	apiGroup.DELETE("/plans/:id", r.deletePlan)
	apiGroup.PATCH("/plans/:id", r.patchPlan)
	apiGroup.POST("/plans/:id/duplicate", r.duplicatePlan)
	apiGroup.GET("/plans/:id/export", r.exportPlan)
	apiGroup.POST("/plans/:id/reproduce/verify", r.verifyReproduction)
	apiGroup.GET("/projects", r.listProjects)
	apiGroup.POST("/projects", r.createProject)
	apiGroup.PATCH("/projects/:id", r.updateProject)
	apiGroup.DELETE("/projects/:id", r.deleteProject)
	apiGroup.GET("/sessions", r.listSessions)
	apiGroup.DELETE("/sessions/:id", r.deleteSession)
	apiGroup.GET("/plans/:id", r.getPlan)
	apiGroup.GET("/plans/:id/events", r.getPlanEvents)
	apiGroup.POST("/plans/:id/execute", r.executePlan)
	apiGroup.POST("/plans/:id/cancel", r.cancelPlan)
	apiGroup.POST("/plans/:id/approve", r.approvePlan)
	apiGroup.POST("/plans/:id/pause", r.pausePlan)
	apiGroup.POST("/plans/:id/resume", r.resumePlan)
	apiGroup.POST("/plans/:id/tasks/:taskId/run", r.runFromNode)
	apiGroup.GET("/plans/:id/runtime-actions", r.getRuntimeActions)
	apiGroup.POST("/plans/:id/runtime-actions/:actionId/decision", r.decideRuntimeAction)
	apiGroup.POST("/plans/:id/tasks/:taskId/retry", r.retryTask)
	apiGroup.POST("/plans/:id/tasks/:taskId/reassign", r.reassignTask)
	apiGroup.POST("/plans/:id/tasks/:taskId/execute", r.executeTask)
	apiGroup.GET("/plans/:id/stream-ticket", r.streamTicket)
	apiGroup.GET("/plans/:id/stream", r.streamPlanEvents)
	apiGroup.GET("/plans/:id/revisions", r.listRevisions)
	apiGroup.POST("/plans/:id/revisions", r.createRevision)
	apiGroup.GET("/plans/:id/revisions/:revision", r.getRevision)
	apiGroup.POST("/plans/:id/revisions/:revision/validate", r.validateRevision)
	apiGroup.POST("/plans/:id/revisions/:revision/approve", r.approveRevision)
	apiGroup.POST("/plans/:id/revisions/:revision/reject", r.rejectRevision)
	apiGroup.POST("/plans/:id/revisions/:revision/execute", r.executeRevision)
	apiGroup.GET("/plans/:id/diff", r.getPlanDiff)
	apiGroup.GET("/plans/:id/questions", r.listQuestions)
	apiGroup.POST("/plans/:id/questions", r.createQuestion)
	apiGroup.POST("/plans/:id/questions/:questionId/answer", r.answerQuestion)
	apiGroup.GET("/approvals", r.listApprovals)
	apiGroup.POST("/approvals/bulk", r.bulkApprovals)
	apiGroup.GET("/plans/:id/usage", r.planUsage)
	apiGroup.GET("/usage/summary", r.usageSummary)
	apiGroup.GET("/usage/timeseries", r.usageTimeseries)
	apiGroup.GET("/budgets", r.getBudget)
	apiGroup.PUT("/budgets", r.putBudget)
	apiGroup.GET("/budgets/alerts", r.budgetAlerts)
	apiGroup.POST("/plans/from-graph", r.importPlan)
	apiGroup.GET("/plans/:id/tool-calls", r.listToolCalls)
	apiGroup.POST("/plans/:id/tool-calls/:callId/replay", r.replayToolCall)
}

func (r *planRuntime) createPlan(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var payload RequestPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	trimmedIntent := strings.TrimSpace(payload.Intent)
	if trimmedIntent == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "intent is required", "code": "intent_required"})
		return
	}
	maxIntentChars := envIntDefault("MAX_INTENT_CHARS", 20000)
	if utf8.RuneCountInString(trimmedIntent) > maxIntentChars {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":       fmt.Sprintf("intent is longer than the %d character limit", maxIntentChars),
			"code":        "intent_too_long",
			"remediation": errorRemediation("intent_too_long"),
			"limit":       maxIntentChars,
			"actual":      utf8.RuneCountInString(trimmedIntent),
		})
		return
	}
	if len(payload.Attachments) > 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at most 8 attachments are allowed per plan", "code": "too_many_attachments"})
		return
	}

	userID := resolveUserID(c)
	sessionID := resolveSessionID(c)

	if allowed, retryAfter := r.planLimiter.Allow(userID); !allowed {
		seconds := int(retryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":               "too many plan creations; retry later",
			"code":                "rate_limited",
			"remediation":         errorRemediation("rate_limited"),
			"retry_after_seconds": seconds,
		})
		return
	}

	idempotencyKey := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(idempotencyKey) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Idempotency-Key is too long", "code": "invalid_idempotency_key"})
		return
	}
	if idempotencyKey != "" {
		if entry, ok := r.idempotency.Lookup(userID, idempotencyKey); ok {
			c.Header("Idempotency-Replayed", "true")
			contentType := entry.contentType
			if contentType == "" {
				contentType = "application/json; charset=utf-8"
			}
			c.Data(entry.status, contentType, entry.body)
			return
		}
	}
	activePlans, activeErr := r.countActivePlans(userID)
	if activeErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot inspect active plans", "code": "store_unavailable"})
		return
	}
	if maxActive := envIntDefault("PLAN_MAX_ACTIVE_PER_USER", 3); activePlans >= maxActive {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":       fmt.Sprintf("at most %d active plans are allowed per user", maxActive),
			"code":        "active_plan_quota_exceeded",
			"remediation": errorRemediation("active_plan_quota_exceeded"),
			"active":      activePlans,
			"max_active":  maxActive,
		})
		return
	}

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
	// An arXiv URL or DOI in the intent is imported automatically so the plan
	// starts from real metadata and the PDF instead of an LLM guess.
	var importedPaper *models.PaperMetadata
	if len(attachments) == 0 {
		if imported, paper, ok := r.importPlanReference(c.Request.Context(), userID, sessionID, payload.Intent); ok {
			importedPaper = paper
			if imported != nil {
				attachments = append(attachments, imported)
			}
			if paper != nil {
				intentContext.Entities["paper_metadata"] = paperMetadataMap(paper)
				if existingTitle, _ := intentContext.Entities["paper_title"].(string); strings.TrimSpace(existingTitle) == "" {
					intentContext.Entities["paper_title"] = paper.Title
				}
				if existingQuery, _ := intentContext.Entities["paper_search_query"].(string); strings.TrimSpace(existingQuery) == "" {
					intentContext.Entities["paper_search_query"] = paper.Title
				}
			}
		}
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
	if importedPaper != nil && researchContext.Paper == nil {
		researchContext.Paper = importedPaper
	}
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
	// The final route must be computed after attachments and paper metadata are
	// resolved: the text-only intent classifier cannot see them.
	route := r.complexityRouter.Decide(c.Request.Context(), intentContext, attachments, researchContext)
	intentContext.Complexity = string(route.Complexity)
	intentContext.Capabilities = route.Capabilities
	intentContext.RouteHint = route.RouteHint
	intentContext.ComplexityConfidence = route.Confidence
	intentContext.ComplexityReason = route.Reason
	plan, err := r.planner.BuildPlanWithRoute(c.Request.Context(), intentContext, route)
	if err != nil {
		log.Printf("Error generating plan graph: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate plan"})
		return
	}
	if len(attachments) > 0 || importedPaper != nil {
		plan.Context = researchContext
		if len(attachments) > 0 {
			if err := planner.ValidatePlanContextCoverage(plan, researchContext); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "missing_requirements": plan.MissingRequirements})
				return
			}
		}
	}
	plan.RuntimePolicy = runtimePolicy
	// Per-request budget caps override env defaults. They are applied before
	// governance so a caller can always tighten a plan, never loosen a limit.
	if payload.MaxCostUSD > 0 {
		plan.Budget.MaxCostUSD = payload.MaxCostUSD
	}
	if payload.MaxTokens > 0 {
		plan.Budget.MaxTokens = payload.MaxTokens
	}
	if payload.MaxSandboxSeconds > 0 {
		plan.Budget.MaxSandboxSeconds = payload.MaxSandboxSeconds
	}
	if payload.MaxDownloadBytes > 0 {
		plan.Budget.MaxDownloadBytes = payload.MaxDownloadBytes
	}
	if policy := strings.ToLower(strings.TrimSpace(payload.BudgetPolicy)); policy == "hard" || policy == "soft" {
		plan.Budget.Policy = policy
	}
	// Budget-aware routing: a caller may opt into a cheaper plan shape. This is
	// deliberately part of the intent context so the route decision records the
	// reason instead of silently changing templates.
	if costPolicy := strings.ToLower(strings.TrimSpace(payload.CostPolicy)); costPolicy == "quality_first" || costPolicy == "balanced" || costPolicy == "budget_first" {
		if intentContext.Constraints == nil {
			intentContext.Constraints = map[string]any{}
		}
		intentContext.Constraints["cost_policy"] = costPolicy
	}
	// Freeze the resolved model profiles so this run stays reproducible even if
	// the user edits profiles while the plan is executing.
	if resolver := modelresolver.Default(); resolver != nil {
		snapshot := resolver.Snapshot(userID)
		snapshot.CreatedAt = time.Now().UTC()
		plan.ModelSnapshot = &snapshot
	}
	configurePlanGovernance(plan, intentContext, userID, sessionID)
	// Review gate: expensive/high-risk/explicitly-reviewed plans must not start
	// automatically. The gate is evaluated after governance so approval status
	// and budget defaults are final.
	applyReviewGate(plan, payload.RequireReview)
	plan.AgentSnapshot = buildAgentSnapshots(plan, userID, r.profileRegistry)
	// Persist the initial immutable revision before the plan becomes visible;
	// editing later appends revision 2..N without touching revision 1.
	if initialRevision, revErr := r.revisionStore().Create(&models.PlanRevision{
		PlanID:    plan.ID,
		Graph:     plan,
		Status:    models.RevisionDraft,
		CreatedBy: userID,
		Reason:    "initial plan revision",
	}, 0); revErr != nil {
		log.Printf("initial revision creation failed plan=%s: %v", plan.ID, revErr)
	} else if initialRevision != nil {
		plan.CurrentRevision = initialRevision.Revision
	}
	if err := r.store.SavePlan(plan); err != nil {
		log.Printf("Error saving plan graph: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save plan"})
		return
	}
	_ = r.store.AppendEvent(plan.ID, models.PlanEvent{
		PlanID:    plan.ID,
		EventType: "plan_created",
		Payload: map[string]any{
			"actor":              userID,
			"session_id":         sessionID,
			"intent_type":        plan.IntentType,
			"planner_source":     plan.Meta.PlannerSource,
			"planning_route":     plan.Meta.PlanningRoute,
			"complexity":         plan.Meta.Complexity,
			"complexity_source":  plan.Meta.ComplexitySource,
			"template_variant":   plan.Meta.TemplateVariant,
			"general_plan_shape": plan.Meta.GeneralPlanShape,
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
	metrics.RecordPlanCreated(plan.IntentType)
	recordAuditEvent("plan_created", userID, plan.ID, map[string]any{
		"intent_type": plan.IntentType,
		"session_id":  sessionID,
	})
	encoded, encodeErr := json.Marshal(response)
	if encodeErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encode plan response"})
		return
	}
	r.idempotency.Store(userID, idempotencyKey, http.StatusOK, encoded, "application/json; charset=utf-8")
	c.Data(http.StatusOK, "application/json; charset=utf-8", encoded)
}

// countActivePlans counts the plans that currently occupy execution capacity.
func (r *planRuntime) countActivePlans(userID string) (int, error) {
	all, err := r.store.ListPlans()
	if err != nil {
		return 0, err
	}
	active := 0
	for _, plan := range all {
		if plan == nil || plan.OwnerID != userID {
			continue
		}
		switch plan.Status {
		case models.StatusInProgress, models.StatusAwaitingApproval:
			active++
		}
	}
	return active, nil
}

func (r *planRuntime) getPlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	// Lazy timeout sweep: a paused plan must honour question deadlines even if
	// no scheduler goroutine is currently iterating it.
	if hasPendingQuestions(plan) {
		_, _ = r.scheduler.ExpireQuestionsNow(plan.ID)
		if refreshed, err := r.store.GetPlan(plan.ID); err == nil && refreshed != nil {
			plan = refreshed
		}
	}
	hydratePlanForResponse(plan)
	c.JSON(http.StatusOK, gin.H{"plan_graph": plan})
}

func (r *planRuntime) deletePlan(c *gin.Context) {
	plan, ok := r.authorizedPlan(c)
	if !ok {
		return
	}
	if plan.Status == models.StatusInProgress || plan.Status == models.StatusAwaitingApproval {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is running or awaiting approval; cancel it before deleting", "code": "plan_active"})
		return
	}
	if err := r.store.DeletePlan(plan.ID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	_ = r.revisionStore().DeletePlan(plan.ID)
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
	if plan.Paused {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is paused; resume it before running", "code": "plan_paused", "remediation": errorRemediation("plan_paused")})
		return
	}
	if plan.Status == models.StatusAwaitingReview {
		c.JSON(http.StatusConflict, gin.H{
			"error":       "plan is awaiting review; approve it before running",
			"code":        "review_pending",
			"remediation": "在审阅页批准该计划，或通过 revision 审阅接口批准后执行。",
		})
		return
	}
	if plan.Review.Required && plan.Review.Status != models.ReviewApproved {
		code := "review_pending"
		message := "plan requires review before running"
		if plan.Review.Status == models.ReviewRejected {
			code = "review_rejected"
			message = "plan review was rejected; edit the plan and approve a new revision"
		}
		c.JSON(http.StatusConflict, gin.H{"error": message, "code": code})
		return
	}
	if plan.Status != models.StatusPending && plan.Status != models.StatusReady {
		c.JSON(http.StatusConflict, gin.H{"error": fmt.Sprintf("plan cannot be started from status %s", plan.Status)})
		return
	}
	var payload struct {
		Trigger string `json:"trigger,omitempty"`
	}
	_ = c.ShouldBindJSON(&payload)
	trigger := strings.TrimSpace(payload.Trigger)
	if trigger == "" {
		trigger = "manual"
	}
	if trigger != "manual" && trigger != "auto" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "trigger must be manual or auto", "code": "invalid_trigger"})
		return
	}
	// Bind the live graph to the latest immutable revision before dispatch so a
	// run can never mix a newer draft with the previous revision stamp.
	if _, err := r.bindLatestRevision(planID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot bind plan revision", "code": "revision_bind_failed"})
		return
	}
	actor := resolveUserID(c)
	if err := r.launchPlan(planID, trigger, actor); err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "plan is already running", "code": "plan_already_running", "remediation": errorRemediation("plan_already_running")})
		return
	}
	recordAuditEvent("plan_execution_requested", actor, planID, map[string]any{"trigger": trigger})
	c.JSON(http.StatusAccepted, gin.H{
		"message": "Plan execution started",
		"plan_id": planID,
		"trigger": trigger,
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
		// The legacy approve endpoint also covers the new review gate so an
		// existing client keeps working. A dedicated revision approve route
		// records which revision was reviewed. When both gates are active the
		// runtime permission is decided first, then the review gate, matching
		// applyReviewGate's precedence rule.
		if current.Status == models.StatusAwaitingApproval {
			if !current.Approval.Required {
				return fmt.Errorf("plan does not require approval")
			}
			if current.Approval.Status == "approved" {
				return fmt.Errorf("plan is already approved")
			}
			current.Approval.Status = "approved"
			current.Approval.ApprovedBy = userID
			current.Approval.ApprovedAt = &now
			if current.Review.Required && current.Review.Status != models.ReviewApproved {
				current.Status = models.StatusAwaitingReview
			} else {
				current.Status = models.StatusPending
			}
			current.UpdatedAt = now
			return nil
		}
		if current.Status == models.StatusAwaitingReview || current.Review.Required {
			if current.Review.Status == models.ReviewApproved {
				return fmt.Errorf("plan is already approved")
			}
			current.Review.Status = models.ReviewApproved
			current.Review.ReviewedBy = userID
			current.Review.ReviewedAt = &now
			if current.Approval.Required && current.Approval.Status != "approved" {
				current.Status = models.StatusAwaitingApproval
			} else {
				current.Status = models.StatusPending
			}
			current.UpdatedAt = now
			if latest, err := r.revisionStore().Latest(current.ID); err == nil && latest != nil {
				_ = r.revisionStore().UpdateStatus(current.ID, latest.Revision, models.RevisionApproved, userID, &now)
				current.CurrentRevision = latest.Revision
			}
			return nil
		}
		return fmt.Errorf("plan cannot be approved from status %s", current.Status)
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
			if !writePlanSSEWithID(c, planStreamEventName, event.Seq, event) {
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
	applyPlanBudgetDefaults(plan)
	if plan.Budget.MaxCostUSD > 0 || plan.Budget.MaxTokens > 0 || plan.Budget.MaxSandboxSeconds > 0 || plan.Budget.MaxDownloadBytes > 0 {
		plan.Usage.BudgetStatus = "ok"
	}
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

// applyPlanBudgetDefaults fills the optional cost caps from env defaults. The
// request payload is applied earlier in createPlan and always wins.
func applyPlanBudgetDefaults(plan *models.PlanGraph) {
	if plan == nil {
		return
	}
	if plan.Budget.MaxCostUSD <= 0 {
		plan.Budget.MaxCostUSD = positiveEnvFloat("PLAN_MAX_COST_USD", 0)
	}
	if plan.Budget.MaxTokens <= 0 {
		plan.Budget.MaxTokens = positiveEnvInt("PLAN_MAX_TOKENS", 0)
	}
	if plan.Budget.MaxSandboxSeconds <= 0 {
		plan.Budget.MaxSandboxSeconds = positiveEnvFloat("PLAN_MAX_SANDBOX_SECONDS", 0)
	}
	if plan.Budget.MaxDownloadBytes <= 0 {
		plan.Budget.MaxDownloadBytes = int64(positiveEnvInt("PLAN_MAX_DOWNLOAD_BYTES", 0))
	}
	if strings.TrimSpace(plan.Budget.Policy) == "" {
		plan.Budget.Policy = strings.ToLower(strings.TrimSpace(os.Getenv("PLAN_BUDGET_POLICY")))
	}
	if plan.Budget.Policy != "hard" {
		plan.Budget.Policy = "soft"
	}
}

func positiveEnvFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
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
