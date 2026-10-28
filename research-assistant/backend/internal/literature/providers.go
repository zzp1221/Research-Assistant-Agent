package literature

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxProviderResponseBytes = 16 << 20

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// fetch performs a rate-limited, cached provider request and returns the body.
// 429 and 503 responses are retried with Retry-After / exponential backoff so
// one throttled provider does not fail the whole search.
func (c *Client) fetch(ctx context.Context, source, rawURL string, headers map[string]string) ([]byte, error) {
	ctx = nonNilContext(ctx)
	if cached, ok := c.cache.Get(ctx, cacheKey(rawURL)); ok {
		return cached, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, wrapError(CodeInvalidQuery, source, rawURL, err, "invalid provider URL")
	}
	if err := c.limiter.Wait(ctx, parsed.Hostname()); err != nil {
		return nil, wrapError(CodeCanceled, source, rawURL, err, "provider request canceled while waiting for rate limit")
	}
	c.mu.RLock()
	httpClient := c.httpClient
	userAgent := c.userAgent
	maxRetries := c.maxRetries
	retryBaseDelay := c.retryBaseDelay
	c.mu.RUnlock()

	for attempt := 0; ; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, wrapError(CodeInternal, source, rawURL, err, "cannot build provider request")
		}
		request.Header.Set("User-Agent", userAgent)
		if source == SourceArxiv {
			request.Header.Set("Accept", "application/atom+xml, application/xml;q=0.9")
		} else {
			request.Header.Set("Accept", "application/json")
		}
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := httpClient.Do(request)
		if err != nil {
			return nil, wrapError(CodeUpstream, source, rawURL, err, "provider request failed")
		}
		if response.StatusCode == http.StatusNotFound {
			response.Body.Close()
			return nil, notFoundError(source, rawURL, "provider record not found")
		}
		if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusServiceUnavailable {
			retryAfter := response.Header.Get("Retry-After")
			response.Body.Close()
			if attempt >= maxRetries {
				return nil, wrapError(CodeRateLimited, source, rawURL, nil, fmt.Sprintf("provider returned HTTP %d after %d retries", response.StatusCode, attempt))
			}
			delay := retryDelay(retryAfter, retryBaseDelay, attempt)
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, wrapError(CodeCanceled, source, rawURL, ctx.Err(), "provider retry canceled")
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			response.Body.Close()
			return nil, wrapError(CodeUpstream, source, rawURL, nil, fmt.Sprintf("provider returned HTTP %d", response.StatusCode))
		}
		body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes))
		response.Body.Close()
		if err != nil {
			return nil, wrapError(CodeUpstream, source, rawURL, err, "cannot read provider response")
		}
		c.cache.Set(ctx, cacheKey(rawURL), body)
		return body, nil
	}
}

// retryDelay interprets Retry-After (seconds or HTTP-date) and falls back to
// exponential backoff. Delays are capped at 2s so a hostile header cannot
// stall a request indefinitely.
func retryDelay(retryAfter string, base time.Duration, attempt int) time.Duration {
	capDelay := 2 * time.Second
	if value := strings.TrimSpace(retryAfter); value != "" {
		if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
			delay := time.Duration(seconds) * time.Second
			if delay > capDelay {
				return capDelay
			}
			return delay
		}
		if when, err := http.ParseTime(value); err == nil {
			delay := time.Until(when)
			if delay < 0 {
				return 0
			}
			if delay > capDelay {
				return capDelay
			}
			return delay
		}
	}
	delay := base << attempt
	if delay > capDelay {
		return capDelay
	}
	return delay
}

// ---- arXiv ----

type arxivFeed struct {
	Entries []struct {
		ID        string `xml:"id"`
		Title     string `xml:"title"`
		Summary   string `xml:"summary"`
		Published string `xml:"published"`
		Authors   []struct {
			Name string `xml:"name"`
		} `xml:"author"`
		Links []struct {
			Href  string `xml:"href,attr"`
			Rel   string `xml:"rel,attr"`
			Title string `xml:"title,attr"`
		} `xml:"link"`
		DOI string `xml:"doi"`
	} `xml:"entry"`
}

