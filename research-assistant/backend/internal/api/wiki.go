package api

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/wiki"
)

// RegisterWikiRoutes wires the Paper Wiki read model and governance surface.
func RegisterWikiRoutes(apiGroup *gin.RouterGroup, builder *wiki.Builder) {
	if builder == nil {
		return
	}
	apiGroup.GET("/wiki/papers", func(c *gin.Context) { listWikiPapers(c, builder) })
	apiGroup.POST("/wiki/papers", func(c *gin.Context) { createWikiPaper(c, builder) })
	apiGroup.GET("/wiki/papers/:paperID", func(c *gin.Context) { getWikiPaper(c, builder) })
	apiGroup.PATCH("/wiki/papers/:paperID", func(c *gin.Context) { patchWikiPaper(c, builder) })
	apiGroup.DELETE("/wiki/papers/:paperID", func(c *gin.Context) { archiveWikiPaper(c, builder) })
	apiGroup.GET("/wiki/papers/:paperID/points", func(c *gin.Context) { listWikiPoints(c, builder) })
	apiGroup.POST("/wiki/papers/:paperID/points", func(c *gin.Context) { createWikiPoint(c, builder) })
	apiGroup.GET("/wiki/papers/:paperID/links", func(c *gin.Context) { listWikiPaperLinks(c, builder) })
	apiGroup.GET("/wiki/links", func(c *gin.Context) { listWikiLinks(c, builder) })
	apiGroup.PATCH("/wiki/points/:pointID", func(c *gin.Context) { patchWikiPoint(c, builder) })
	apiGroup.DELETE("/wiki/points/:pointID", func(c *gin.Context) { deleteWikiPoint(c, builder) })
	apiGroup.POST("/wiki/links/:linkID/confirm", func(c *gin.Context) { confirmWikiLink(c, builder) })
	apiGroup.POST("/wiki/links/:linkID/reject", func(c *gin.Context) { rejectWikiLink(c, builder) })
	apiGroup.POST("/wiki/build", func(c *gin.Context) { startWikiBuild(c, builder) })
	apiGroup.GET("/wiki/build/:jobID", func(c *gin.Context) { getWikiBuild(c) })
	apiGroup.GET("/wiki/stats", func(c *gin.Context) { wikiStats(c, builder) })
}

func wikiError(c *gin.Context, err error) {
	message := err.Error()
	switch {
	case strings.Contains(message, "not found"):
		c.JSON(http.StatusNotFound, gin.H{"error": message})
	case strings.Contains(message, "hidden benchmark"):
		c.JSON(http.StatusNotFound, gin.H{"error": "paper not found"})
	case strings.Contains(message, "quote") && strings.Contains(message, "locate"):
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "quote_not_located", "detail": message})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": message})
	}
}

func listWikiPapers(c *gin.Context, builder *wiki.Builder) {
	query := wiki.PaperQuery{OwnerID: resolveUserID(c), ProjectID: strings.TrimSpace(c.Query("project")), Text: strings.TrimSpace(c.Query("q")), Status: strings.TrimSpace(c.Query("status"))}
	if limit, err := strconv.Atoi(c.Query("limit")); err == nil && limit > 0 {
		query.Limit = limit
	}
	if cursor, err := strconv.Atoi(c.Query("cursor")); err == nil && cursor > 0 {
		query.Cursor = cursor
	}
	papers, total, err := builder.Store.List(c.Request.Context(), query)
	if err != nil {
		wikiError(c, err)
		return
	}
	next := query.Cursor + len(papers)
	if next >= total {
		next = 0
	}
	c.JSON(http.StatusOK, gin.H{"papers": papers, "total": total, "next_cursor": next})
}

type wikiPaperPayload struct {
	Title      string          `json:"title"`
	Authors    []string        `json:"authors"`
	Year       int             `json:"year"`
	Venue      string          `json:"venue"`
	ArxivID    string          `json:"arxiv_id"`
	DOI        string          `json:"doi"`
	Abstract   string          `json:"abstract"`
	TLDR       string          `json:"tldr"`
	ProjectID  string          `json:"project_id"`
	DocumentID string          `json:"document_id"`
	RepoURL    string          `json:"repo_url"`
	Entities   []memory.Entity `json:"entities"`
}

