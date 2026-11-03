package wiki

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"research-assistant-backend/internal/documents"
	"research-assistant-backend/internal/literature"
	"research-assistant-backend/internal/memory"
)

// CitationProvider resolves a DOI's citation neighborhood. The production
// implementation is literature.Client; tests inject a fake.
type CitationProvider interface {
	Citations(ctx context.Context, doi string, limit int) (*literature.CitationGraph, error)
}

// LinkCandidate is one candidate before persistence.
type LinkCandidate struct {
	FromPaper  string
	ToPaper    string
	Type       LinkType
	Evidence   []memory.SourceRef
	Source     string
	Confidence float64
}

// LinkFromRecord parses a wiki_link memory record.
func LinkFromRecord(record memory.Record) (Link, bool) {
	if !hasTag(record.Tags, TagWikiLink) {
		return Link{}, false
	}
	attributes := record.Attributes
	linkType := LinkType(attributeString(attributes, "link_type"))
	if linkType == "" {
		for _, tag := range record.Tags {
			if strings.HasPrefix(tag, linkTagPrefix) {
				linkType = LinkType(strings.TrimPrefix(tag, linkTagPrefix))
				break
			}
		}
	}
	if !linkType.Valid() {
		return Link{}, false
	}
	from := attributeString(attributes, "from_paper")
	to := attributeString(attributes, "to_paper")
	if from == "" || to == "" {
		return Link{}, false
	}
	confidence := attributeFloat(attributes, "confidence")
	return Link{
		ID: record.ID, OwnerID: record.Scope.OwnerID, ProjectID: record.Scope.ProjectID,
		FromPaper: from, ToPaper: to, Type: linkType, Evidence: record.Refs,
		Source: attributeString(attributes, "source"), Confidence: confidence,
		Status: record.Status, Revision: record.Version,
		SupersededBy: record.Validity.SupersededBy,
		CreatedAt:    record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}, true
}

func attributeString(attributes map[string]any, key string) string {
	if attributes == nil {
		return ""
	}
	value, _ := attributes[key].(string)
	return strings.TrimSpace(value)
}

func attributeFloat(attributes map[string]any, key string) float64 {
	if attributes == nil {
		return 0
	}
	switch typed := attributes[key].(type) {
	case float64:
		return typed
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	}
	return 0
}

// BuildLinksForPaper generates deterministic links for one paper: cites from
// the citation provider, uses_same_dataset/method/repo from entity overlap.
func (b *Builder) BuildLinksForPaper(ctx context.Context, ownerID, paperID string) ([]Link, error) {
	if b == nil || b.Memory == nil {
		return nil, fmt.Errorf("wiki link store is not configured")
	}
	page, err := b.Store.Get(ctx, ownerID, paperID)
	if err != nil {
		return nil, err
	}
	allPages, _, err := b.Store.List(ctx, PaperQuery{OwnerID: ownerID, Status: "active", Limit: 200})
	if err != nil {
		return nil, err
	}
	candidates := make([]LinkCandidate, 0, 32)
	candidates = append(candidates, b.entityCandidates(page, allPages)...)
	candidates = append(candidates, b.pdfReferenceCandidates(ctx, ownerID, page, allPages)...)
	if LinkAdjudicationEnabled() && b.LLM != nil {
		pairs := map[string]bool{}
		for _, candidate := range candidates {
			pairs[candidate.FromPaper+"|"+candidate.ToPaper] = true
			pairs[candidate.ToPaper+"|"+candidate.FromPaper] = true
		}
		for _, candidate := range b.textSimilarityCandidates(page, allPages) {
			if pairs[candidate.FromPaper+"|"+candidate.ToPaper] {
				continue
			}
			target := findPaperByID(allPages, candidate.ToPaper)
			if target == nil {
				continue
			}
			if adjudicated, ok := b.adjudicateLink(ctx, ownerID, page, *target); ok {
				candidate = adjudicated
			}
			candidates = append(candidates, candidate)
			pairs[candidate.FromPaper+"|"+candidate.ToPaper] = true
			pairs[candidate.ToPaper+"|"+candidate.FromPaper] = true
		}
	}
	candidates = append(candidates, b.citationCandidates(ctx, page, allPages)...)
	links := make([]Link, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if candidate.FromPaper == candidate.ToPaper {
			continue
		}
		normalizeCandidate(&candidate)
		key := linkStableKey(ownerID, candidate)
		if seen[key] {
			continue
		}
		seen[key] = true
		link, writeErr := b.writeLink(ctx, ownerID, page.ProjectID, candidate)
		if writeErr != nil {
			return nil, writeErr
		}
		links = append(links, link)
	}
	if err := b.refreshPaperLinks(ctx, ownerID, paperID); err != nil {
		return nil, err
	}
	return links, nil
}

