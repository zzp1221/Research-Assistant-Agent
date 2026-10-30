// Package literature implements literature search and PDF import for the
// research assistant.
//
// It speaks directly to the public arXiv, OpenAlex, Crossref and GitHub APIs
// using only the Go standard library. Requests are cached per URL, rate
// limited per host, and provider failures are reported as structured *Error
// values so that one unavailable provider never makes an entire search
// unusable.
package literature

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Supported result sources.
const (
	SourceArxiv           = "arxiv"
	SourceOpenAlex        = "openalex"
	SourceCrossref        = "crossref"
	SourceGitHub          = "github"
	SourceSemanticScholar = "semanticscholar"
	SourcePubMed          = "pubmed"
	SourceDBLP            = "dblp"
	SourceUnpaywall       = "unpaywall"
	SourceURL             = "url"
)

// Error codes carried by Error.Code.
const (
	CodeInvalidRef       = "invalid_ref"
	CodeInvalidQuery     = "invalid_query"
	CodeNotFound         = "not_found"
	CodeUpstream         = "upstream_error"
	CodeRateLimited      = "rate_limited"
	CodeMalformed        = "malformed_payload"
	CodePDFUnavailable   = "pdf_unavailable"
	CodePDFTooLarge      = "pdf_too_large"
	CodeInvalidPDF       = "invalid_pdf"
	CodeAllSourcesFailed = "all_sources_failed"
	CodeCanceled         = "canceled"
	CodeInternal         = "internal_error"
)

// Sentinel errors. Every *Error returned by this package wraps exactly one of
// these, so callers can use errors.Is without matching on message strings.
var (
	ErrInvalidRef       = errors.New("invalid literature reference")
	ErrInvalidQuery     = errors.New("invalid literature search query")
	ErrNotFound         = errors.New("literature record not found")
	ErrUpstream         = errors.New("literature provider request failed")
	ErrMalformed        = errors.New("malformed literature provider payload")
	ErrPDFUnavailable   = errors.New("open-access pdf unavailable")
	ErrPDFTooLarge      = errors.New("pdf exceeds the configured size limit")
	ErrInvalidPDF       = errors.New("invalid pdf payload")
	ErrAllSourcesFailed = errors.New("all literature sources failed")
)

// defaultSources is both the default search set and its relevance order.
var defaultSources = []string{SourceArxiv, SourceOpenAlex, SourceCrossref, SourceGitHub}

// Result is one normalized literature or repository record.
type Result struct {
	Source        string   `json:"source"`
	ID            string   `json:"id"`
	Title         string   `json:"title"`
	Authors       []string `json:"authors,omitempty"`
	Year          int      `json:"year,omitempty"`
	Venue         string   `json:"venue,omitempty"`
	DOI           string   `json:"doi,omitempty"`
	ArxivID       string   `json:"arxiv_id,omitempty"`
	URL           string   `json:"url,omitempty"`
	PDFURL        string   `json:"pdf_url,omitempty"`
	Abstract      string   `json:"abstract,omitempty"`
	Citations     int      `json:"citations,omitempty"`
	RepositoryURL string   `json:"repository_url,omitempty"`
	// OpenAccess and License describe the best available copy when the
	// provider exposes it (Unpaywall/OpenAlex/Semantic Scholar).
	OpenAccess bool   `json:"open_access,omitempty"`
	License    string `json:"license,omitempty"`
	// Rank is the 1-based position in the final result list; Score is a
	// normalized 0..1 relevance signal; RankReason explains the ordering.
	Rank       int     `json:"rank,omitempty"`
	Score      float64 `json:"score,omitempty"`
	RankReason string  `json:"rank_reason,omitempty"`
	// ProviderRank is the provider-native ordering (1-based) and
	// ProviderScore is an optional provider relevance score (for example
	// OpenAlex relevance_score). Both feed the cross-provider ranker.
	ProviderRank  int     `json:"provider_rank,omitempty"`
	ProviderScore float64 `json:"provider_score,omitempty"`
}

