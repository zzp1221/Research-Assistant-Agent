package wiki

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/memory"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/paperid"
)

// Builder materializes PaperPage projections from memory, documents and the
// literature library. It never calls an LLM for deterministic fields.
type Builder struct {
	Store     *PaperStore
	Memory    memory.Store
	Documents *documents.Store
	Library   *literature.LibraryStore
	Aliases   *AliasStore
	Citations CitationProvider
	// LLM optionally generates TLDR/summary points and link adjudications (W3).
	LLM LLM
}

// NewBuilder wires the wiki builder.
func NewBuilder(store *PaperStore, mem memory.Store, docs *documents.Store, library *literature.LibraryStore, aliases *AliasStore) *Builder {
	return &Builder{Store: store, Memory: mem, Documents: docs, Library: library, Aliases: aliases}
}

// EnsurePaper resolves identity, merges metadata and refreshes the projection.
// documentID may be empty when the paper is created from a title/DOI only.
func (b *Builder) EnsurePaper(ctx context.Context, ownerID, projectID string, meta models.PaperMetadata, documentID string) (PaperPage, error) {
	if b == nil || b.Store == nil || b.Aliases == nil {
		return PaperPage{}, fmt.Errorf("wiki builder is not configured")
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return PaperPage{}, fmt.Errorf("owner_id is required")
	}
	record := b.findLibraryRecord(ownerID, meta)
	paperID, canonicalKey, err := b.Aliases.Resolve(ownerID, meta, record)
	if err != nil {
		return PaperPage{}, err
	}
	page, getErr := b.Store.Get(ctx, ownerID, paperID)
	if getErr != nil {
		page = PaperPage{PaperID: paperID, OwnerID: ownerID, ProjectID: projectID}
	}
	page.CanonicalKey = canonicalKey
	if projectID != "" {
		page.ProjectID = projectID
	}
	page.Title = firstNonEmpty(meta.Title, recordTitle(record), page.Title)
	page.Authors = firstNonEmptySlice(meta.Authors, recordAuthors(record), page.Authors)
	page.Year = firstNonZero(meta.Year, recordYear(record), page.Year)
	page.Venue = firstNonEmpty(meta.Venue, recordVenue(record), page.Venue)
	page.ArxivID = firstNonEmpty(paperidStrip(meta.ArxivID), paperidStrip(recordArxiv(record)), page.ArxivID)
	page.DOI = firstNonEmpty(normalizeDOI(meta.DOI), normalizeDOI(recordDOI(record)), page.DOI)
	page.Abstract = firstNonEmpty(meta.Abstract, recordAbstract(record), page.Abstract)
	if strings.TrimSpace(page.Title) == "" {
		return PaperPage{}, fmt.Errorf("paper title is required")
	}
	if memory.IsHiddenText(page.Title) || memory.IsHiddenText(page.Abstract) || memory.IsHiddenText(strings.Join(page.Authors, " ")) {
		return PaperPage{}, fmt.Errorf("hidden benchmark paper cannot enter the wiki")
	}
	if documentID != "" {
		page.DocumentIDs = mergeStrings(page.DocumentIDs, []string{documentID})
		if sections, sectionErr := b.loadSections(ctx, ownerID, documentID); sectionErr == nil && len(sections) > 0 {
			page.Sections = sections
		}
	}
	page.Entities = mergeEntities(page.Entities, paperEntities(meta, record))
	page.Reproduction = ensureReproduction(page.Reproduction)
	saved, err := b.Store.Put(ctx, page)
	if err != nil {
		return PaperPage{}, err
	}
	// Restore a previously generated TLDR first: rebuilding data/wiki must not
	// call the LLM again (the TLDR is persisted as a wiki_tldr memory record).
	if strings.TrimSpace(saved.TLDR) == "" {
		if tldr, ok := b.loadTLDR(ctx, ownerID, saved.PaperID); ok {
			saved.TLDR = tldr
			saved.TLDRInferred = true
			if updated, putErr := b.Store.Put(ctx, saved); putErr == nil {
				saved = updated
			}
		}
	}
	if strings.TrimSpace(saved.TLDR) == "" && b.LLM != nil && SummaryEnabled() {
		var document *documents.Document
		if documentID != "" && b.Documents != nil {
			document, _ = b.Documents.GetDocument(ctx, ownerID, documentID)
		}
		if summarizeErr := b.maybeSummarizePaper(ctx, ownerID, &saved, documentID, document); summarizeErr == nil {
			if updated, putErr := b.Store.Put(ctx, saved); putErr == nil {
				saved = updated
			}
		}
	}
	return b.RefreshPaper(ctx, ownerID, saved.PaperID)
}