func (b *Builder) entityCandidates(page PaperPage, pages []PaperPage) []LinkCandidate {
	out := make([]LinkCandidate, 0, 16)
	for _, entity := range page.Entities {
		if strings.TrimSpace(entity.Value) == "" {
			continue
		}
		linkType, ok := entityLinkType(entity.Type)
		if !ok {
			continue
		}
		matches := 0
		for _, other := range pages {
			if other.PaperID == page.PaperID {
				continue
			}
			if !paperHasEntity(other, entity) {
				continue
			}
			matches++
			if matches > 500 {
				break
			}
			out = append(out, LinkCandidate{
				FromPaper: page.PaperID, ToPaper: other.PaperID, Type: linkType,
				Evidence: []memory.SourceRef{entityEvidence(page, entity)},
				Source:   "entity_overlap", Confidence: 0.7,
			})
		}
	}
	return out
}

func entityLinkType(entityType string) (LinkType, bool) {
	switch strings.ToLower(strings.TrimSpace(entityType)) {
	case "dataset":
		return LinkSameDataset, true
	case "method", "model":
		return LinkSameMethod, true
	case "repo":
		return LinkSameRepo, true
	}
	return "", false
}

func paperHasEntity(page PaperPage, wanted memory.Entity) bool {
	for _, entity := range page.Entities {
		if strings.EqualFold(entity.Type, wanted.Type) && strings.EqualFold(entity.Value, wanted.Value) {
			return true
		}
	}
	return false
}

func entityEvidence(page PaperPage, entity memory.Entity) memory.SourceRef {
	ref := memory.SourceRef{Section: "metadata", Quote: truncate("entity:"+entity.Type+"="+entity.Value, 500)}
	if len(page.DocumentIDs) > 0 {
		ref.DocumentID = page.DocumentIDs[0]
	}
	return ref
}

func (b *Builder) citationCandidates(ctx context.Context, page PaperPage, pages []PaperPage) []LinkCandidate {
	if b.Citations == nil || strings.TrimSpace(page.DOI) == "" {
		return nil
	}
	graph, err := b.Citations.Citations(ctx, page.DOI, 50)
	if err != nil || graph == nil {
		return nil
	}
	byDOI := map[string]PaperPage{}
	for _, other := range pages {
		if other.DOI != "" {
			byDOI[normalizeDOI(other.DOI)] = other
		}
	}
	out := make([]LinkCandidate, 0, len(graph.References))
	for _, reference := range graph.References {
		target, ok := byDOI[normalizeDOI(reference.DOI)]
		if !ok || target.PaperID == page.PaperID {
			continue
		}
		out = append(out, LinkCandidate{
			FromPaper: page.PaperID, ToPaper: target.PaperID, Type: LinkCites,
			Evidence: []memory.SourceRef{{Section: "references", Quote: truncate("reference:"+reference.Title, 500)}},
			Source:   "openalex", Confidence: 0.95,
		})
	}
	return out
}

func normalizeCandidate(candidate *LinkCandidate) {
	if !candidate.Type.Symmetric() {
		return
	}
	if candidate.FromPaper > candidate.ToPaper {
		candidate.FromPaper, candidate.ToPaper = candidate.ToPaper, candidate.FromPaper
	}
}

func linkStableKey(ownerID string, candidate LinkCandidate) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{strings.TrimSpace(ownerID), candidate.FromPaper, candidate.ToPaper, string(candidate.Type)}, "|")))
	return hex.EncodeToString(sum[:])[:24]
}