// SearchOptions controls one literature search. Sources is a subset of
// "arxiv", "openalex", "crossref", "github" or "all"; an empty Sources means
// all of them. Limit defaults to 10. YearFrom, when positive, drops records
// with an unknown or older publication year.
type SearchOptions struct {
	Sources  []string `json:"sources,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	YearFrom int      `json:"year_from,omitempty"`
	// Sort is "relevance" (default), "citations" or "year".
	Sort string `json:"sort,omitempty"`
}

// ImportResult is the metadata and raw PDF bytes for one imported reference.
// The embedded Result.Source and the outer Source are kept in sync; the outer
// field exists so the import origin is explicit even when Result is empty.
type ImportResult struct {
	Result
	PDF    []byte `json:"-"`
	Source string `json:"source"`
}

// Warning describes one provider that failed during an otherwise successful
// search. Warnings are also returned by SearchWithWarnings.
type Warning struct {
	Source  string `json:"source"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

// Error is the structured error returned by every public operation. Code is
// one of the Code* constants and Err unwraps to one of the Err* sentinels.
type Error struct {
	Code    string `json:"code"`
	Source  string `json:"source,omitempty"`
	Ref     string `json:"ref,omitempty"`
	Message string `json:"message"`
	Err     error  `json:"-"`
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e == nil {
		return "literature: <nil>"
	}
	message := strings.TrimSpace(e.Message)
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if message == "" {
		message = "unknown error"
	}
	prefix := "literature"
	if e.Code != "" {
		prefix += " " + e.Code
	}
	details := make([]string, 0, 2)
	if e.Source != "" {
		details = append(details, "source="+e.Source)
	}
	if e.Ref != "" {
		details = append(details, "ref="+e.Ref)
	}
	if len(details) > 0 {
		return fmt.Sprintf("%s (%s): %s", prefix, strings.Join(details, ", "), message)
	}
	return prefix + ": " + message
}

// Unwrap exposes the underlying sentinel or provider error.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// IsCode reports whether err is an *Error with the given code.
func IsCode(err error, code string) bool {
	var target *Error
	if errors.As(err, &target) {
		return target.Code == code
	}
	return false
}

func newError(code, source, ref, message string) *Error {
	return &Error{Code: code, Source: source, Ref: ref, Message: message}
}

func wrapError(code, source, ref string, cause error, message string) *Error {
	return &Error{Code: code, Source: source, Ref: ref, Message: message, Err: cause}
}

func notFoundError(source, ref, message string) *Error {
	return &Error{Code: CodeNotFound, Source: source, Ref: ref, Message: message, Err: ErrNotFound}
}

func malformedPayloadError(source string, cause error) *Error {
	wrapped := ErrMalformed
	if cause != nil {
		wrapped = fmt.Errorf("%w: %v", ErrMalformed, cause)
	}
	return &Error{Code: CodeMalformed, Source: source, Message: "decode provider payload", Err: wrapped}
}

func pdfUnavailableError(source, ref, message string) *Error {
	return &Error{Code: CodePDFUnavailable, Source: source, Ref: ref, Message: message, Err: ErrPDFUnavailable}
}

func resolveSources(raw []string) ([]string, []Warning) {
	if len(raw) == 0 {
		return append([]string(nil), defaultSources...), nil
	}
	var sources []string
	var warnings []Warning
	seen := make(map[string]bool, len(raw))
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		sources = append(sources, name)
	}
	for _, item := range raw {
		name := strings.ToLower(strings.TrimSpace(item))
		switch name {
		case "":
			continue
		case "all", "*":
			for _, source := range defaultSources {
				add(source)
			}
		case SourceArxiv, SourceOpenAlex, SourceCrossref, SourceGitHub,
			SourceSemanticScholar, SourcePubMed, SourceDBLP:
			add(name)
		default:
			warnings = append(warnings, Warning{
				Source:  name,
				Message: "unsupported literature source ignored",
			})
		}
	}
	return sources, warnings
}