func (c *Client) searchArxiv(ctx context.Context, query string, limit, yearFrom int) ([]Result, error) {
	endpoint := c.endpoint(SourceArxiv)
	target := endpoint + "?search_query=" + url.QueryEscape("all:"+query) +
		"&start=0&max_results=" + strconv.Itoa(clampLimit(limit, 1, 50)) +
		"&sortBy=relevance&sortOrder=descending"
	body, err := c.fetch(ctx, SourceArxiv, target, nil)
	if err != nil {
		return nil, err
	}
	var feed arxivFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, malformedPayloadError(SourceArxiv, err)
	}
	results := make([]Result, 0, len(feed.Entries))
	for _, entry := range feed.Entries {
		arxivID := normalizedArxivID(entry.ID)
		result := Result{
			Source:   SourceArxiv,
			ID:       arxivID,
			Title:    cleanText(entry.Title),
			Abstract: cleanText(entry.Summary),
			ArxivID:  arxivID,
			DOI:      normalizeDOI(entry.DOI),
		}
		if result.ID == "" {
			result.ID = result.ArxivID
		}
		if result.ArxivID != "" {
			result.URL = "https://arxiv.org/abs/" + result.ArxivID
			result.PDFURL = "https://arxiv.org/pdf/" + result.ArxivID + ".pdf"
		}
		for _, author := range entry.Authors {
			if name := cleanText(author.Name); name != "" {
				result.Authors = append(result.Authors, name)
			}
		}
		for _, link := range entry.Links {
			if strings.EqualFold(link.Title, "pdf") || strings.HasSuffix(strings.ToLower(link.Href), ".pdf") {
				result.PDFURL = link.Href
			}
		}
		if year := parseYear(entry.Published); year > 0 {
			result.Year = year
		}
		results = append(results, result)
	}
	return filterByYear(results, yearFrom), nil
}

// ---- OpenAlex ----

type openAlexWork struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	DisplayName     string   `json:"display_name"`
	DOI             string   `json:"doi"`
	PublicationYear int      `json:"publication_year"`
	Citations       int      `json:"cited_by_count"`
	ReferencedWorks []string `json:"referenced_works"`
	OpenAccess      struct {
		IsOA bool `json:"is_oa"`
	} `json:"open_access"`
	Authorships []struct {
		Author struct {
			DisplayName string `json:"display_name"`
		} `json:"author"`
	} `json:"authorships"`
	PrimaryLocation struct {
		LandingPageURL string `json:"landing_page_url"`
		PDFURL         string `json:"pdf_url"`
		License        string `json:"license"`
	} `json:"primary_location"`
}

func (c *Client) searchOpenAlex(ctx context.Context, query string, limit, yearFrom int) ([]Result, error) {
	endpoint := c.endpoint(SourceOpenAlex)
	target := endpoint + "?search=" + url.QueryEscape(query) +
		"&per-page=" + strconv.Itoa(clampLimit(limit, 1, 50))
	if mailto := c.openAlexMailto; mailto != "" {
		target += "&mailto=" + url.QueryEscape(mailto)
	}
	body, err := c.fetch(ctx, SourceOpenAlex, target, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Results []openAlexWork `json:"results"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, malformedPayloadError(SourceOpenAlex, err)
	}
	results := make([]Result, 0, len(payload.Results))
	for _, work := range payload.Results {
		results = append(results, openAlexResult(work))
	}
	return filterByYear(results, yearFrom), nil
}

// openAlexResult maps one OpenAlex work to the normalized Result.
func openAlexResult(work openAlexWork) Result {
	title := cleanText(work.Title)
	if title == "" {
		title = cleanText(work.DisplayName)
	}
	result := Result{
		Source:     SourceOpenAlex,
		ID:         strings.TrimPrefix(work.ID, "https://openalex.org/"),
		Title:      title,
		Year:       work.PublicationYear,
		DOI:        normalizeDOI(work.DOI),
		URL:        work.PrimaryLocation.LandingPageURL,
		PDFURL:     firstNonEmpty(work.PrimaryLocation.PDFURL, work.PrimaryLocation.LandingPageURL),
		Citations:  work.Citations,
		OpenAccess: work.OpenAccess.IsOA,
		License:    strings.TrimSpace(work.PrimaryLocation.License),
	}
	if result.DOI != "" {
		result.URL = "https://doi.org/" + result.DOI
	}
	for _, authorship := range work.Authorships {
		if name := cleanText(authorship.Author.DisplayName); name != "" {
			result.Authors = append(result.Authors, name)
		}
	}
	return result
}

// ---- Crossref ----

type crossrefWork struct {
	DOI       string   `json:"DOI"`
	Title     []string `json:"title"`
	Container []string `json:"container-title"`
	URL       string   `json:"URL"`
	Author    []struct {
		Given  string `json:"given"`
		Family string `json:"family"`
		Name   string `json:"name"`
	} `json:"author"`
	Issued struct {
		DateParts [][]int `json:"date-parts"`
	} `json:"issued"`
	Link []struct {
		URL         string `json:"URL"`
		ContentType string `json:"content-type"`
	} `json:"link"`
}

func (c *Client) searchCrossref(ctx context.Context, query string, limit, yearFrom int) ([]Result, error) {
	endpoint := c.endpoint(SourceCrossref)
	target := endpoint + "?query=" + url.QueryEscape(query) + "&rows=" + strconv.Itoa(clampLimit(limit, 1, 50))
	body, err := c.fetch(ctx, SourceCrossref, target, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Message struct {
			Items []crossrefWork `json:"items"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, malformedPayloadError(SourceCrossref, err)
	}
	results := make([]Result, 0, len(payload.Message.Items))
	for _, item := range payload.Message.Items {
		results = append(results, crossrefResult(item))
	}
	return filterByYear(results, yearFrom), nil
}

