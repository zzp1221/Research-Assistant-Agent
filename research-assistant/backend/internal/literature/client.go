package literature

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultSearchLimit     = 10
	defaultTimeout         = 30 * time.Second
	defaultCacheTTL        = 15 * time.Minute
	defaultMaxPDFBytes     = int64(64 << 20)
	maxConfiguredPDFEnvMB  = int64(1 << 20)
	defaultUserAgent       = "research-assistant-literature/1.0"
	defaultProviderTimeout = 15 * time.Second
)

// Config controls the literature client. HTTPClient is optional and is also
// the seam used by tests to point requests at an httptest server together
// with WithEndpoint. CacheDir selects the on-disk cache; an empty CacheDir
// selects an in-memory cache instead.
type Config struct {
	HTTPClient     *http.Client
	CacheDir       string
	CacheTTL       time.Duration
	Timeout        time.Duration
	UserAgent      string
	GitHubToken    string
	OpenAlexMailto string
	// MaxRetries is the number of retries for 429/503 provider responses.
	// Zero uses the default (2); negative disables retries.
	MaxRetries int
	// RetryBaseDelay is the base exponential backoff. Zero uses 200ms.
	RetryBaseDelay time.Duration
	// ProviderTimeout bounds one provider call inside a multi-provider search.
	// Zero uses 8s so one throttled provider cannot consume the whole request
	// budget and discard the other providers' results.
	ProviderTimeout time.Duration
}

// Client is a concurrency-safe literature search and import client.
type Client struct {
	mu sync.RWMutex

	httpClient      *http.Client
	cache           cacheStore
	limiter         *hostLimiter
	timeout         time.Duration
	userAgent       string
	githubToken     string
	openAlexMailto  string
	maxPDFBytes     int64
	endpoints       map[string]string
	maxRetries      int
	retryBaseDelay  time.Duration
	providerTimeout time.Duration
}

// NewClient builds a client with conservative defaults. Invalid or absent
// configuration values fall back to public API defaults; the constructor
// never fails so callers can use it directly from request handlers.
func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	cacheTTL := cfg.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = defaultCacheTTL
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	userAgent := strings.TrimSpace(cfg.UserAgent)
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	maxRetries := cfg.MaxRetries
	if maxRetries == 0 {
		maxRetries = 2
	}
	if maxRetries < 0 {
		maxRetries = 0
	}
	retryBaseDelay := cfg.RetryBaseDelay
	if retryBaseDelay <= 0 {
		retryBaseDelay = 200 * time.Millisecond
	}
	providerTimeout := cfg.ProviderTimeout
	if providerTimeout <= 0 {
		providerTimeout = defaultProviderTimeout
	}
	return &Client{
		httpClient:      httpClient,
		cache:           newCache(cfg.CacheDir, cacheTTL),
		limiter:         newHostLimiter(),
		timeout:         timeout,
		userAgent:       userAgent,
		githubToken:     strings.TrimSpace(cfg.GitHubToken),
		openAlexMailto:  strings.TrimSpace(cfg.OpenAlexMailto),
		maxPDFBytes:     maxPDFBytesFromEnv(),
		endpoints:       defaultEndpoints(),
		maxRetries:      maxRetries,
		retryBaseDelay:  retryBaseDelay,
		providerTimeout: providerTimeout,
	}
}

// WithHTTPClient replaces the HTTP client and returns the receiver for
// chaining. It is the supported seam for httptest servers.
func (c *Client) WithHTTPClient(client *http.Client) *Client {
	if c == nil || client == nil {
		return c
	}
	c.mu.Lock()
	c.httpClient = client
	c.mu.Unlock()
	return c
}

// WithEndpoint overrides a provider base URL and returns the receiver for
// chaining. It is intended for tests and local mirrors. The source must be one
// of the Source* constants; unknown sources are ignored.
func (c *Client) WithEndpoint(source, baseURL string) *Client {
	if c == nil {
		return nil
	}
	name := strings.ToLower(strings.TrimSpace(source))
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return c
	}
	switch name {
	case SourceArxiv, SourceOpenAlex, SourceCrossref, SourceGitHub,
		SourceSemanticScholar, SourcePubMed, SourceDBLP, SourceUnpaywall:
	default:
		return c
	}
	c.mu.Lock()
	if c.endpoints == nil {
		c.endpoints = defaultEndpoints()
	}
	c.endpoints[name] = baseURL
	c.mu.Unlock()
	return c
}

// WithMaxPDFBytes overrides the 64 MiB PDF size cap. It is primarily useful
// in tests; non-positive limits are ignored.
func (c *Client) WithMaxPDFBytes(limit int64) *Client {
	if c == nil || limit <= 0 {
		return c
	}
	c.mu.Lock()
	c.maxPDFBytes = limit
	c.mu.Unlock()
	return c
}

// Search runs the query against the requested providers and returns the
// deduplicated, ordered results. A provider failure is reported as a warning
// when another provider succeeded; only a search in which every requested
// provider failed returns an error. Use SearchWithWarnings to inspect warnings.
func (c *Client) Search(ctx context.Context, query string, opts SearchOptions) ([]Result, error) {
	results, _, err := c.SearchWithWarnings(ctx, query, opts)
	return results, err
}

// SearchWithWarnings is Search with the per-provider warnings exposed.
func (c *Client) SearchWithWarnings(ctx context.Context, query string, opts SearchOptions) (results []Result, warnings []Warning, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			results = nil
			warnings = nil
			err = newError(CodeInternal, "", "", fmt.Sprintf("literature search failed unexpectedly: %v", recovered))
		}
	}()
	return c.search(ctx, query, opts)
}