func createWikiPaper(c *gin.Context, builder *wiki.Builder) {
	var payload wikiPaperPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload: " + err.Error()})
		return
	}
	meta := models.PaperMetadata{Title: payload.Title, Authors: payload.Authors, Year: payload.Year, Venue: payload.Venue, ArxivID: payload.ArxivID, DOI: payload.DOI, Abstract: payload.Abstract, RepoURL: payload.RepoURL, Source: "user", Confidence: 1.0}
	page, err := builder.EnsurePaper(c.Request.Context(), resolveUserID(c), payload.ProjectID, meta, payload.DocumentID)
	if err != nil {
		wikiError(c, err)
		return
	}
	if len(payload.Entities) > 0 {
		page.Entities = mergePaperEntities(page.Entities, payload.Entities)
		if saved, saveErr := builder.Store.Put(c.Request.Context(), page); saveErr == nil {
			page = saved
		}
	}
	if strings.TrimSpace(payload.TLDR) != "" {
		page.TLDR = strings.TrimSpace(payload.TLDR)
		page.TLDRInferred = true
		if saved, saveErr := builder.Store.Put(c.Request.Context(), page); saveErr == nil {
			page = saved
		}
	}
	c.JSON(http.StatusCreated, gin.H{"paper": page})
}

func getWikiPaper(c *gin.Context, builder *wiki.Builder) {
	page, err := builder.Store.Get(c.Request.Context(), resolveUserID(c), c.Param("paperID"))
	if err != nil {
		wikiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"paper": page})
}

type wikiPaperPatch struct {
	Title        *string                  `json:"title"`
	TLDR         *string                  `json:"tldr"`
	Reproduction *wiki.ReproductionStatus `json:"reproduction"`
	Archived     *bool                    `json:"archived"`
	ProjectID    *string                  `json:"project_id"`
}

func patchWikiPaper(c *gin.Context, builder *wiki.Builder) {
	var payload wikiPaperPatch
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload: " + err.Error()})
		return
	}
	page, err := builder.Store.Get(c.Request.Context(), resolveUserID(c), c.Param("paperID"))
	if err != nil {
		wikiError(c, err)
		return
	}
	if payload.Title != nil {
		page.Title = strings.TrimSpace(*payload.Title)
	}
	if payload.TLDR != nil {
		page.TLDR = strings.TrimSpace(*payload.TLDR)
		page.TLDRInferred = true
	}
	if payload.Reproduction != nil {
		page.Reproduction = *payload.Reproduction
	}
	if payload.Archived != nil {
		page.Archived = *payload.Archived
	}
	if payload.ProjectID != nil {
		page.ProjectID = strings.TrimSpace(*payload.ProjectID)
	}
	saved, err := builder.Store.Put(c.Request.Context(), page)
	if err != nil {
		wikiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"paper": saved})
}

func archiveWikiPaper(c *gin.Context, builder *wiki.Builder) {
	page, err := builder.Store.Archive(c.Request.Context(), resolveUserID(c), c.Param("paperID"), true)
	if err != nil {
		wikiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"paper": page})
}

func listWikiPoints(c *gin.Context, builder *wiki.Builder) {
	limit := 50
	if parsed, err := strconv.Atoi(c.Query("limit")); err == nil && parsed > 0 {
		limit = parsed
	}
	offset := 0
	if parsed, err := strconv.Atoi(c.Query("cursor")); err == nil && parsed > 0 {
		offset = parsed
	}
	kind := wiki.PointKind(strings.TrimSpace(c.Query("kind")))
	points, total, err := builder.ListPoints(c.Request.Context(), resolveUserID(c), c.Param("paperID"), kind, limit, offset)
	if err != nil {
		wikiError(c, err)
		return
	}
	next := offset + len(points)
	if next >= total {
		next = 0
	}
	c.JSON(http.StatusOK, gin.H{"points": points, "total": total, "next_cursor": next})
}

type wikiPointPayload struct {
	Kind       wiki.PointKind     `json:"kind"`
	Title      string             `json:"title"`
	Text       string             `json:"text"`
	Refs       []memory.SourceRef `json:"refs"`
	Attributes map[string]any     `json:"attributes"`
}

func createWikiPoint(c *gin.Context, builder *wiki.Builder) {
	var payload wikiPointPayload
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload: " + err.Error()})
		return
	}
	kind := payload.Kind
	if kind == "" {
		kind = wiki.PointUserNote
	}
	if kind != wiki.PointUserNote {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only user_note points can be created directly"})
		return
	}
	text := strings.TrimSpace(payload.Text)
	if text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "text is required"})
		return
	}
	refs := payload.Refs
	if len(refs) == 0 {
		refs = []memory.SourceRef{{Section: "user_note", Quote: text}}
	}
	point, err := builder.CreatePoint(c.Request.Context(), wiki.PointInput{
		OwnerID: resolveUserID(c), ProjectID: strings.TrimSpace(c.Query("project")), PaperID: c.Param("paperID"),
		Kind: kind, Title: wikiFirstNonEmpty(payload.Title, "用户笔记"), Text: text, Refs: refs,
		Trust: memory.TrustUserExplicit, Status: memory.StatusActive, SourceType: "user",
		SourceRef: "user_note", Importance: 8, Confidence: 1.0, EvidenceClass: "user_explicit",
		Attributes: payload.Attributes,
	})
	if err != nil {
		wikiError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"point": point})
}