func crossrefResult(item crossrefWork) Result {
	result := Result{
		Source: SourceCrossref,
		DOI:    normalizeDOI(item.DOI),
		URL:    item.URL,
	}
	if len(item.Title) > 0 {
		result.Title = cleanText(item.Title[0])
	}
	if len(item.Container) > 0 {
		result.Venue = cleanText(item.Container[0])
	}
	if result.DOI != "" {
		result.ID = result.DOI
		if result.URL == "" {
			result.URL = "https://doi.org/" + result.DOI
		}
	}
	for _, author := range item.Author {
		name := strings.TrimSpace(strings.TrimSpace(author.Given + " " + author.Family))
		if name == "" {
			name = cleanText(author.Name)
		}
		if name != "" {
			result.Authors = append(result.Authors, name)
		}
	}
	if len(item.Issued.DateParts) > 0 && len(item.Issued.DateParts[0]) > 0 {
		result.Year = item.Issued.DateParts[0][0]
	}
	for _, link := range item.Link {
		if strings.Contains(strings.ToLower(link.ContentType), "pdf") {
			result.PDFURL = link.URL
			break
		}
	}
	return result
}

// ---- GitHub ----

func (c *Client) searchGitHub(ctx context.Context, query string, limit, yearFrom int) ([]Result, error) {
	_ = yearFrom
	endpoint := c.endpoint(SourceGitHub)
	target := endpoint + "?q=" + url.QueryEscape(query) + "&per_page=" + strconv.Itoa(clampLimit(limit, 1, 50))
	headers := map[string]string{}
	if c.githubToken != "" {
		headers["Authorization"] = "Bearer " + c.githubToken
	}
	body, err := c.fetch(ctx, SourceGitHub, target, headers)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Items []struct {
			FullName    string `json:"full_name"`
			HTMLURL     string `json:"html_url"`
			Description string `json:"description"`
			Stars       int    `json:"stargazers_count"`
			Language    string `json:"language"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, malformedPayloadError(SourceGitHub, err)
	}
	results := make([]Result, 0, len(payload.Items))
	for _, item := range payload.Items {
		results = append(results, Result{
			Source:        SourceGitHub,
			ID:            item.FullName,
			Title:         item.FullName,
			Abstract:      cleanText(item.Description),
			URL:           item.HTMLURL,
			RepositoryURL: item.HTMLURL,
			Citations:     item.Stars,
			Venue:         item.Language,
		})
	}
	return results, nil
}

// ---- Semantic Scholar ----

type semanticScholarPaper struct {
	PaperID       string            `json:"paperId"`
	Title         string            `json:"title"`
	Abstract      string            `json:"abstract"`
	Year          int               `json:"year"`
	Venue         string            `json:"venue"`
	URL           string            `json:"url"`
	CitationCount int               `json:"citationCount"`
	ExternalIDs   map[string]string `json:"externalIds"`
	OpenAccessPDF struct {
		URL     string `json:"url"`
		License string `json:"license"`
	} `json:"openAccessPdf"`
	Authors []struct {
		Name string `json:"name"`
	} `json:"authors"`
}

func (c *Client) searchSemanticScholar(ctx context.Context, query string, limit, yearFrom int) ([]Result, error) {
	endpoint := c.endpoint(SourceSemanticScholar)
	target := endpoint + "?query=" + url.QueryEscape(query) +
		"&limit=" + strconv.Itoa(clampLimit(limit, 1, 50)) +
		"&fields=paperId,title,abstract,year,venue,url,citationCount,openAccessPdf,externalIds,authors"
	body, err := c.fetch(ctx, SourceSemanticScholar, target, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Data []semanticScholarPaper `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, malformedPayloadError(SourceSemanticScholar, err)
	}
	results := make([]Result, 0, len(payload.Data))
	for _, paper := range payload.Data {
		result := Result{
			Source:     SourceSemanticScholar,
			ID:         paper.PaperID,
			Title:      cleanText(paper.Title),
			Abstract:   cleanText(paper.Abstract),
			Year:       paper.Year,
			Venue:      cleanText(paper.Venue),
			URL:        paper.URL,
			DOI:        normalizeDOI(paper.ExternalIDs["DOI"]),
			ArxivID:    strings.TrimSpace(paper.ExternalIDs["ArXiv"]),
			Citations:  paper.CitationCount,
			OpenAccess: strings.TrimSpace(paper.OpenAccessPDF.URL) != "",
			License:    strings.TrimSpace(paper.OpenAccessPDF.License),
			PDFURL:     strings.TrimSpace(paper.OpenAccessPDF.URL),
		}
		if result.ID == "" {
			result.ID = firstNonEmpty(result.DOI, result.ArxivID, normalizeTitle(paper.Title))
		}
		if result.URL == "" && result.DOI != "" {
			result.URL = "https://doi.org/" + result.DOI
		}
		for _, author := range paper.Authors {
			if name := cleanText(author.Name); name != "" {
				result.Authors = append(result.Authors, name)
			}
		}
		results = append(results, result)
	}
	return filterByYear(results, yearFrom), nil
}

