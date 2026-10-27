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
	"sort"
	"strings"
	"unicode"
)

// Supported result sources.
const (
	SourceArxiv    = "arxiv"
	SourceOpenAlex = "openalex"
	SourceCrossref = "crossref"
	SourceGitHub   = "github"
	SourceURL      = "url"
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
}

// SearchOptions controls one literature search. Sources is a subset of
// "arxiv", "openalex", "crossref", "github" or "all"; an empty Sources means
// all of them. Limit defaults to 10. YearFrom, when positive, drops records
// with an unknown or older publication year.
type SearchOptions struct {
	Sources  []string `json:"sources,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	YearFrom int      `json:"year_from,omitempty"`
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

func errorIsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || IsCode(err, CodeNotFound)
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
		case SourceArxiv, SourceOpenAlex, SourceCrossref, SourceGitHub:
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
