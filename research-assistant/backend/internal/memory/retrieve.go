package memory

import (
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const rrfK = 60.0

// BuildQuery trims and combines a user query with a few recent turns. It keeps
// the newest signal first and caps the result so task descriptions cannot
// dilute BM25 the way the audit found.
func BuildQuery(text string, recent []string, maxRunes int) string {
	parts := make([]string, 0, len(recent)+1)
	parts = append(parts, strings.TrimSpace(text))
	for i := len(recent) - 1; i >= 0; i-- {
		if strings.TrimSpace(recent[i]) != "" {
			parts = append(parts, strings.TrimSpace(recent[i]))
		}
	}
	joined := strings.Join(parts, " ")
	if maxRunes > 0 {
		runes := []rune(joined)
		if len(runes) > maxRunes {
			joined = string(runes[:maxRunes])
		}
	}
	return strings.TrimSpace(joined)
}

// matchQuery applies owner/scope/status/kind/hidden filters. Owner is a hard
// boundary: records from another owner are never returned.
func matchQuery(rec Record, q Query) bool {
	if strings.TrimSpace(q.OwnerID) == "" || rec.Scope.OwnerID != q.OwnerID {
		return false
	}
	if rec.Status == StatusDeleted {
		return false
	}
	if rec.Evidence.Hidden {
		return false
	}
	if rec.Kind == KindProcedure && rec.Attributes != nil && !q.AllScopes {
		for key, wanted := range map[string]string{"environment_hash": q.EnvironmentHash, "protocol_hash": q.ProtocolHash} {
			if required, ok := rec.Attributes[key].(string); ok && required != "" && (wanted == "" || wanted != required) {
				return false
			}
		}
	}
	if len(q.Kinds) > 0 {
		found := false
		for _, kind := range q.Kinds {
			if rec.Kind == kind {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(q.Statuses) > 0 {
		found := false
		for _, status := range q.Statuses {
			if rec.Status == status {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	} else if q.IncludeSuperseded {
		if rec.Status != StatusActive && rec.Status != StatusSuperseded && rec.Status != StatusArchived {
			return false
		}
	} else if q.IncludePending {
		if rec.Status != StatusActive && rec.Status != StatusPending {
			return false
		}
	} else if rec.Status != StatusActive {
		return false
	}
	if !q.AllScopes {
		if !scopeMatches(rec.Scope.ProjectID, q.ProjectID) {
			return false
		}
		if !scopeMatches(rec.Scope.SessionID, q.SessionID) {
			return false
		}
		if !scopeMatches(rec.Scope.PlanID, q.PlanID) {
			return false
		}
		if !scopeMatches(rec.Scope.AgentID, q.AgentID) {
			return false
		}
	}
	if len(q.Tags) > 0 {
		for _, wanted := range q.Tags {
			found := false
			for _, tag := range rec.Tags {
				if strings.EqualFold(strings.TrimSpace(tag), strings.TrimSpace(wanted)) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	if q.AsOf != nil || (!q.AllScopes && !q.IncludeSuperseded) {
		asOf := time.Now().UTC()
		if q.AsOf != nil {
			asOf = *q.AsOf
		}
		if rec.Validity.ValidFrom != nil && rec.Validity.ValidFrom.After(asOf) {
			return false
		}
		if rec.Validity.ValidTo != nil && !asOf.Before(*rec.Validity.ValidTo) {
			return false
		}
	}
	return true
}

// scopeMatches implements strict scope semantics: a record that carries a
// scope is visible only to a query that carries exactly the same value. A
// record without that scope is visible to every query in the parent scope.
func scopeMatches(recordScope, queryScope string) bool {
	return ScopeMatches(recordScope, queryScope)
}

// ScopeMatches is shared by memory and paper/document retrieval. An omitted
// query scope never grants access to records in a child scope.
func ScopeMatches(recordScope, queryScope string) bool {
	if recordScope == "" {
		return true
	}
	return queryScope != "" && recordScope == queryScope
}

func trustWeight(trust Trust) float64 {
	switch trust {
	case TrustUserExplicit:
		return 1.0
	case TrustVerified:
		return 0.9
	case TrustInferred:
		return 0.6
	case TrustUntrusted:
		return 0.3
	}
	return 0.5
}

func scopeWeight(rec Record, q Query) float64 {
	switch {
	case rec.Scope.PlanID != "" && rec.Scope.PlanID == q.PlanID:
		return 1.0
	case rec.Scope.ProjectID != "" && rec.Scope.ProjectID == q.ProjectID:
		return 0.9
	case rec.Scope.SessionID != "" && rec.Scope.SessionID == q.SessionID:
		return 0.8
	default:
		return 0.7
	}
}

func halfLifeDays(kind Kind) float64 {
	switch kind {
	case KindFact:
		return 180
	case KindEpisode:
		return 30
	case KindProcedure, KindReflection:
		return 365
	case KindProfile:
		return 3650
	case KindConstraint:
		return 90
	case KindNegative:
		return 180
	default:
		return 90
	}
}

func recencyWeight(rec Record, now time.Time) float64 {
	base := rec.Validity.ValidFrom
	if base == nil {
		base = &rec.UpdatedAt
	}
	if base == nil || base.IsZero() {
		return 1
	}
	ageDays := now.Sub(*base).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	return math.Exp(-ageDays / halfLifeDays(rec.Kind))
}

func hasTimeWords(query string) bool {
	q := strings.ToLower(query)
	for _, word := range []string{"上次", "之前", "最近", "昨天", "上周", "刚才", "last time", "recent", "yesterday", "last week"} {
		if strings.Contains(q, word) {
			return true
		}
	}
	return false
}

// searchLocked is the core candidate-generation + fusion step. Callers hold at
// least a read lock on the store.
func (s *FileStore) searchLocked(q Query) []ScoredRecord {
	return mmrSelect(s.rankCandidatesLocked(q), queryLimit(q), 0.7, perKindMaxForQuery())
}

func queryLimit(q Query) int {
	limit := q.Limit
	if limit <= 0 {
		if q.TopK > 0 {
			limit = q.TopK
		} else {
			limit = 8
		}
	}
	return limit
}

func (s *FileStore) rankCandidatesLocked(q Query) []ScoredRecord {
	now := time.Now().UTC()
	rrf := map[string]float64{}
	meta := map[string]*ScoredRecord{}

	addRanked := func(ids []string, weight float64, kind string) {
		for rank, id := range ids {
			rrf[id] += weight / (rrfK + float64(rank+1))
			entry := meta[id]
			if entry == nil {
				rec, ok := s.records[id]
				if !ok {
					continue
				}
				entry = &ScoredRecord{Record: cloneRecord(rec)}
				meta[id] = entry
			}
			switch kind {
			case "bm25":
				entry.BM25 = 1
			case "entity":
				entry.EntityHit = true
			case "recency":
				entry.RecencyHit = true
			}
		}
	}

	visible := make(map[string]struct{}, len(s.byOwner[q.OwnerID]))
	for id := range s.byOwner[q.OwnerID] {
		if rec, ok := s.records[id]; ok && matchQuery(rec, q) {
			visible[id] = struct{}{}
		}
	}
	bm25Hits := s.idx.SearchBM25In(ExpandRecallQuery(q.Text), 50, visible)
	bm25IDs := make([]string, 0, len(bm25Hits))
	for _, hit := range bm25Hits {
		bm25IDs = append(bm25IDs, hit.ID)
	}
	addRanked(bm25IDs, 1.0, "bm25")
	for _, hit := range bm25Hits {
		meta[hit.ID].BM25 = hit.Score
	}

	if len(q.Entities) > 0 {
		entityIDs := make([]string, 0)
		for _, id := range s.idx.EntityIDs(q.Entities) {
			if _, ok := visible[id]; ok {
				entityIDs = append(entityIDs, id)
			}
		}
		if len(entityIDs) > 20 {
			entityIDs = entityIDs[:20]
		}
		addRanked(entityIDs, 0.8, "entity")
	}

	if hasTimeWords(q.Text) {
		recent := make([]string, 0)
		for id := range s.byOwner[q.OwnerID] {
			rec, ok := s.records[id]
			if !ok || !matchQuery(rec, q) {
				continue
			}
			recent = append(recent, id)
		}
		sort.Slice(recent, func(i, j int) bool {
			ri := s.records[recent[i]]
			rj := s.records[recent[j]]
			return ri.UpdatedAt.After(rj.UpdatedAt)
		})
		if len(recent) > 10 {
			recent = recent[:10]
		}
		addRanked(recent, 0.5, "recency")
	}

	candidates := make([]ScoredRecord, 0, len(meta))
	for id, entry := range meta {
		rec := entry.Record
		if !matchQuery(rec, q) {
			continue
		}
		score := rrf[id]
		score *= trustWeight(rec.Trust)
		score *= 0.5 + float64(rec.Importance)/10.0
		score *= recencyWeight(rec, now)
		score *= rec.Confidence
		score *= scopeWeight(rec, q)
		if rec.Pinned {
			score += 0.15
			entry.PinnedBonus = true
		}
		entry.Score = score
		candidates = append(candidates, *entry)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].Record.ID < candidates[j].Record.ID
		}
		return candidates[i].Score > candidates[j].Score
	})

	return candidates
}

// ExpandRecallQuery is a small, inspectable bilingual alias vocabulary, not a
// semantic model. All candidates still pass the original visibility filters.
func ExpandRecallQuery(text string) string {
	groups := [][]string{
		{"out of memory", "oom", "显存不足", "内存不足"},
		{"learning rate", "学习率"},
		{"batch size", "批大小", "批量大小"},
		{"negative result", "负结果"},
		{"macro-f1", "宏平均f1", "宏平均 f1"},
	}
	lower := strings.ToLower(text)
	tokens := map[string]bool{}
	for _, token := range Tokenize(lower) {
		tokens[token] = true
	}
	var expanded strings.Builder
	expanded.WriteString(text)
	for _, group := range groups {
		matched := false
		for _, alias := range group {
			if (alias == "oom" && tokens[alias]) || (alias != "oom" && strings.Contains(lower, alias)) {
				matched = true
			}
		}
		if matched {
			expanded.WriteString(" " + strings.Join(group, " "))
		}
	}
	return expanded.String()
}

// perKindMaxForQuery returns the MMR per-kind cap. Profiles are deliberately
// generous because pinned user facts live there; episodic records stay capped
// so near-duplicate episodes cannot crowd out facts. MEMORY_MMR_PER_KIND_MAX
// accepts "profile=10,episode=3" overrides.
func perKindMaxForQuery() map[Kind]int {
	caps := map[Kind]int{
		KindProfile:    10,
		KindFact:       6,
		KindEpisode:    3,
		KindProcedure:  4,
		KindConstraint: 3,
		KindNegative:   3,
		KindDocument:   3,
		KindWorking:    3,
		KindReflection: 3,
	}
	raw := strings.TrimSpace(os.Getenv("MEMORY_MMR_PER_KIND_MAX"))
	for _, pair := range strings.Split(raw, ",") {
		key, value, ok := strings.Cut(pair, "=")
		if !ok {
			continue
		}
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || parsed <= 0 {
			continue
		}
		caps[Kind(strings.TrimSpace(key))] = parsed
	}
	return caps
}

// mmrSelect applies maximal marginal relevance so near-duplicate episodes do
// not occupy the whole budget.
func mmrSelect(candidates []ScoredRecord, limit int, lambda float64, perKindMax map[Kind]int) []ScoredRecord {
	if limit <= 0 || len(candidates) == 0 {
		return nil
	}
	selected := make([]ScoredRecord, 0, limit)
	kindCount := map[Kind]int{}
	used := make([]bool, len(candidates))
	maxRelevance := 0.0
	for _, candidate := range candidates {
		maxRelevance = max(maxRelevance, candidate.Score)
	}
	for i := range candidates {
		if maxRelevance > 0 {
			candidates[i].Relevance = candidates[i].Score / maxRelevance
		}
	}
	for len(selected) < limit {
		best := -1
		bestScore := math.Inf(-1)
		for i, candidate := range candidates {
			cap := perKindMax[candidate.Record.Kind]
			if cap <= 0 {
				cap = 3
			}
			if used[i] || kindCount[candidate.Record.Kind] >= cap {
				continue
			}
			maxSim := 0.0
			for _, chosen := range selected {
				if sim := redundancy(candidate.Record, chosen.Record); sim > maxSim {
					maxSim = sim
				}
			}
			score := lambda*candidate.Relevance - (1-lambda)*maxSim
			if score > bestScore {
				bestScore = score
				best = i
			}
		}
		if best < 0 {
			break
		}
		used[best] = true
		candidates[best].MMRScore = bestScore
		selected = append(selected, candidates[best])
		kindCount[candidates[best].Record.Kind]++
	}
	sort.SliceStable(selected, func(i, j int) bool {
		if selected[i].Score == selected[j].Score {
			return selected[i].Record.ID < selected[j].Record.ID
		}
		return selected[i].Score > selected[j].Score
	})
	return selected
}

var evidenceNumber = regexp.MustCompile(`[-+]?[0-9]+(?:\.[0-9]+)?(?:%|[a-zA-Z]+)?`)

func redundancy(a, b Record) float64 {
	// Similar wording can carry different experimental values or a correction.
	// Only exact duplicates may suppress those necessary differences.
	if a.Content != b.Content {
		for _, key := range []string{"experiment_id", "dataset_version", "split", "method", "configuration_hash", "metric_name", "aggregation", "seed", "environment_hash", "protocol_hash"} {
			if a.Attributes[key] != nil && b.Attributes[key] != nil && fmt.Sprint(a.Attributes[key]) != fmt.Sprint(b.Attributes[key]) {
				return 0
			}
		}
		if strings.Join(evidenceNumber.FindAllString(a.Content, -1), "|") != strings.Join(evidenceNumber.FindAllString(b.Content, -1), "|") ||
			(a.FactID != "" && a.FactID == b.FactID && a.RevisionID != b.RevisionID) ||
			(a.Kind == KindNegative) != (b.Kind == KindNegative) ||
			containsCorrection(a.Content) != containsCorrection(b.Content) {
			return 0
		}
	}
	return tokenJaccard(a.Content, b.Content)
}

func containsCorrection(text string) bool {
	lower := strings.ToLower(text)
	for _, marker := range []string{"纠正", "更正", "修正", "未通过", "失败", "不适用", "没有", "不能", "not ", "failed", "correction", "corrected", "supersed"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func tokenJaccard(a, b string) float64 {
	setA := map[string]struct{}{}
	for _, token := range Tokenize(a) {
		setA[token] = struct{}{}
	}
	if len(setA) == 0 {
		return 0
	}
	inter := 0
	setB := map[string]struct{}{}
	for _, token := range Tokenize(b) {
		if _, exists := setB[token]; exists {
			continue
		}
		setB[token] = struct{}{}
		if _, exists := setA[token]; exists {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// SearchResult is the single-pass response used by /api/memories/search:
// ranked results plus the rendered injection context produced from the same
// candidate scan.
type SearchResult struct {
	Results []ScoredRecord `json:"results"`
	Context Context        `json:"context"`
}

// SearchWithContext runs searchLocked once and builds the context from those
// candidates. It is the replacement for calling Search and Retrieve separately.
func (s *FileStore) SearchWithContext(ctx context.Context, q Query, coreBudget, retrievedBudget int) (SearchResult, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	candidates := s.rankCandidatesLocked(q)
	results := mmrSelect(candidates, queryLimit(q), 0.7, perKindMaxForQuery())
	result := s.buildContextLocked(q, coreBudget, retrievedBudget, results)
	result.Report.MatchedCandidates = len(candidates)
	result.Report.RankExcluded = len(candidates) - len(results)
	return SearchResult{Results: results, Context: result}, nil
}

// Retrieve builds the injected memory context: a stable core block plus a
// query-relevant retrieved block, each capped by its token budget.
func (s *FileStore) Retrieve(ctx context.Context, q Query, coreBudget, retrievedBudget int) (Context, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.buildContextLocked(q, coreBudget, retrievedBudget, nil), nil
}

func (s *FileStore) buildContextLocked(q Query, coreBudget, retrievedBudget int, precomputed []ScoredRecord) Context {
	if coreBudget < 0 {
		coreBudget = 512
	}
	if retrievedBudget < 0 {
		retrievedBudget = 2000
	}

	var result Context
	result.Report.OwnerRecords = len(s.byOwner[q.OwnerID])
	coreCandidates := make([]Record, 0)
	for id := range s.byOwner[q.OwnerID] {
		rec, ok := s.records[id]
		if !ok || !matchQuery(rec, q) {
			continue
		}
		result.Report.VisibleRecords++
		if !rec.Pinned {
			continue
		}
		coreCandidates = append(coreCandidates, cloneRecord(rec))
	}
	sort.Slice(coreCandidates, func(i, j int) bool {
		if coreCandidates[i].Importance == coreCandidates[j].Importance {
			return coreCandidates[i].UpdatedAt.After(coreCandidates[j].UpdatedAt)
		}
		return coreCandidates[i].Importance > coreCandidates[j].Importance
	})
	core, coreText, coreTokens, coreTruncated := renderWithBudget(coreCandidates, coreBudget)
	result.Core = core
	result.CoreText = coreText
	result.Report.CoreTokens = coreTokens
	result.Report.Truncated += coreTruncated

	retrievedCandidates := precomputed
	if retrievedCandidates == nil {
		candidates := s.rankCandidatesLocked(q)
		retrievedCandidates = mmrSelect(candidates, queryLimit(q), 0.7, perKindMaxForQuery())
		result.Report.MatchedCandidates = len(candidates)
		result.Report.RankExcluded = len(candidates) - len(retrievedCandidates)
	}
	filtered := make([]Record, 0, len(retrievedCandidates))
	coreIDs := map[string]struct{}{}
	for _, rec := range core {
		coreIDs[rec.ID] = struct{}{}
	}
	for _, scored := range retrievedCandidates {
		if _, exists := coreIDs[scored.Record.ID]; exists {
			continue
		}
		filtered = append(filtered, scored.Record)
	}
	retrieved, retrievedText, retrievedTokens, retrievedTruncated := renderWithBudget(filtered, retrievedBudget)
	result.Retrieved = retrieved
	result.RetrievedText = retrievedText
	result.Report.RetrievedTokens = retrievedTokens
	result.Report.Candidates = len(retrievedCandidates)
	result.Report.Selected = len(retrieved)
	result.Report.Truncated += retrievedTruncated
	result.Report.BudgetExcluded = len(filtered) - len(retrieved)
	result.Report.Ranking = "rrf_max_normalized_mmr_0.7"
	switch {
	case result.Report.OwnerRecords == 0:
		result.Report.FailureStage = "no_records"
	case result.Report.VisibleRecords == 0:
		result.Report.FailureStage = "not_visible"
	case len(retrievedCandidates) == 0 && len(core) == 0:
		result.Report.FailureStage = "not_recalled"
	case len(retrieved) == 0 && len(core) == 0:
		result.Report.FailureStage = "budget_excluded"
	default:
		result.Report.FailureStage = "selected"
	}
	result.Tokens = coreTokens + retrievedTokens
	for _, rec := range append(append([]Record{}, core...), retrieved...) {
		result.Citations = append(result.Citations, Citation{
			MemoryID: rec.ID,
			Title:    rec.Title,
			Kind:     rec.Kind,
			Date:     rec.UpdatedAt.Format("2006-01-02"),
			Source:   rec.Source.Type + ":" + rec.Source.Ref,
			Trust:    rec.Trust,
			Quote:    rec.Content,
			FactID:   rec.FactID, RevisionID: rec.RevisionID,
		})
	}
	return result
}

func renderWithBudget(records []Record, budget int) ([]Record, string, int, int) {
	selected := make([]Record, 0, len(records))
	builder := strings.Builder{}
	truncated := 0
	for _, rec := range records {
		block := RenderRecord(rec)
		if builder.Len() > 0 {
			block = "\n" + block
		}
		tokens := EstimateTokens(block)
		if EstimateTokens(builder.String())+tokens > budget {
			remaining := budget - EstimateTokens(builder.String())
			if remaining < 20 {
				truncated++
				continue
			}
			content, didTruncate := truncateToTokens(RenderPlain(rec), remaining-10)
			if !didTruncate {
				truncated++
				continue
			}
			rec.Content = content
			block = "\n" + RenderRecord(rec)
			truncated++
			tokens = EstimateTokens(block)
			if EstimateTokens(builder.String())+tokens > budget {
				continue
			}
		}
		builder.WriteString(block)
		selected = append(selected, rec)
	}
	return selected, builder.String(), EstimateTokens(builder.String()), truncated
}