func (b *Builder) writeLink(ctx context.Context, ownerID, projectID string, candidate LinkCandidate) (Link, error) {
	key := linkStableKey(ownerID, candidate)
	existing, err := b.linksForKey(ctx, ownerID, key)
	if err != nil {
		return Link{}, err
	}
	revision := 1
	signature := linkSignature(candidate)
	for _, record := range existing {
		if record.Revision >= revision {
			revision = record.Revision + 1
		}
		if record.Link.Status == memory.StatusActive && record.Signature == signature {
			return record.Link, nil
		}
	}
	status := memory.StatusActive
	if candidate.Confidence < 0.6 || candidate.Source == "llm" {
		status = memory.StatusPending
	}
	record := memory.Record{
		ID:      fmt.Sprintf("link-%s-r%d", key, revision),
		Kind:    memory.KindFact,
		Scope:   memory.Scope{OwnerID: ownerID, ProjectID: projectID},
		Title:   truncate(fmt.Sprintf("%s: %s ↔ %s", candidate.Type, candidate.FromPaper, candidate.ToPaper), 120),
		Content: truncate(fmt.Sprintf("关系：%s；来源：%s；置信度：%.2f", candidate.Type, candidate.Source, candidate.Confidence), 500),
		Tags:    []string{TagWikiLink, LinkTag(candidate.Type)},
		Entities: []memory.Entity{
			{Type: "paper", Value: candidate.FromPaper},
			{Type: "paper", Value: candidate.ToPaper},
		},
		Source:   memory.Source{Type: "wiki_link", Ref: candidate.Source, CreatedAt: time.Now().UTC()},
		Evidence: memory.Evidence{Class: "link_" + candidate.Source, Confidence: candidate.Confidence},
		Refs:     candidate.Evidence,
		Attributes: map[string]any{
			"from_paper": candidate.FromPaper, "to_paper": candidate.ToPaper,
			"link_type": string(candidate.Type), "source": candidate.Source,
			"confidence": candidate.Confidence, "link_key": key,
		},
		Importance: 7, Confidence: candidate.Confidence, Trust: memory.TrustVerified, Status: status,
	}
	saved, err := b.Memory.Put(ctx, record)
	if err != nil {
		return Link{}, err
	}
	// Supersede the previous active revision so history is preserved.
	for _, previous := range existing {
		if previous.Link.Status != memory.StatusActive && previous.Link.Status != memory.StatusPending {
			continue
		}
		superseded := memory.StatusSuperseded
		attributes := previous.Record.Attributes
		if attributes == nil {
			attributes = map[string]any{}
		}
		attributes["superseded_by"] = saved.ID
		if _, updateErr := b.Memory.Update(ctx, ownerID, previous.Record.ID, memory.Patch{Status: &superseded, Attributes: &attributes}); updateErr != nil {
			return Link{}, updateErr
		}
	}
	link, _ := LinkFromRecord(saved)
	return link, nil
}

type existingLink struct {
	Link      Link
	Record    memory.Record
	Revision  int
	Signature string
}

func (b *Builder) linksForKey(ctx context.Context, ownerID, key string) ([]existingLink, error) {
	records, _, err := b.Memory.List(ctx, memory.Query{OwnerID: ownerID, Tags: []string{TagWikiLink}, Statuses: []memory.Status{memory.StatusActive, memory.StatusPending, memory.StatusSuperseded, memory.StatusArchived}, Limit: 5000})
	if err != nil {
		return nil, err
	}
	out := make([]existingLink, 0, 2)
	for _, record := range records {
		if attributeString(record.Attributes, "link_key") != key {
			continue
		}
		link, ok := LinkFromRecord(record)
		if !ok {
			continue
		}
		out = append(out, existingLink{Link: link, Record: record, Revision: record.Version, Signature: linkSignatureFromRecord(record)})
	}
	return out, nil
}

func linkSignature(candidate LinkCandidate) string {
	return strings.Join([]string{candidate.FromPaper, candidate.ToPaper, string(candidate.Type), candidate.Source}, "|")
}

func linkSignatureFromRecord(record memory.Record) string {
	return strings.Join([]string{
		attributeString(record.Attributes, "from_paper"), attributeString(record.Attributes, "to_paper"),
		attributeString(record.Attributes, "link_type"), attributeString(record.Attributes, "source"),
	}, "|")
}

func (b *Builder) refreshPaperLinks(ctx context.Context, ownerID, paperID string) error {
	page, err := b.Store.Get(ctx, ownerID, paperID)
	if err != nil {
		return err
	}
	links, _, err := b.ListLinks(ctx, ownerID, paperID, "", "", 500, 0)
	if err != nil {
		return err
	}
	ids := make([]string, 0, len(links))
	for _, link := range links {
		if link.Status == memory.StatusSuperseded || link.Status == memory.StatusArchived {
			continue
		}
		ids = append(ids, link.ID)
	}
	sort.Strings(ids)
	page.LinkIDs = ids
	_, err = b.Store.Put(ctx, page)
	return err
}