// ---- PubMed ----

func (c *Client) searchPubMed(ctx context.Context, query string, limit, yearFrom int) ([]Result, error) {
	endpoint := c.endpoint(SourcePubMed)
	target := endpoint + "?db=pubmed&term=" + url.QueryEscape(query) +
		"&retmode=json&retmax=" + strconv.Itoa(clampLimit(limit, 1, 50)) + "&sort=relevance"
	body, err := c.fetch(ctx, SourcePubMed, target, nil)
	if err != nil {
		return nil, err
	}
	var searchPayload struct {
		Result struct {
			IDList []string `json:"idlist"`
		} `json:"esearchresult"`
	}
	if err := json.Unmarshal(body, &searchPayload); err != nil {
		return nil, malformedPayloadError(SourcePubMed, err)
	}
	ids := searchPayload.Result.IDList
	if len(ids) == 0 {
		return []Result{}, nil
	}
	summaryEndpoint := strings.Replace(endpoint, "esearch", "esummary", 1)
	if summaryEndpoint == endpoint {
		summaryEndpoint = strings.TrimRight(endpoint, "/") + "/esummary"
	}
	summaryURL := summaryEndpoint + "?db=pubmed&id=" + url.QueryEscape(strings.Join(ids, ",")) + "&retmode=json"
	summaryBody, err := c.fetch(ctx, SourcePubMed, summaryURL, nil)
	if err != nil {
		return nil, err
	}
	var summaryPayload struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(summaryBody, &summaryPayload); err != nil {
		return nil, malformedPayloadError(SourcePubMed, err)
	}
	results := make([]Result, 0, len(ids))
	for _, id := range ids {
		raw, ok := summaryPayload.Result[id]
		if !ok {
			continue
		}
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		result := Result{
			Source:  SourcePubMed,
			ID:      "pubmed:" + id,
			Title:   cleanText(jsonString(item, "title")),
			Venue:   cleanText(jsonString(item, "fulljournalname")),
			URL:     "https://pubmed.ncbi.nlm.nih.gov/" + id + "/",
			Year:    parseYear(jsonString(item, "pubdate")),
			Authors: pubmedAuthors(item),
		}
		if doi := pubmedDOI(item); doi != "" {
			result.DOI = doi
		}
		results = append(results, result)
	}
	return filterByYear(results, yearFrom), nil
}

func pubmedAuthors(item map[string]any) []string {
	raw, ok := item["authors"].([]any)
	if !ok {
		return nil
	}
	authors := make([]string, 0, len(raw))
	for _, entry := range raw {
		author, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if name := cleanText(jsonString(author, "name")); name != "" {
			authors = append(authors, name)
		}
	}
	return authors
}

