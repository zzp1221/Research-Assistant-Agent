package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/memory"

	"github.com/gin-gonic/gin"
)

// RegisterMemoryRoutes wires the memory CRUD/search surface. The handlers are
// intentionally thin: all scoping and safety checks live in internal/memory.
func RegisterMemoryRoutes(apiGroup *gin.RouterGroup, store memory.Store, settings *memory.SettingsStore) {
	if store == nil {
		return
	}
	apiGroup.GET("/memories", func(c *gin.Context) { listMemories(c, store) })
	apiGroup.POST("/memories", func(c *gin.Context) { createMemory(c, store) })
	apiGroup.GET("/memories/stats", func(c *gin.Context) { memoryStats(c, store) })
	apiGroup.GET("/memories/export", func(c *gin.Context) { exportMemories(c, store) })
	apiGroup.POST("/memories/import", func(c *gin.Context) { importMemories(c, store) })
	apiGroup.POST("/memories/search", func(c *gin.Context) { searchMemories(c, store, settings) })
	apiGroup.POST("/memories/consolidate", func(c *gin.Context) {
		report, err := memory.NewConsolidator(store, settings).RunOnce(c.Request.Context(), resolveUserID(c))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, report)
	})
	apiGroup.GET("/memories/settings", func(c *gin.Context) { getMemorySettings(c, settings) })
	apiGroup.PUT("/memories/settings", func(c *gin.Context) { putMemorySettings(c, settings) })
	apiGroup.GET("/memories/:id", func(c *gin.Context) { getMemory(c, store) })
	apiGroup.PATCH("/memories/:id", func(c *gin.Context) { patchMemory(c, store) })
	apiGroup.DELETE("/memories/:id", func(c *gin.Context) { deleteMemory(c, store) })
	apiGroup.POST("/memories/:id/feedback", func(c *gin.Context) { feedbackMemory(c, store) })
}

func listMemories(c *gin.Context, store memory.Store) {
	q := memory.Query{OwnerID: resolveUserID(c), AllScopes: true}
	if raw := strings.TrimSpace(c.Query("kind")); raw != "" {
		q.Kinds = []memory.Kind{memory.Kind(raw)}
	}
	if raw := strings.TrimSpace(c.Query("status")); raw != "" {
		q.Statuses = []memory.Status{memory.Status(raw)}
	} else {
		q.Statuses = []memory.Status{memory.StatusActive, memory.StatusPending, memory.StatusSuperseded, memory.StatusArchived}
	}
	q.Text = strings.TrimSpace(c.Query("q"))
	if limit, err := strconv.Atoi(c.Query("limit")); err == nil && limit > 0 && limit <= 200 {
		q.Limit = limit
	}
	if pinned := strings.TrimSpace(c.Query("pinned")); pinned != "" {
		q.Tags = nil
	}
	items, total, err := store.List(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pinned := strings.TrimSpace(c.Query("pinned")); pinned == "true" || pinned == "1" {
		filtered := make([]memory.Record, 0, len(items))
		for _, item := range items {
			if item.Pinned {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	c.JSON(http.StatusOK, gin.H{"memories": items, "total": total})
}

func createMemory(c *gin.Context, store memory.Store) {
	var rec memory.Record
	if err := c.ShouldBindJSON(&rec); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rec.Scope.OwnerID = resolveUserID(c)
	rec.Trust = memory.TrustUserExplicit
	rec.Status = memory.StatusActive
	rec.Source.Type = "user"
	created, err := store.Put(c.Request.Context(), rec)
	if err != nil {
		writeMemoryError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"memory": created})
}

func getMemory(c *gin.Context, store memory.Store) {
	rec, err := store.Get(c.Request.Context(), resolveUserID(c), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "memory not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"memory": rec})
}

func patchMemory(c *gin.Context, store memory.Store) {
	var patch memory.Patch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rec, err := store.Update(c.Request.Context(), resolveUserID(c), c.Param("id"), patch)
	if err != nil {
		writeMemoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"memory": rec})
}

func deleteMemory(c *gin.Context, store memory.Store) {
	hard := strings.EqualFold(c.Query("hard"), "true")
	if hard {
		adminToken := strings.TrimSpace(os.Getenv("ADMIN_TOKEN"))
		if adminToken == "" || c.GetHeader("X-Admin-Token") != adminToken {
			c.JSON(http.StatusForbidden, gin.H{"error": "hard delete requires an admin token"})
			return
		}
	}
	if err := store.Delete(c.Request.Context(), resolveUserID(c), c.Param("id"), hard); err != nil {
		writeMemoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true, "hard": hard})
}

type memorySearchRequest struct {
	AsOf                  string   `json:"as_of,omitempty"`
	Query                 string   `json:"query"`
	Kinds                 []string `json:"kinds,omitempty"`
	ProjectID             string   `json:"project_id,omitempty"`
	SessionID             string   `json:"session_id,omitempty"`
	PlanID                string   `json:"plan_id,omitempty"`
	AgentID               string   `json:"agent_id,omitempty"`
	Limit                 int      `json:"limit,omitempty"`
	CoreBudgetTokens      int      `json:"core_budget_tokens,omitempty"`
	RetrievedBudgetTokens int      `json:"retrieved_budget_tokens,omitempty"`
	IncludeSuperseded     bool     `json:"include_superseded,omitempty"`
}

func searchMemories(c *gin.Context, store memory.Store, settings *memory.SettingsStore) {
	var req memorySearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ownerID := resolveUserID(c)
	q := memory.Query{
		OwnerID: ownerID, ProjectID: req.ProjectID, SessionID: req.SessionID,
		PlanID: req.PlanID, AgentID: req.AgentID, Text: memory.BuildQuery(req.Query, nil, 2000),
		IncludeSuperseded: req.IncludeSuperseded,
	}
	if req.AsOf != "" {
		asOf, err := time.Parse(time.RFC3339Nano, req.AsOf)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "as_of must be an RFC3339 timestamp"})
			return
		}
		q.AsOf = &asOf
		q.IncludeSuperseded = true
	}
	for _, kind := range req.Kinds {
		q.Kinds = append(q.Kinds, memory.Kind(kind))
	}
	if req.Limit > 0 {
		q.Limit = req.Limit
	} else if settings != nil {
		q.Limit = settings.Get(ownerID).RetrievalTopK
	}
	coreBudget, retrievedBudget := 0, 0
	if settings != nil {
		cfg := settings.Get(ownerID)
		coreBudget, retrievedBudget = cfg.CoreBudgetTokens, cfg.RetrievedBudgetTokens
	}
	if req.CoreBudgetTokens > 0 {
		coreBudget = req.CoreBudgetTokens
	}
	if req.RetrievedBudgetTokens > 0 {
		retrievedBudget = req.RetrievedBudgetTokens
	}
	result, err := store.SearchWithContext(c.Request.Context(), q, coreBudget, retrievedBudget)
	if err != nil {
		writeMemoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": result.Results, "context": result.Context})
}

