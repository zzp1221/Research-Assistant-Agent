package api

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"research-assistant-backend/internal/apppaths"
	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/wiki"
)

var wikiRuntimeOnce struct {
	sync.Once
	store     *wiki.PaperStore
	builder   *wiki.Builder
	enabled   bool
	degraded  bool
	ephemeral bool
	reason    string
}

// WikiDirectory resolves the on-disk wiki root once per process.
func WikiDirectory() string {
	dir := strings.TrimSpace(os.Getenv("WIKI_DIR"))
	if dir == "" {
		if planPath := strings.TrimSpace(os.Getenv("PLAN_STORE_PATH")); planPath != "" {
			dir = filepath.Join(filepath.Dir(planPath), "wiki")
		} else {
			dir = filepath.Join(apppaths.DataDir(), "wiki")
		}
	}
	return dir
}

// WikiStore returns the process-wide paper store (nil when disabled or failed
// closed).
func WikiStore() *wiki.PaperStore {
	wikiRuntimeOnce.Do(initWikiRuntime)
	return wikiRuntimeOnce.store
}

// WikiBuilder returns the process-wide builder (nil when disabled).
func WikiBuilder() *wiki.Builder {
	wikiRuntimeOnce.Do(initWikiRuntime)
	return wikiRuntimeOnce.builder
}

// WikiEnabled reports whether the wiki is configured.
func WikiEnabled() bool {
	wikiRuntimeOnce.Do(initWikiRuntime)
	return wikiRuntimeOnce.enabled
}

// WikiAvailable reports whether the wiki store is usable.
func WikiAvailable() bool {
	wikiRuntimeOnce.Do(initWikiRuntime)
	return wikiRuntimeOnce.store != nil && wikiRuntimeOnce.builder != nil
}

// WikiHealth is embedded in /api/health.
func WikiHealth() gin.H {
	health := gin.H{
		"ok":        wikiRuntimeOnce.store != nil || !wikiRuntimeOnce.enabled,
		"enabled":   wikiRuntimeOnce.enabled,
		"degraded":  wikiRuntimeOnce.degraded,
		"ephemeral": wikiRuntimeOnce.ephemeral,
	}
	if wikiRuntimeOnce.reason != "" {
		health["reason"] = wikiRuntimeOnce.reason
	}
	return health
}

func allowEphemeralWiki() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("WIKI_ALLOW_EPHEMERAL")), "true")
}

func initWikiRuntime() {
	wikiRuntimeOnce.enabled = wiki.Enabled()
	if !wikiRuntimeOnce.enabled {
		return
	}
	dir := WikiDirectory()
	store, err := wiki.NewPaperStore(dir)
	if err != nil {
		wikiRuntimeOnce.degraded = true
		wikiRuntimeOnce.reason = err.Error()
		if allowEphemeralWiki() {
			log.Printf("[wiki] init failed dir=%s: %v (WIKI_ALLOW_EPHEMERAL=true, using ephemeral store)", dir, err)
			store = wiki.NewMemoryPaperStore()
			wikiRuntimeOnce.store = store
			wikiRuntimeOnce.ephemeral = true
		} else {
			log.Printf("[wiki] init failed dir=%s: %v (fail-closed; wiki routes will return 503)", dir, err)
			return
		}
	} else {
		wikiRuntimeOnce.store = store
	}
	wikiRuntimeOnce.builder = wiki.NewBuilder(store, MemoryStore(), documents.NewStore(""), getLiteratureLibrary(), wiki.NewAliasStore(dir))
	// External citation lookup is optional and off unless the deployment
	// configures an OpenAlex contact (or opts in explicitly). This keeps
	// deterministic builds fast and offline-safe.
	if strings.TrimSpace(os.Getenv("OPENALEX_MAILTO")) != "" || strings.EqualFold(strings.TrimSpace(os.Getenv("WIKI_CITATIONS_ENABLED")), "true") {
		wikiRuntimeOnce.builder.Citations = getLiteratureClient()
	}
	wikiRuntimeOnce.builder.LLM = wikiModelLLM{}
	log.Printf("[wiki] store ready dir=%s mode=%s degraded=%v", dir, wiki.Mode(), wikiRuntimeOnce.degraded)
}

func wikiUnavailable(c *gin.Context) {
	c.JSON(503, gin.H{"error": "wiki_store_unavailable", "reason": wikiRuntimeOnce.reason})
}

func registerWikiRuntime(apiGroup *gin.RouterGroup) {
	if !WikiEnabled() {
		return
	}
	if !WikiAvailable() {
		apiGroup.Any("/wiki", wikiUnavailable)
		apiGroup.Any("/wiki/*path", wikiUnavailable)
		return
	}
	RegisterWikiRoutes(apiGroup, WikiBuilder())
}

// refreshWikiForPlan is a best-effort hook used by the memory sink. Failures
// are logged and never change the plan result.
func refreshWikiForPlan(plan *models.PlanGraph) {
	builder := WikiBuilder()
	if builder == nil || !wiki.Enabled() {
		return
	}
	if err := builder.RefreshForPlan(context.Background(), plan); err != nil {
		log.Printf("[wiki] refresh plan %s failed: %v", plan.ID, err)
	}
}

// tagAndRefreshPlan attaches wiki tags to the deterministic memory records and
// refreshes the paper page. It runs after memory.RecordPlan.
func tagAndRefreshPlan(store memory.Store, plan *models.PlanGraph) {
	if builder := WikiBuilder(); builder != nil && builder.Aliases != nil && plan != nil && plan.Context != nil && plan.Context.Paper != nil {
		if paperID, _, err := builder.Aliases.Resolve(plan.OwnerID, *plan.Context.Paper, nil); err == nil {
			if err := wiki.TagPlanRecords(context.Background(), store, plan, paperID); err != nil {
				log.Printf("[wiki] tag plan %s failed: %v", plan.ID, err)
			}
		}
	}
	refreshWikiForPlan(plan)
}

// tagTaskAndIngest tags task/procedure records and ingests paper_parse output as
// pending wiki points. It also records metrics artifacts as result points.
func tagTaskAndIngest(ctx context.Context, store memory.Store, plan *models.PlanGraph, task *models.TaskNode, result *models.TaskExecutionResult) {
	builder := WikiBuilder()
	if builder == nil || plan == nil || task == nil {
		return
	}
	if err := builder.RefreshForTask(ctx, plan, task, result); err != nil {
		log.Printf("[wiki] refresh task %s failed: %v", task.ID, err)
	}
}

// wikiModelLLM adapts the deployment model resolver to wiki.LLM. It uses the
// owner's text profile (or the env-bootstrap profile) and asks for strict JSON.
type wikiModelLLM struct{}

func (wikiModelLLM) Complete(ctx context.Context, ownerID, system, user string) (string, error) {
	resolver := modelresolver.Default()
	if resolver == nil {
		return "", fmt.Errorf("model resolver is not configured")
	}
	response, _, err := resolver.Complete(ctx, ownerID, []oaichat.Message{
		{Role: "system", Text: system},
		{Role: "user", Text: user},
	}, oaichat.Options{Temperature: 0.2, MaxOutputTokens: 1600, ResponseFormatJSON: true})
	if err != nil {
		return "", err
	}
	if response == nil {
		return "", fmt.Errorf("empty llm response")
	}
	return response.Content, nil
}
