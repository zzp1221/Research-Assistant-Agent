package api

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"research-assistant-backend/internal/apppaths"
	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/wiki"
)

var wikiRuntimeOnce struct {
	sync.Once
	store   *wiki.PaperStore
	builder *wiki.Builder
	enabled bool
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

// WikiStore returns the process-wide paper store (nil when disabled).
func WikiStore() *wiki.PaperStore {
	wikiRuntimeOnce.Do(initWikiRuntime)
	return wikiRuntimeOnce.store
}

// WikiBuilder returns the process-wide builder (nil when disabled).
func WikiBuilder() *wiki.Builder {
	wikiRuntimeOnce.Do(initWikiRuntime)
	return wikiRuntimeOnce.builder
}

// WikiEnabled reports whether the wiki is enabled.
func WikiEnabled() bool {
	wikiRuntimeOnce.Do(initWikiRuntime)
	return wikiRuntimeOnce.enabled
}

func initWikiRuntime() {
	wikiRuntimeOnce.enabled = wiki.Enabled()
	if !wikiRuntimeOnce.enabled {
		return
	}
	dir := WikiDirectory()
	store, err := wiki.NewPaperStore(dir)
	if err != nil {
		log.Printf("[wiki] init failed dir=%s: %v (falling back to in-memory store)", dir, err)
		store = wiki.NewMemoryPaperStore()
	}
	wikiRuntimeOnce.store = store
	wikiRuntimeOnce.builder = wiki.NewBuilder(store, MemoryStore(), documents.NewStore(""), getLiteratureLibrary(), wiki.NewAliasStore(dir))
	wikiRuntimeOnce.builder.Citations = getLiteratureClient()
	log.Printf("[wiki] store ready dir=%s mode=%s", dir, wiki.Mode())
}

func registerWikiRuntime(apiGroup *gin.RouterGroup) {
	if !WikiEnabled() || WikiStore() == nil {
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