func (b *Builder) loadSections(ctx context.Context, ownerID, documentID string) ([]documents.SectionInfo, error) {
	if b.Documents == nil {
		return nil, fmt.Errorf("document store is not configured")
	}
	sections, err := b.Documents.Outline(ctx, ownerID, documentID)
	if err != nil {
		return nil, err
	}
	if len(sections) > 20 {
		sections = sections[:20]
	}
	return sections, nil
}

// RefreshPaper recomputes point ids and revision from the memory store.
func (b *Builder) RefreshPaper(ctx context.Context, ownerID, paperID string) (PaperPage, error) {
	page, err := b.Store.Get(ctx, ownerID, paperID)
	if err != nil {
		return PaperPage{}, err
	}
	if b.Memory == nil {
		return page, nil
	}
	records, _, err := b.Memory.List(ctx, memory.Query{
		OwnerID:        ownerID,
		Tags:           []string{TagWikiPoint, PaperTag(paperID)},
		IncludePending: true,
		AllScopes:      true,
		Limit:          2000,
	})
	if err != nil {
		return PaperPage{}, err
	}
	ids := make([]string, 0, len(records))
	revision := page.Revision
	for _, record := range records {
		if record.Status == memory.StatusDeleted {
			continue
		}
		ids = append(ids, record.ID)
		if record.Version > revision {
			revision = record.Version
		}
	}
	sort.Strings(ids)
	page.PointIDs = ids
	page.Revision = revision
	return b.Store.Put(ctx, page)
}

// RefreshForPlan refreshes every paper referenced by a plan and derives the
// reproduction status from the plan outcome.
func (b *Builder) RefreshForPlan(ctx context.Context, plan *models.PlanGraph) error {
	if b == nil || plan == nil || b.Store == nil || strings.TrimSpace(plan.OwnerID) == "" {
		return nil
	}
	papers := b.papersForPlan(ctx, plan.OwnerID, plan)
	for _, paperID := range papers {
		page, err := b.Store.Get(ctx, plan.OwnerID, paperID)
		if err != nil {
			// A plan that carries paper metadata but never ran paper_parse still
			// deserves a PaperPage: create it from the plan context (and the paper
			// attachment when present) without re-parsing the PDF.
			if plan.Context == nil || plan.Context.Paper == nil {
				continue
			}
			documentID := ""
			for _, attachment := range plan.Context.Attachments {
				if attachment.Role == "paper" && attachment.DocumentID != "" {
					documentID = attachment.DocumentID
					break
				}
			}
			page, err = b.EnsurePaper(ctx, plan.OwnerID, plan.ProjectID, *plan.Context.Paper, documentID)
			if err != nil {
				continue
			}
		}
		applyPlanReproduction(&page, plan)
		if _, err := b.Store.Put(ctx, page); err != nil {
			return err
		}
		if _, err := b.RefreshPaper(ctx, plan.OwnerID, paperID); err != nil {
			return err
		}
	}
	return nil
}

func (b *Builder) papersForPlan(ctx context.Context, ownerID string, plan *models.PlanGraph) []string {
	out := make([]string, 0, 2)
	if b.Memory != nil {
		records, _, err := b.Memory.List(ctx, memory.Query{OwnerID: ownerID, Tags: []string{TagWikiPoint}, IncludePending: true, AllScopes: true, Limit: 2000})
		if err == nil {
			for _, record := range records {
				if record.Scope.PlanID != "" && record.Scope.PlanID != plan.ID {
					continue
				}
				if paperID := PaperIDFromRecord(record); paperID != "" && !containsString(out, paperID) {
					out = append(out, paperID)
				}
			}
		}
	}
	if plan.Context != nil && plan.Context.Paper != nil && b.Aliases != nil {
		if paperID, _, err := b.Aliases.Resolve(ownerID, *plan.Context.Paper, nil); err == nil && paperID != "" && !containsString(out, paperID) {
			out = append(out, paperID)
		}
	}
	return out
}