func memoryStats(c *gin.Context, store memory.Store) {
	stats, err := store.Stats(c.Request.Context(), resolveUserID(c))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

func exportMemories(c *gin.Context, store memory.Store) {
	q := memory.Query{OwnerID: resolveUserID(c), Statuses: []memory.Status{memory.StatusActive, memory.StatusPending, memory.StatusSuperseded, memory.StatusArchived}, AllScopes: true, Limit: 100000}
	records, _, err := store.List(c.Request.Context(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"version": 1, "records": records})
}

func importMemories(c *gin.Context, store memory.Store) {
	var payload struct {
		Version int             `json:"version"`
		Records []memory.Record `json:"records"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	ownerID := resolveUserID(c)
	for index := range payload.Records {
		payload.Records[index].Scope.OwnerID = ownerID
		if payload.Records[index].Trust == "" {
			payload.Records[index].Trust = memory.TrustUserExplicit
		}
	}
	const batchSize = 500
	imported, skipped := 0, 0
	for start := 0; start < len(payload.Records); start += batchSize {
		end := start + batchSize
		if end > len(payload.Records) {
			end = len(payload.Records)
		}
		batchImported, batchSkipped, err := store.PutBatch(c.Request.Context(), payload.Records[start:end])
		if err != nil {
			writeMemoryError(c, err)
			return
		}
		imported += batchImported
		skipped += batchSkipped
	}
	c.JSON(http.StatusOK, gin.H{"imported": imported, "skipped": skipped})
}

func writeMemoryError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, memory.ErrStorageUnavailable) {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "memory_store_unavailable", "detail": err.Error()})
		return
	}
	message := err.Error()
	if strings.Contains(message, "not found") {
		c.JSON(http.StatusNotFound, gin.H{"error": message})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": message})
}

func feedbackMemory(c *gin.Context, store memory.Store) {
	var payload struct {
		Useful bool `json:"useful"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	rec, err := store.Get(c.Request.Context(), resolveUserID(c), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "memory not found"})
		return
	}
	if payload.Useful {
		rec.UseCount++
	}
	if _, err := store.Put(c.Request.Context(), rec); err != nil {
		writeMemoryError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"recorded": true})
}

func getMemorySettings(c *gin.Context, settings *memory.SettingsStore) {
	if settings == nil {
		c.JSON(http.StatusOK, memory.DefaultSettings())
		return
	}
	c.JSON(http.StatusOK, settings.Get(resolveUserID(c)))
}

func putMemorySettings(c *gin.Context, settings *memory.SettingsStore) {
	if settings == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "memory settings store is not configured"})
		return
	}
	var payload memory.Settings
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := settings.Put(resolveUserID(c), payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, payload)
}

// memoryJSON is kept for export helpers that need a stable envelope.
func memoryJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