type wikiPointPatch struct {
	Title      *string             `json:"title"`
	Text       *string             `json:"text"`
	Status     *memory.Status      `json:"status"`
	Importance *int                `json:"importance"`
	Pinned     *bool               `json:"pinned"`
	Refs       *[]memory.SourceRef `json:"refs"`
	Attributes *map[string]any     `json:"attributes"`
}

func patchWikiPoint(c *gin.Context, builder *wiki.Builder) {
	var payload wikiPointPatch
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payload: " + err.Error()})
		return
	}
	ownerID := resolveUserID(c)
	store := builder.Memory
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "memory store is not configured"})
		return
	}
	record, err := store.Get(c.Request.Context(), ownerID, c.Param("pointID"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "point not found"})
		return
	}
	point, ok := wiki.ParsePoint(record)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "point not found"})
		return
	}
	if payload.Refs != nil {
		if err := validatePointRefs(c.Request.Context(), builder, ownerID, *payload.Refs); err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "quote_not_located", "detail": err.Error()})
			return
		}
	}
	patch := memory.Patch{Title: payload.Title, Content: payload.Text, Status: payload.Status, Importance: payload.Importance, Pinned: payload.Pinned, Refs: payload.Refs, Attributes: payload.Attributes}
	updated, err := store.Update(c.Request.Context(), ownerID, record.ID, patch)
	if err != nil {
		wikiError(c, err)
		return
	}
	_, _ = builder.RefreshPaper(c.Request.Context(), ownerID, point.PaperID)
	c.JSON(http.StatusOK, gin.H{"point": updated})
}

func deleteWikiPoint(c *gin.Context, builder *wiki.Builder) {
	ownerID := resolveUserID(c)
	store := builder.Memory
	if store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "memory store is not configured"})
		return
	}
	record, err := store.Get(c.Request.Context(), ownerID, c.Param("pointID"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "point not found"})
		return
	}
	point, ok := wiki.ParsePoint(record)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "point not found"})
		return
	}
	if err := store.Delete(c.Request.Context(), ownerID, record.ID, false); err != nil {
		wikiError(c, err)
		return
	}
	_, _ = builder.RefreshPaper(c.Request.Context(), ownerID, point.PaperID)
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

func listWikiPaperLinks(c *gin.Context, builder *wiki.Builder) {
	listWikiLinksWithPaper(c, builder, c.Param("paperID"))
}

func listWikiLinks(c *gin.Context, builder *wiki.Builder) {
	listWikiLinksWithPaper(c, builder, "")
}

func listWikiLinksWithPaper(c *gin.Context, builder *wiki.Builder, paperID string) {
	limit := 50
	if parsed, err := strconv.Atoi(c.Query("limit")); err == nil && parsed > 0 {
		limit = parsed
	}
	offset := 0
	if parsed, err := strconv.Atoi(c.Query("cursor")); err == nil && parsed > 0 {
		offset = parsed
	}
	linkType := wiki.LinkType(strings.TrimSpace(c.Query("type")))
	links, total, err := builder.ListLinks(c.Request.Context(), resolveUserID(c), paperID, linkType, strings.TrimSpace(c.Query("status")), limit, offset)
	if err != nil {
		wikiError(c, err)
		return
	}
	next := offset + len(links)
	if next >= total {
		next = 0
	}
	c.JSON(http.StatusOK, gin.H{"links": links, "total": total, "next_cursor": next})
}

func confirmWikiLink(c *gin.Context, builder *wiki.Builder) {
	ownerID := resolveUserID(c)
	link, err := builder.ConfirmLink(c.Request.Context(), ownerID, c.Param("linkID"))
	if err != nil {
		wikiError(c, err)
		return
	}
	_ = builder.RefreshPaperLinks(c.Request.Context(), ownerID, link.FromPaper)
	_ = builder.RefreshPaperLinks(c.Request.Context(), ownerID, link.ToPaper)
	c.JSON(http.StatusOK, gin.H{"link": link})
}

type wikiRejectPayload struct {
	Reason string `json:"reason"`
}

func rejectWikiLink(c *gin.Context, builder *wiki.Builder) {
	var payload wikiRejectPayload
	_ = c.ShouldBindJSON(&payload)
	ownerID := resolveUserID(c)
	link, err := builder.RejectLink(c.Request.Context(), ownerID, c.Param("linkID"), payload.Reason)
	if err != nil {
		wikiError(c, err)
		return
	}
	_ = builder.RefreshPaperLinks(c.Request.Context(), ownerID, link.FromPaper)
	_ = builder.RefreshPaperLinks(c.Request.Context(), ownerID, link.ToPaper)
	c.JSON(http.StatusOK, gin.H{"link": link})
}