func applyPlanReproduction(page *PaperPage, plan *models.PlanGraph) {
	page.Reproduction = ensureReproduction(page.Reproduction)
	if plan == nil {
		return
	}
	status := ""
	switch plan.Outcome {
	case models.OutcomeCompleted:
		status = "succeeded"
	case models.OutcomeFailed:
		status = "failed"
	case models.OutcomeBlocked:
		status = "blocked"
	case models.OutcomeNeedsReview:
		status = "running"
	default:
		if plan.Status == models.StatusCanceled {
			status = "cancelled"
		}
	}
	if status != "" {
		page.Reproduction.Status = status
		page.Reproduction.LastPlanID = plan.ID
	}
	if plan.Outcome == models.OutcomeFailed || plan.Outcome == models.OutcomeBlocked {
		page.Reproduction.LastError = truncate(plan.OutcomeReason, 300)
	}
}

func ensureReproduction(status ReproductionStatus) ReproductionStatus {
	if strings.TrimSpace(status.Status) == "" {
		status.Status = "none"
	}
	return status
}

// RebuildOwner re-derives every PaperPage for one owner from memory + documents.
// Point ids and links are rebuilt; user confirm/reject decisions survive because
// Link records live in memory. This is what makes deleting data/wiki safe.
func (b *Builder) RebuildOwner(ctx context.Context, ownerID string) (int, error) {
	if b.Memory != nil {
		if err := b.rebuildPagesFromMemory(ctx, ownerID); err != nil {
			return 0, err
		}
	}
	pages, err := b.Store.ListAll(ctx, ownerID)
	if err != nil {
		return 0, err
	}
	index, err := b.loadLinkIndex(ctx, ownerID)
	if err != nil {
		return 0, err
	}
	for _, page := range pages {
		if _, err := b.RefreshPaper(ctx, ownerID, page.PaperID); err != nil {
			return 0, err
		}
		if err := b.refreshPaperLinksFromIndex(ctx, ownerID, page.PaperID, index); err != nil {
			return 0, err
		}
	}
	return len(pages), nil
}

// rebuildPagesFromMemory recreates missing PaperPages from the deterministic
// paper metadata facts that memory.RecordPlan writes. No PDF parsing and no LLM
// call is involved.
func (b *Builder) rebuildPagesFromMemory(ctx context.Context, ownerID string) error {
	records, _, err := b.Memory.List(ctx, memory.Query{OwnerID: ownerID, Tags: []string{"paper", "metadata"}, IncludePending: true, AllScopes: true, Limit: 5000})
	if err != nil {
		return err
	}
	documentsByTitle := map[string]string{}
	docRecords, _, docErr := b.Memory.List(ctx, memory.Query{OwnerID: ownerID, Tags: []string{"document", "sections"}, IncludePending: true, AllScopes: true, Limit: 5000})
	if docErr == nil {
		for _, record := range docRecords {
			for _, entity := range record.Entities {
				if entity.Type == "document" {
					title := strings.TrimPrefix(record.Title, "文档：")
					documentsByTitle[literature.NormalizeTitle(title)] = entity.Value
				}
			}
		}
	}
	for _, record := range records {
		meta := parsePaperFact(record.Content)
		if strings.TrimSpace(meta.Title) == "" {
			continue
		}
		documentID := documentsByTitle[literature.NormalizeTitle(meta.Title)]
		if _, err := b.EnsurePaper(ctx, ownerID, record.Scope.ProjectID, meta, documentID); err != nil {
			continue
		}
	}
	return nil
}

// parsePaperFact parses the bounded metadata fact written by memory.RecordPlan.
func parsePaperFact(content string) models.PaperMetadata {
	meta := models.PaperMetadata{}
	for _, part := range strings.Split(content, "；") {
		key, value, ok := strings.Cut(part, "：")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "标题":
			meta.Title = value
		case "作者":
			for _, author := range strings.Split(value, ", ") {
				if strings.TrimSpace(author) != "" {
					meta.Authors = append(meta.Authors, strings.TrimSpace(author))
				}
			}
		case "年份":
			fmt.Sscanf(value, "%d", &meta.Year)
		case "会议":
			meta.Venue = value
		case "DOI":
			meta.DOI = value
		case "arXiv":
			meta.ArxivID = value
		case "仓库":
			meta.RepoURL = value
		}
	}
	return meta
}