func (c *Client) search(ctx context.Context, query string, opts SearchOptions) ([]Result, []Warning, error) {
	if c == nil {
		return nil, nil, newError(CodeInternal, "", "", "literature client is nil")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil, wrapError(CodeInvalidQuery, "", "", ErrInvalidQuery, "search query is empty")
	}
	ctx = nonNilContext(ctx)
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	sources, warnings := resolveSources(opts.Sources)
	if len(sources) == 0 {
		return nil, warnings, wrapError(CodeInvalidQuery, "", "", ErrInvalidQuery, "no supported literature sources requested")
	}
	// Candidate pooling: a provider's own top-5 can omit the strongest
	// cross-provider match. Fetch a wider window, rank everything, then trim
	// to the caller's limit. (50 is the providers' common maximum page size.)
	candidateLimit := limit * 3
	if candidateLimit < limit+10 {
		candidateLimit = limit + 10
	}
	if candidateLimit > 50 {
		candidateLimit = 50
	}

	type providerOutcome struct {
		source  string
		results []Result
		err     error
	}
	outcomes := make([]providerOutcome, len(sources))
	c.mu.RLock()
	providerTimeout := c.providerTimeout
	c.mu.RUnlock()
	var wg sync.WaitGroup
	for index, source := range sources {
		wg.Add(1)
		go func(index int, source string) {
			defer wg.Done()
			providerCtx, cancel := context.WithTimeout(ctx, providerTimeout)
			defer cancel()
			var results []Result
			var sourceErr error
			switch source {
			case SourceArxiv:
				results, sourceErr = c.searchArxiv(providerCtx, query, candidateLimit, opts.YearFrom)
			case SourceOpenAlex:
				results, sourceErr = c.searchOpenAlex(providerCtx, query, candidateLimit, opts.YearFrom)
			case SourceCrossref:
				results, sourceErr = c.searchCrossref(providerCtx, query, candidateLimit, opts.YearFrom)
			case SourceGitHub:
				results, sourceErr = c.searchGitHub(providerCtx, query, candidateLimit, opts.YearFrom)
			case SourceSemanticScholar:
				results, sourceErr = c.searchSemanticScholar(providerCtx, query, candidateLimit, opts.YearFrom)
			case SourcePubMed:
				results, sourceErr = c.searchPubMed(providerCtx, query, candidateLimit, opts.YearFrom)
			case SourceDBLP:
				results, sourceErr = c.searchDBLP(providerCtx, query, candidateLimit, opts.YearFrom)
			default:
				sourceErr = newError(CodeInvalidQuery, source, "", "unsupported literature source")
			}
			outcomes[index] = providerOutcome{source: source, results: results, err: sourceErr}
		}(index, source)
	}
	wg.Wait()

	collected := make([]Result, 0, candidateLimit*len(sources))
	failed := make([]Warning, 0, len(sources))
	for _, outcome := range outcomes {
		if outcome.err != nil {
			failed = append(failed, Warning{Source: outcome.source, Message: outcome.err.Error(), Err: outcome.err})
			continue
		}
		// Freeze the provider-native order so the cross-provider ranker can
		// use it as a small prior instead of losing it during sorting.
		for index := range outcome.results {
			outcome.results[index].ProviderRank = index + 1
			if outcome.results[index].ProviderScore <= 0 && len(outcome.results) > 1 {
				outcome.results[index].ProviderScore = 1 - float64(index)/float64(len(outcome.results))
			}
		}
		collected = append(collected, filterByYear(outcome.results, opts.YearFrom)...)
	}
	warnings = append(warnings, failed...)
	deduped := dedupeResults(collected)
	applyRanking(deduped, query, sources, opts.Sort)
	deduped = limitResults(deduped, limit)
	if ctx.Err() != nil {
		return deduped, warnings, wrapError(CodeCanceled, "", "", ctx.Err(), "search canceled")
	}
	if len(failed) > 0 && len(failed) == len(sources) {
		return nil, warnings, allSourcesError(failed)
	}
	if deduped == nil {
		deduped = []Result{}
	}
	return deduped, warnings, nil
}

func defaultEndpoints() map[string]string {
	return map[string]string{
		SourceArxiv:           "https://export.arxiv.org/api/query",
		SourceOpenAlex:        "https://api.openalex.org/works",
		SourceCrossref:        "https://api.crossref.org/works",
		SourceGitHub:          "https://api.github.com/search/repositories",
		SourceSemanticScholar: "https://api.semanticscholar.org/graph/v1/paper/search",
		SourcePubMed:          "https://eutils.ncbi.nlm.nih.gov/entrez/eutils/esearch.fcgi",
		SourceDBLP:            "https://dblp.org/search/publ/api",
		SourceUnpaywall:       "https://api.unpaywall.org/v2",
	}
}

func (c *Client) endpoint(source string) string {
	c.mu.RLock()
	value := ""
	if c.endpoints != nil {
		value = c.endpoints[source]
	}
	c.mu.RUnlock()
	if value != "" {
		return value
	}
	return defaultEndpoints()[source]
}

func maxPDFBytesFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv("LITERATURE_MAX_PDF_MB"))
	if raw == "" {
		return defaultMaxPDFBytes
	}
	megabytes, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || megabytes <= 0 || megabytes > maxConfiguredPDFEnvMB {
		return defaultMaxPDFBytes
	}
	return megabytes * (1 << 20)
}