type wikiBuildJob struct {
	ID         string     `json:"job_id"`
	Mode       string     `json:"mode"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Papers     int        `json:"papers"`
	Links      int        `json:"links"`
	Error      string     `json:"error,omitempty"`
}

var wikiBuildState = struct {
	sync.Mutex
	jobs    map[string]*wikiBuildJob
	running bool
}{jobs: map[string]*wikiBuildJob{}}

func startWikiBuild(c *gin.Context, builder *wiki.Builder) {
	var payload struct {
		Mode string `json:"mode"`
	}
	_ = c.ShouldBindJSON(&payload)
	mode := strings.TrimSpace(payload.Mode)
	if mode == "" {
		mode = "incremental"
	}
	wikiBuildState.Lock()
	if wikiBuildState.running {
		wikiBuildState.Unlock()
		c.JSON(http.StatusConflict, gin.H{"error": "wiki_build_in_progress"})
		return
	}
	job := &wikiBuildJob{ID: "wiki-build-" + strconv.FormatInt(time.Now().UnixNano(), 36), Mode: mode, Status: "running", StartedAt: time.Now().UTC()}
	wikiBuildState.jobs[job.ID] = job
	wikiBuildState.running = true
	wikiBuildState.Unlock()
	ownerID := resolveUserID(c)
	go func() {
		papers, err := builder.RebuildOwner(context.Background(), ownerID)
		links := 0
		if err == nil && mode == "full" {
			links, err = builder.RebuildLinksForOwner(context.Background(), ownerID)
		}
		finished := time.Now().UTC()
		wikiBuildState.Lock()
		job.Papers = papers
		job.Links = links
		job.FinishedAt = &finished
		job.Status = "completed"
		if err != nil {
			job.Status = "failed"
			job.Error = err.Error()
		}
		wikiBuildState.running = false
		wikiBuildState.Unlock()
	}()
	c.JSON(http.StatusAccepted, gin.H{"job_id": job.ID, "status": job.Status})
}

func getWikiBuild(c *gin.Context) {
	wikiBuildState.Lock()
	job, ok := wikiBuildState.jobs[c.Param("jobID")]
	wikiBuildState.Unlock()
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "build job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func wikiStats(c *gin.Context, builder *wiki.Builder) {
	ownerID := resolveUserID(c)
	stats := builder.Store.Stats(c.Request.Context(), ownerID)
	store := builder.Memory
	if store != nil {
		records, _, err := store.List(c.Request.Context(), memory.Query{OwnerID: ownerID, Tags: []string{wiki.TagWikiPoint}, IncludePending: true, Limit: 5000})
		if err == nil {
			stats.Points = len(records)
			for _, record := range records {
				stats.ByKind[string(wiki.PointKindFromRecord(record))]++
			}
		}
		links, _, linkErr := store.List(c.Request.Context(), memory.Query{OwnerID: ownerID, Tags: []string{wiki.TagWikiLink}, Statuses: []memory.Status{memory.StatusActive, memory.StatusPending, memory.StatusArchived, memory.StatusSuperseded}, Limit: 5000})
		if linkErr == nil {
			stats.Links = len(links)
			for _, record := range links {
				if record.Status == memory.StatusPending {
					stats.PendingLinks++
				}
			}
		}
	}
	c.JSON(http.StatusOK, stats)
}

func wikiFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func mergePaperEntities(existing, additions []memory.Entity) []memory.Entity {
	seen := map[string]bool{}
	out := make([]memory.Entity, 0, len(existing)+len(additions))
	for _, entity := range append(append([]memory.Entity{}, existing...), additions...) {
		key := strings.ToLower(strings.TrimSpace(entity.Type)) + "|" + strings.ToLower(strings.TrimSpace(entity.Value))
		if strings.TrimSpace(entity.Value) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entity)
	}
	return out
}

// validatePointRefs checks that every quote actually exists in its document.
func validatePointRefs(ctx context.Context, builder *wiki.Builder, ownerID string, refs []memory.SourceRef) error {
	if builder == nil || builder.Documents == nil {
		return nil
	}
	for _, ref := range refs {
		if strings.TrimSpace(ref.DocumentID) == "" || strings.TrimSpace(ref.Quote) == "" {
			continue
		}
		document, err := builder.Documents.GetDocument(ctx, ownerID, ref.DocumentID)
		if err != nil {
			continue
		}
		if _, _, _, ok := documents.LocateQuote(document, ref.Quote); !ok {
			return fmt.Errorf("quote not located in document %s", ref.DocumentID)
		}
	}
	return nil
}