func (b *Builder) findLibraryRecord(ownerID string, meta models.PaperMetadata) *literature.Record {
	if b.Library == nil {
		return nil
	}
	if doi := normalizeDOI(meta.DOI); doi != "" {
		if record, ok, err := b.Library.FindByDOI(ownerID, doi); err == nil && ok {
			return &record
		}
	}
	if arxiv := paperidStrip(meta.ArxivID); arxiv != "" {
		if record, ok, err := b.Library.FindByArxiv(ownerID, arxiv); err == nil && ok {
			return &record
		}
	}
	title := literature.NormalizeTitle(meta.Title)
	if title == "" {
		return nil
	}
	records, err := b.Library.List(ownerID, meta.Title, "", 20)
	if err != nil {
		return nil
	}
	for index := range records {
		if literature.NormalizeTitle(records[index].Title) != title {
			continue
		}
		if meta.Year == 0 || records[index].Year == 0 || meta.Year == records[index].Year {
			return &records[index]
		}
	}
	return nil
}

func paperEntities(meta models.PaperMetadata, record *literature.Record) []memory.Entity {
	entities := make([]memory.Entity, 0, 6)
	if doi := firstNonEmpty(normalizeDOI(meta.DOI), normalizeDOI(recordDOI(record))); doi != "" {
		entities = append(entities, memory.Entity{Type: "paper", Value: "doi:" + doi})
	}
	if arxiv := firstNonEmpty(paperidStrip(meta.ArxivID), paperidStrip(recordArxiv(record))); arxiv != "" {
		entities = append(entities, memory.Entity{Type: "paper", Value: "arxiv:" + strings.ToLower(arxiv)})
	}
	if title := firstNonEmpty(meta.Title, recordTitle(record)); title != "" {
		entities = append(entities, memory.Entity{Type: "paper", Value: literature.NormalizeTitle(title)})
	}
	if repo := firstNonEmpty(meta.RepoURL, recordRepo(record)); repo != "" {
		entities = append(entities, memory.Entity{Type: "repo", Value: normalizeRepo(repo)})
	}
	return dedupeEntities(entities)
}

func normalizeRepo(raw string) string {
	repo := strings.ToLower(strings.TrimSpace(raw))
	repo = strings.TrimSuffix(repo, ".git")
	repo = strings.TrimPrefix(repo, "https://")
	repo = strings.TrimPrefix(repo, "http://")
	repo = strings.TrimPrefix(repo, "git@")
	repo = strings.ReplaceAll(repo, ":", "/")
	return repo
}

func mergeEntities(existing, additions []memory.Entity) []memory.Entity {
	out := append(append([]memory.Entity{}, existing...), additions...)
	return dedupeEntities(out)
}

func dedupeEntities(entities []memory.Entity) []memory.Entity {
	seen := map[string]bool{}
	out := make([]memory.Entity, 0, len(entities))
	for _, entity := range entities {
		key := strings.ToLower(strings.TrimSpace(entity.Type)) + "|" + strings.ToLower(strings.TrimSpace(entity.Value))
		if strings.TrimSpace(entity.Value) == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, entity)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type == out[j].Type {
			return out[i].Value < out[j].Value
		}
		return out[i].Type < out[j].Type
	})
	return out
}

func paperidStrip(raw string) string {
	return paperid.StripVersion(strings.TrimSpace(raw))
}

func recordAuthors(record *literature.Record) []string {
	if record == nil {
		return nil
	}
	return record.Authors
}

func recordVenue(record *literature.Record) string {
	if record == nil {
		return ""
	}
	return record.Venue
}

func recordAbstract(record *literature.Record) string {
	if record == nil {
		return ""
	}
	return record.Abstract
}

func recordRepo(record *literature.Record) string {
	if record == nil {
		return ""
	}
	return record.URL
}

func firstNonEmptySlice(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func truncate(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit])
}

// Now returns the builder clock, kept as a hook for deterministic tests.
func (b *Builder) Now() time.Time { return time.Now().UTC() }