func pubmedDOI(item map[string]any) string {
	raw, ok := item["articleids"].([]any)
	if !ok {
		return ""
	}
	for _, entry := range raw {
		articleID, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(jsonString(articleID, "idtype"), "doi") {
			return normalizeDOI(jsonString(articleID, "value"))
		}
	}
	return ""
}

// ---- DBLP ----

func (c *Client) searchDBLP(ctx context.Context, query string, limit, yearFrom int) ([]Result, error) {
	endpoint := c.endpoint(SourceDBLP)
	target := endpoint + "?q=" + url.QueryEscape(query) + "&format=json&h=" + strconv.Itoa(clampLimit(limit, 1, 50))
	body, err := c.fetch(ctx, SourceDBLP, target, nil)
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, malformedPayloadError(SourceDBLP, err)
	}
	hits := jsonPath(payload, "result", "hits", "hit")
	list, ok := hits.([]any)
	if !ok {
		return []Result{}, nil
	}
	results := make([]Result, 0, len(list))
	for _, entry := range list {
		hit, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		info, ok := hit["info"].(map[string]any)
		if !ok {
			continue
		}
		result := Result{
			Source:  SourceDBLP,
			ID:      firstNonEmpty(jsonString(info, "key"), jsonString(info, "doi"), normalizeTitle(jsonString(info, "title"))),
			Title:   cleanText(jsonString(info, "title")),
			Venue:   cleanText(jsonString(info, "venue")),
			DOI:     normalizeDOI(jsonString(info, "doi")),
			URL:     firstNonEmpty(jsonString(info, "ee"), jsonString(info, "url")),
			Year:    parseYear(jsonString(info, "year")),
			Authors: dblpAuthors(info),
		}
		results = append(results, result)
	}
	return filterByYear(results, yearFrom), nil
}

func dblpAuthors(info map[string]any) []string {
	raw := jsonPath(info, "authors", "author")
	switch value := raw.(type) {
	case []any:
		authors := make([]string, 0, len(value))
		for _, entry := range value {
			if author, ok := entry.(map[string]any); ok {
				if name := cleanText(jsonString(author, "text")); name != "" {
					authors = append(authors, name)
				}
			} else if name := cleanText(fmt.Sprint(entry)); name != "" {
				authors = append(authors, name)
			}
		}
		return authors
	case map[string]any:
		if name := cleanText(jsonString(value, "text")); name != "" {
			return []string{name}
		}
	case string:
		if name := cleanText(value); name != "" {
			return []string{name}
		}
	}
	return nil
}

func jsonPath(root map[string]any, keys ...string) any {
	var current any = root
	for _, key := range keys {
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = mapping[key]
	}
	return current
}

func jsonString(mapping map[string]any, key string) string {
	value, ok := mapping[key]
	if !ok || value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// ---- Unpaywall ----

// unpaywallPDF resolves an open-access PDF URL for a DOI. It is best-effort:
// any error returns an empty string so DOI import can still fall back to the
// provider metadata and report pdf_unavailable.
func (c *Client) unpaywallPDF(ctx context.Context, doi string) string {
	endpoint := strings.TrimRight(c.endpoint(SourceUnpaywall), "/")
	email := strings.TrimSpace(c.openAlexMailto)
	if email == "" {
		email = "research-assistant@example.org"
	}
	target := endpoint + "/" + url.PathEscape(doi) + "?email=" + url.QueryEscape(email)
	body, err := c.fetch(ctx, SourceUnpaywall, target, nil)
	if err != nil {
		return ""
	}
	var payload struct {
		IsOA           bool `json:"is_oa"`
		BestOALocation struct {
			URLForPDF string `json:"url_for_pdf"`
			License   string `json:"license"`
		} `json:"best_oa_location"`
		OALocations []struct {
			URLForPDF string `json:"url_for_pdf"`
			License   string `json:"license"`
		} `json:"oa_locations"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	if !payload.IsOA {
		return ""
	}
	if pdf := strings.TrimSpace(payload.BestOALocation.URLForPDF); pdf != "" {
		return pdf
	}
	for _, location := range payload.OALocations {
		if pdf := strings.TrimSpace(location.URLForPDF); pdf != "" {
			return pdf
		}
	}
	return ""
}
func parseYear(raw string) int {
	raw = strings.TrimSpace(raw)
	if len(raw) < 4 {
		return 0
	}
	year, err := strconv.Atoi(raw[:4])
	if err != nil || year < 1900 || year > 2200 {
		return 0
	}
	return year
}

func clampLimit(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