// ListLinks returns owner-scoped links, optionally filtered by paper/type/status.
func (b *Builder) ListLinks(ctx context.Context, ownerID, paperID string, linkType LinkType, status string, limit, offset int) ([]Link, int, error) {
	if b.Memory == nil {
		return nil, 0, fmt.Errorf("wiki link store is not configured")
	}
	tags := []string{TagWikiLink}
	if linkType != "" {
		tags = append(tags, LinkTag(linkType))
	}
	records, _, err := b.Memory.List(ctx, memory.Query{OwnerID: ownerID, Tags: tags, Statuses: []memory.Status{memory.StatusActive, memory.StatusPending, memory.StatusSuperseded, memory.StatusArchived}, Limit: 5000})
	if err != nil {
		return nil, 0, err
	}
	links := make([]Link, 0, len(records))
	for _, record := range records {
		link, ok := LinkFromRecord(record)
		if !ok {
			continue
		}
		if paperID != "" && link.FromPaper != paperID && link.ToPaper != paperID {
			continue
		}
		if status != "" && string(link.Status) != status {
			continue
		}
		links = append(links, link)
	}
	sort.Slice(links, func(i, j int) bool {
		if links[i].Confidence == links[j].Confidence {
			return links[i].ID < links[j].ID
		}
		return links[i].Confidence > links[j].Confidence
	})
	total := len(links)
	if offset >= total {
		return []Link{}, total, nil
	}
	if limit <= 0 {
		limit = 50
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return links[offset:end], total, nil
}

// ConfirmLink moves a pending link to active.
func (b *Builder) ConfirmLink(ctx context.Context, ownerID, linkID string) (Link, error) {
	record, err := b.Memory.Get(ctx, ownerID, linkID)
	if err != nil {
		return Link{}, err
	}
	if !hasTag(record.Tags, TagWikiLink) {
		return Link{}, fmt.Errorf("link not found")
	}
	status := memory.StatusActive
	updated, err := b.Memory.Update(ctx, ownerID, linkID, memory.Patch{Status: &status})
	if err != nil {
		return Link{}, err
	}
	link, _ := LinkFromRecord(updated)
	return link, nil
}

// RejectLink archives a link and records the reason.
func (b *Builder) RejectLink(ctx context.Context, ownerID, linkID, reason string) (Link, error) {
	record, err := b.Memory.Get(ctx, ownerID, linkID)
	if err != nil {
		return Link{}, err
	}
	if !hasTag(record.Tags, TagWikiLink) {
		return Link{}, fmt.Errorf("link not found")
	}
	status := memory.StatusArchived
	attributes := map[string]any{}
	for key, value := range record.Attributes {
		attributes[key] = value
	}
	attributes["reject_reason"] = truncate(reason, 300)
	updated, err := b.Memory.Update(ctx, ownerID, linkID, memory.Patch{Status: &status, Attributes: &attributes})
	if err != nil {
		return Link{}, err
	}
	link, _ := LinkFromRecord(updated)
	return link, nil
}

// RefreshPaperLinks recomputes one paper's link id list after a decision.
func (b *Builder) RefreshPaperLinks(ctx context.Context, ownerID, paperID string) error {
	return b.refreshPaperLinks(ctx, ownerID, paperID)
}

// RebuildLinksForOwner regenerates deterministic links for every paper.
func (b *Builder) RebuildLinksForOwner(ctx context.Context, ownerID string) (int, error) {
	pages, _, err := b.Store.List(ctx, PaperQuery{OwnerID: ownerID, Status: "active", Limit: 200})
	if err != nil {
		return 0, err
	}
	count := 0
	for _, page := range pages {
		links, buildErr := b.BuildLinksForPaper(ctx, ownerID, page.PaperID)
		if buildErr != nil {
			return count, buildErr
		}
		count += len(links)
	}
	return count, nil
}

// textSimilarityCandidates produces low-confidence related candidates from
// title/abstract token overlap. They are only used when WIKI_LLM_MODE allows
// link adjudication; the LLM may upgrade the type, otherwise they stay pending.
func (b *Builder) textSimilarityCandidates(page PaperPage, pages []PaperPage) []LinkCandidate {
	tokens := similarityTokens(page.Title + " " + page.Abstract)
	if len(tokens) == 0 {
		return nil
	}
	out := make([]LinkCandidate, 0, 3)
	for _, other := range pages {
		if other.PaperID == page.PaperID {
			continue
		}
		score := jaccard(tokens, similarityTokens(other.Title+" "+other.Abstract))
		if score < 0.12 {
			continue
		}
		confidence := 0.3 + score
		if confidence > 0.5 {
			confidence = 0.5
		}
		out = append(out, LinkCandidate{
			FromPaper: page.PaperID, ToPaper: other.PaperID, Type: LinkRelated,
			Evidence: []memory.SourceRef{{Section: "abstract", Quote: truncate("text_similarity:"+other.Title, 500)}},
			Source:   "text_similarity", Confidence: confidence,
		})
		if len(out) >= 3 {
			break
		}
	}
	return out
}

func similarityTokens(text string) map[string]bool {
	tokens := map[string]bool{}
	for _, field := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len([]rune(field)) >= 3 {
			tokens[field] = true
		}
	}
	return tokens
}

