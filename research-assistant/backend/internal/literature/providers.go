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
)

const maxProviderResponseBytes = 16 << 20

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// fetch performs a rate-limited, cached provider request and returns the body.
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, wrapError(CodeInternal, source, rawURL, err, "cannot build provider request")
	}
	c.mu.RLock()
	httpClient := c.httpClient
	userAgent := c.userAgent
	c.mu.RUnlock()
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
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound {
		return nil, notFoundError(source, rawURL, "provider record not found")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, wrapError(CodeUpstream, source, rawURL, nil, fmt.Sprintf("provider returned HTTP %d", response.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseBytes))
	if err != nil {
		return nil, wrapError(CodeUpstream, source, rawURL, err, "cannot read provider response")
	}
	c.cache.Set(ctx, cacheKey(rawURL), body)
	return body, nil
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
	ID              string `json:"id"`
	Title           string `json:"title"`
	DisplayName     string `json:"display_name"`
	DOI             string `json:"doi"`
	PublicationYear int    `json:"publication_year"`
	Citations       int    `json:"cited_by_count"`
	Authorships     []struct {
		Author struct {
			DisplayName string `json:"display_name"`
		} `json:"author"`
	} `json:"authorships"`
	PrimaryLocation struct {
		LandingPageURL string `json:"landing_page_url"`
		PDFURL         string `json:"pdf_url"`
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
		title := cleanText(work.Title)
		if title == "" {
			title = cleanText(work.DisplayName)
		}
		result := Result{
			Source:    SourceOpenAlex,
			ID:        strings.TrimPrefix(work.ID, "https://openalex.org/"),
			Title:     title,
			Year:      work.PublicationYear,
			DOI:       normalizeDOI(work.DOI),
			URL:       work.PrimaryLocation.LandingPageURL,
			PDFURL:    firstNonEmpty(work.PrimaryLocation.PDFURL, work.PrimaryLocation.LandingPageURL),
			Citations: work.Citations,
		}
		if result.DOI != "" {
			result.URL = "https://doi.org/" + result.DOI
		}
		for _, authorship := range work.Authorships {
			if name := cleanText(authorship.Author.DisplayName); name != "" {
				result.Authors = append(result.Authors, name)
			}
		}
		results = append(results, result)
	}
	return filterByYear(results, yearFrom), nil
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