func allSourcesError(failed []Warning) *Error {
	parts := make([]string, 0, len(failed))
	causes := []error{ErrAllSourcesFailed}
	for _, warning := range failed {
		parts = append(parts, warning.Source+": "+warning.Message)
		if warning.Err != nil {
			causes = append(causes, warning.Err)
		}
	}
	return &Error{
		Code:    CodeAllSourcesFailed,
		Message: "all literature sources failed: " + strings.Join(parts, "; "),
		Err:     errors.Join(causes...),
	}
}

func dedupeResults(results []Result) []Result {
	if len(results) == 0 {
		return results
	}
	index := make(map[string]int, len(results)*2)
	deduped := make([]Result, 0, len(results))
	for _, result := range results {
		keys := resultKeys(result)
		existing := -1
		for _, key := range keys {
			if position, ok := index[key]; ok {
				existing = position
				break
			}
		}
		if existing >= 0 {
			mergeResult(&deduped[existing], result)
			for _, key := range resultKeys(deduped[existing]) {
				index[key] = existing
			}
			continue
		}
		deduped = append(deduped, result)
		position := len(deduped) - 1
		for _, key := range keys {
			index[key] = position
		}
	}
	return deduped
}

func resultKeys(result Result) []string {
	keys := make([]string, 0, 3)
	if doi := strings.ToLower(normalizeDOI(result.DOI)); doi != "" {
		keys = append(keys, "doi:"+doi)
	}
	if arxivID := strings.ToLower(normalizedArxivID(result.ArxivID)); arxivID != "" {
		keys = append(keys, "arxiv:"+arxivID)
	}
	title := normalizeTitle(result.Title)
	if len([]rune(title)) >= 4 {
		keys = append(keys, "title:"+title)
	}
	return keys
}

func mergeResult(destination *Result, source Result) {
	if destination.Title == "" {
		destination.Title = source.Title
	}
	if destination.ID == "" {
		destination.ID = source.ID
	}
	if destination.Year == 0 {
		destination.Year = source.Year
	}
	if destination.Venue == "" {
		destination.Venue = source.Venue
	}
	if destination.DOI == "" {
		destination.DOI = source.DOI
	}
	if destination.ArxivID == "" {
		destination.ArxivID = source.ArxivID
	}
	if destination.URL == "" {
		destination.URL = source.URL
	}
	if destination.PDFURL == "" {
		destination.PDFURL = source.PDFURL
	}
	if destination.RepositoryURL == "" {
		destination.RepositoryURL = source.RepositoryURL
	}
	if len(destination.Authors) == 0 && len(source.Authors) > 0 {
		destination.Authors = append([]string(nil), source.Authors...)
	}
	if destination.Citations < source.Citations {
		destination.Citations = source.Citations
	}
	if len(source.Abstract) > len(destination.Abstract) {
		destination.Abstract = source.Abstract
	}
	if !destination.OpenAccess && source.OpenAccess {
		destination.OpenAccess = true
	}
	if destination.License == "" {
		destination.License = source.License
	}
	if destination.ProviderRank == 0 || (source.ProviderRank > 0 && source.ProviderRank < destination.ProviderRank) {
		destination.ProviderRank = source.ProviderRank
	}
	if source.ProviderScore > destination.ProviderScore {
		destination.ProviderScore = source.ProviderScore
	}
}

// sourcePrior is a small cross-provider prior. It keeps broad aggregators
// ahead of metadata-only or repository sources when lexical evidence ties.
func sourcePrior(source string) float64 {
	switch source {
	case SourceArxiv, SourceOpenAlex:
		return 1.0
	case SourceSemanticScholar:
		return 0.95
	case SourcePubMed, SourceDBLP:
		return 0.75
	case SourceCrossref:
		return 0.7
	case SourceGitHub:
		return 0.3
	default:
		return 0.5
	}
}

var relevanceStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
	"be": true, "by": true, "for": true, "from": true, "in": true, "into": true,
	"is": true, "of": true, "on": true, "or": true, "the": true, "to": true,
	"with": true, "using": true, "based": true, "via": true,
}