func jaccard(left, right map[string]bool) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	intersection := 0
	for token := range left {
		if right[token] {
			intersection++
		}
	}
	union := len(left) + len(right) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func findPaperByID(pages []PaperPage, paperID string) *PaperPage {
	for index := range pages {
		if pages[index].PaperID == paperID {
			return &pages[index]
		}
	}
	return nil
}

const linkAdjudicationSystemPrompt = "你是论文关系裁决器。只输出严格 JSON，不要输出 Markdown 代码块或解释。\n" +
	"JSON schema: {\"type\":\"extends|contradicts|related\",\"confidence\":0.0,\"evidence_quote\":\"逐字原文摘录\"}\n" +
	"type 定义：extends=A 在 B 基础上改进；contradicts=A 与 B 的结论或指标冲突；related=一般相关。\n" +
	"不确定时使用 related 且 confidence<=0.5。evidence_quote 必须逐字来自 A 或 B 的原文，不得编造。"

type linkAdjudicationPayload struct {
	Type          string  `json:"type"`
	Confidence    float64 `json:"confidence"`
	EvidenceQuote string  `json:"evidence_quote"`
}

// adjudicateLink asks the LLM to classify one ambiguous pair. The returned
// candidate is always pending_review because Source=="llm".
func (b *Builder) adjudicateLink(ctx context.Context, ownerID string, from, to PaperPage) (LinkCandidate, bool) {
	if b == nil || b.LLM == nil || !LinkAdjudicationEnabled() {
		return LinkCandidate{}, false
	}
	user := fmt.Sprintf("论文A：%s\n摘要：%s\n\n论文B：%s\n摘要：%s",
		from.Title, truncate(from.Abstract, 1500), to.Title, truncate(to.Abstract, 1500))
	raw, err := b.LLM.Complete(ctx, ownerID, linkAdjudicationSystemPrompt, user)
	if err != nil {
		return LinkCandidate{}, false
	}
	payload, parseErr := parseLinkAdjudicationJSON(raw)
	if parseErr != nil {
		return LinkCandidate{}, false
	}
	linkType := LinkType(payload.Type)
	if linkType != LinkExtends && linkType != LinkContradicts && linkType != LinkRelated {
		return LinkCandidate{}, false
	}
	confidence := payload.Confidence
	if confidence < 0.3 {
		confidence = 0.3
	}
	if confidence > 0.9 {
		confidence = 0.9
	}
	evidence := []memory.SourceRef{}
	if quote := strings.TrimSpace(payload.EvidenceQuote); quote != "" {
		if document := b.documentForPaper(ctx, ownerID, from); document != nil {
			if location := documents.LocateQuoteLocation(document, quote); location.Found {
				evidence = append(evidence, memory.SourceRef{
					DocumentID: firstNonEmpty(documentIDOf(from), ""),
					BlockID:    location.BlockID,
					Page:       location.Page,
					Quote:      truncate(quote, 500),
				})
			}
		}
	}
	return LinkCandidate{
		FromPaper: from.PaperID, ToPaper: to.PaperID, Type: linkType,
		Evidence: evidence, Source: "llm", Confidence: confidence,
	}, true
}

func parseLinkAdjudicationJSON(raw string) (linkAdjudicationPayload, error) {
	var payload linkAdjudicationPayload
	text := strings.TrimSpace(raw)
	fence := "\x60\x60\x60"
	text = strings.TrimPrefix(text, fence+"json")
	text = strings.TrimPrefix(text, fence)
	text = strings.TrimSuffix(text, fence)
	if start := strings.Index(text, "{"); start >= 0 {
		if end := strings.LastIndex(text, "}"); end > start {
			text = text[start : end+1]
		}
	}
	if err := json.Unmarshal([]byte(text), &payload); err != nil {
		return linkAdjudicationPayload{}, err
	}
	payload.Type = strings.TrimSpace(payload.Type)
	if payload.Type == "" {
		return linkAdjudicationPayload{}, fmt.Errorf("missing link type")
	}
	return payload, nil
}

func (b *Builder) documentForPaper(ctx context.Context, ownerID string, page PaperPage) *documents.Document {
	if b.Documents == nil || len(page.DocumentIDs) == 0 {
		return nil
	}
	document, err := b.Documents.GetDocument(ctx, ownerID, page.DocumentIDs[0])
	if err != nil {
		return nil
	}
	return document
}

func documentIDOf(page PaperPage) string {
	if len(page.DocumentIDs) == 0 {
		return ""
	}
	return page.DocumentIDs[0]
}