// relevanceTerms tokenizes a query for lexical relevance scoring. It keeps
// English words of two or more characters and CJK bigrams.
func relevanceTerms(query string) []string {
	lower := strings.ToLower(query)
	fields := strings.FieldsFunc(lower, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := make([]string, 0, len(fields)+8)
	seen := make(map[string]bool, len(fields)+8)
	add := func(term string) {
		term = strings.TrimSpace(term)
		if len([]rune(term)) < 2 || relevanceStopwords[term] || seen[term] {
			return
		}
		seen[term] = true
		terms = append(terms, term)
	}
	for _, field := range fields {
		add(field)
	}
	runes := []rune(lower)
	for index := 0; index+1 < len(runes); index++ {
		if unicode.Is(unicode.Han, runes[index]) && unicode.Is(unicode.Han, runes[index+1]) {
			add(string(runes[index : index+2]))
		}
	}
	return terms
}

func containsAnyWord(text string, words ...string) bool {
	for _, word := range words {
		if strings.Contains(text, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

// rankByRelevance scores each result against the query with a small lexical
// model (coverage of query terms in title/abstract, phrase match, survey
// intent and citations) instead of grouping by provider. Provider-native
// order is only a tie-breaking prior, so one strong provider result cannot be
// buried behind every result from an earlier provider.
func rankByRelevance(results []Result, query string, order []string) {
	terms := relevanceTerms(query)
	if len(terms) == 0 {
		sortResults(results, order)
		return
	}
	documents := make([]string, len(results))
	titles := make([]string, len(results))
	documentFrequency := make(map[string]int, len(terms))
	for index := range results {
		titles[index] = strings.ToLower(results[index].Title + " " + results[index].Venue)
		documents[index] = titles[index] + " " + strings.ToLower(results[index].Abstract)
		for _, term := range terms {
			if strings.Contains(documents[index], term) {
				documentFrequency[term]++
			}
		}
	}
	surveyQuery := containsAnyWord(strings.ToLower(query), "survey", "review", "overview", "tutorial", "综述", "文献综述")
	normalizedQuery := normalizeTitle(query)
	corpusSize := float64(len(results))
	type scored struct {
		index int
		score float64
	}
	candidates := make([]scored, 0, len(results))
	for index := range results {
		titleMatches, allMatches := 0, 0
		titleLower := titles[index]
		documentLower := documents[index]
		for _, term := range terms {
			if strings.Contains(titleLower, term) {
				titleMatches++
			}
			if strings.Contains(documentLower, term) {
				allMatches++
			}
		}
		coverage := float64(allMatches) / float64(len(terms))
		titleCoverage := float64(titleMatches) / float64(len(terms))
		phrase := 0.0
		if normalizedQuery != "" && strings.Contains(normalizeTitle(results[index].Title), normalizedQuery) {
			phrase = 1
		}
		// Down-weight terms that occur in almost every candidate (for example
		// the generic words in a broad query) while keeping coverage primary.
		idfBonus := 0.0
		for _, term := range terms {
			if !strings.Contains(documentLower, term) {
				continue
			}
			df := float64(documentFrequency[term])
			idfBonus += math.Log(1 + (corpusSize-df+0.5)/(df+0.5))
		}
		if len(terms) > 0 {
			idfBonus = idfBonus / float64(len(terms)) / 3
		}
		citation := 0.0
		if results[index].Citations > 0 {
			citation = math.Log1p(float64(results[index].Citations)) / math.Log1p(20000)
			if citation > 1 {
				citation = 1
			}
		}
		survey := 0.0
		if surveyQuery && containsAnyWord(titleLower, "survey", "review", "overview", "tutorial", "综述") {
			survey = 1
		}
		providerPriorValue := 0.0
		if results[index].ProviderRank > 0 {
			providerPriorValue = 1 / float64(results[index].ProviderRank+1)
		}
		// Coverage stays primary, but exact phrase order is a weak signal
		// (survey titles often reorder "Survey on Graph Neural Networks").
		// Citations carry real weight because they are the strongest
		// available proxy for a canonical survey when lexical coverage ties.
		score := 0.40*coverage + 0.18*titleCoverage + 0.05*phrase + 0.12*survey + 0.18*citation + 0.05*idfBonus +
			0.05*sourcePrior(results[index].Source) + 0.02*providerPriorValue
		candidates = append(candidates, scored{index: index, score: score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		left := results[candidates[i].index]
		right := results[candidates[j].index]
		if left.Citations != right.Citations {
			return left.Citations > right.Citations
		}
		if left.Year != right.Year {
			return left.Year > right.Year
		}
		return strings.ToLower(left.Title) < strings.ToLower(right.Title)
	})
	reranked := make([]Result, len(candidates))
	for position, candidate := range candidates {
		reranked[position] = results[candidate.index]
		reranked[position].Score = math.Round(candidate.score*100) / 100
		if reranked[position].Score > 1 {
			reranked[position].Score = 1
		}
	}
	copy(results, reranked)
}
func sortResults(results []Result, order []string) {
	rank := make(map[string]int, len(order))
	for position, source := range order {
		rank[source] = position
	}
	fallback := len(order)
	sort.SliceStable(results, func(i, j int) bool {
		left, ok := rank[results[i].Source]
		if !ok {
			left = fallback
		}
		right, ok := rank[results[j].Source]
		if !ok {
			right = fallback
		}
		if left != right {
			return left < right
		}
		return results[i].Year > results[j].Year
	})
}

// applyRanking orders results according to sortMode and stamps rank/score/
// rank_reason so the API can explain why a result is where it is.
func applyRanking(results []Result, query string, order []string, sortMode string) {
	switch strings.ToLower(strings.TrimSpace(sortMode)) {
	case "citations", "citation":
		sort.SliceStable(results, func(i, j int) bool {
			if results[i].Citations != results[j].Citations {
				return results[i].Citations > results[j].Citations
			}
			if results[i].Year != results[j].Year {
				return results[i].Year > results[j].Year
			}
			return strings.ToLower(results[i].Title) < strings.ToLower(results[j].Title)
		})
		for index := range results {
			results[index].Rank = index + 1
			results[index].RankReason = "citations desc"
			results[index].Score = normalizedRankScore(index, len(results))
		}
	case "year", "newest":
		sort.SliceStable(results, func(i, j int) bool {
			if results[i].Year != results[j].Year {
				return results[i].Year > results[j].Year
			}
			return results[i].Citations > results[j].Citations
		})
		for index := range results {
			results[index].Rank = index + 1
			results[index].RankReason = "year desc"
			results[index].Score = normalizedRankScore(index, len(results))
		}
	default:
		rankByRelevance(results, query, order)
		for index := range results {
			results[index].Rank = index + 1
			results[index].RankReason = "lexical relevance + citations + provider rank"
		}
	}
}

func normalizedRankScore(index, total int) float64 {
	if total <= 0 {
		return 0
	}
	return math.Round((1-float64(index)/float64(total))*100) / 100
}

func filterByYear(results []Result, yearFrom int) []Result {
	if yearFrom <= 0 || len(results) == 0 {
		return results
	}
	filtered := make([]Result, 0, len(results))
	for _, result := range results {
		if result.Year >= yearFrom {
			filtered = append(filtered, result)
		}
	}
	return filtered
}

func limitResults(results []Result, limit int) []Result {
	if limit <= 0 || len(results) <= limit {
		return results
	}
	return results[:limit]
}

func normalizeTitle(raw string) string {
	var builder strings.Builder
	space := false
	for _, char := range strings.ToLower(strings.TrimSpace(raw)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
			space = false
			continue
		}
		if !space && builder.Len() > 0 {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}

func cleanText(raw string) string {
	return strings.Join(strings.Fields(raw), " ")
}
