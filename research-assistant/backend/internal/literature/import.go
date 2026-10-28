package literature

import (
	"bytes"
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

// Metadata resolves a reference without downloading the PDF. Plan creation uses
// it to get arXiv/Crossref metadata quickly and downloads the PDF separately
// under a bounded budget.
func (c *Client) Metadata(ctx context.Context, rawRef string) (*Result, error) {
	if c == nil {
		return nil, newError(CodeInternal, "", rawRef, "literature client is nil")
	}
	ctx = nonNilContext(ctx)
	parsed, err := parseReference(rawRef)
	if err != nil {
		return nil, err
	}
	switch parsed.kind {
	case refKindArxiv:
		return c.arxivMetadata(ctx, parsed.value)
	case refKindDOI:
		return c.crossrefMetadata(ctx, parsed.value)
	case refKindPDF:
		result := directPDFResult(parsed.value)
		return &result, nil
	default:
		return nil, invalidRefError(rawRef, "unsupported reference kind")
	}
}

// Import resolves a reference (arXiv ID/URL, DOI, or direct PDF URL), fetches
// its metadata and downloads the open-access PDF.
func (c *Client) Import(ctx context.Context, rawRef string) (*ImportResult, error) {
	if c == nil {
		return nil, newError(CodeInternal, "", rawRef, "literature client is nil")
	}
	ctx = nonNilContext(ctx)
	parsed, err := parseReference(rawRef)
	if err != nil {
		return nil, err
	}
	switch parsed.kind {
	case refKindArxiv:
		return c.importArxiv(ctx, parsed.value)
	case refKindDOI:
		return c.importDOI(ctx, parsed.value)
	case refKindPDF:
		result := directPDFResult(parsed.value)
		pdf, downloadErr := c.downloadPDF(ctx, result.Source, result.PDFURL)
		if downloadErr != nil {
			return nil, downloadErr
		}
		return &ImportResult{Result: result, PDF: pdf, Source: result.Source}, nil
	default:
		return nil, invalidRefError(rawRef, "unsupported reference kind")
	}
}

func (c *Client) importArxiv(ctx context.Context, arxivID string) (*ImportResult, error) {
	result, err := c.arxivMetadata(ctx, arxivID)
	if err != nil {
		return nil, err
	}
	pdfURL := result.PDFURL
	if pdfURL == "" {
		pdfURL = "https://arxiv.org/pdf/" + arxivID + ".pdf"
	}
	pdf, err := c.downloadPDF(ctx, SourceArxiv, pdfURL)
	if err != nil {
		return nil, err
	}
	return &ImportResult{Result: *result, PDF: pdf, Source: SourceArxiv}, nil
}

func (c *Client) arxivMetadata(ctx context.Context, arxivID string) (*Result, error) {
	endpoint := c.endpoint(SourceArxiv)
	target := endpoint + "?id_list=" + url.QueryEscape(arxivID) + "&max_results=1"
	body, err := c.fetch(ctx, SourceArxiv, target, nil)
	if err != nil {
		return nil, err
	}
	var feed arxivFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, malformedPayloadError(SourceArxiv, err)
	}
	if len(feed.Entries) == 0 {
		return nil, notFoundError(SourceArxiv, arxivID, "arXiv record not found")
	}
	entry := feed.Entries[0]
	result := &Result{
		Source:   SourceArxiv,
		ID:       arxivID,
		ArxivID:  arxivID,
		Title:    cleanText(entry.Title),
		Abstract: cleanText(entry.Summary),
		DOI:      normalizeDOI(entry.DOI),
		URL:      "https://arxiv.org/abs/" + arxivID,
		PDFURL:   "https://arxiv.org/pdf/" + arxivID + ".pdf",
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
	result.Year = parseYear(entry.Published)
	if result.Title == "" {
		result.Title = arxivID
	}
	return result, nil
}

func (c *Client) importDOI(ctx context.Context, doi string) (*ImportResult, error) {
	result, err := c.crossrefMetadata(ctx, doi)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(result.PDFURL) == "" {
		// Crossref rarely advertises an OA PDF; ask Unpaywall before giving up.
		if pdfURL := c.unpaywallPDF(ctx, doi); pdfURL != "" {
			result.PDFURL = pdfURL
			result.OpenAccess = true
		}
	}
	if strings.TrimSpace(result.PDFURL) == "" {
		return nil, pdfUnavailableError(SourceCrossref, doi, "no open-access PDF is advertised for this DOI; upload the PDF manually")
	}
	pdf, err := c.downloadPDF(ctx, SourceCrossref, result.PDFURL)
	if err != nil {
		return nil, err
	}
	return &ImportResult{Result: *result, PDF: pdf, Source: SourceCrossref}, nil
}

func (c *Client) crossrefMetadata(ctx context.Context, doi string) (*Result, error) {
	endpoint := c.endpoint(SourceCrossref)
	target := strings.TrimRight(endpoint, "/") + "/" + url.PathEscape(doi)
	body, err := c.fetch(ctx, SourceCrossref, target, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Message crossrefWork `json:"message"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, malformedPayloadError(SourceCrossref, err)
	}
	result := crossrefResult(payload.Message)
	if result.Title == "" {
		return nil, notFoundError(SourceCrossref, doi, "DOI record has no title")
	}
	result.Source = SourceCrossref
	return &result, nil
}

// downloadPDF fetches a PDF with a hard size cap and validates the header.
func (c *Client) downloadPDF(ctx context.Context, source, rawURL string) ([]byte, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, pdfUnavailableError(source, "", "no PDF URL available")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, wrapError(CodePDFUnavailable, source, rawURL, err, "invalid PDF URL")
	}
	// Cache downloaded PDFs by URL so repeated imports of the same paper do not
	// re-download megabytes (and cannot time out on a slow link repeatedly).
	cacheKeyValue := "pdf:" + rawURL
	if cached, ok := c.cache.Get(ctx, cacheKeyValue); ok && len(cached) > 0 {
		if !bytes.HasPrefix(cached, []byte("%PDF")) {
			return nil, wrapError(CodeInvalidPDF, source, rawURL, ErrInvalidPDF, "cached content is not a PDF")
		}
		return cached, nil
	}
	if err := c.limiter.Wait(ctx, parsed.Hostname()); err != nil {
		return nil, wrapError(CodeCanceled, source, rawURL, err, "PDF download canceled")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, wrapError(CodePDFUnavailable, source, rawURL, err, "cannot build PDF request")
	}
	c.mu.RLock()
	httpClient := c.httpClient
	userAgent := c.userAgent
	limit := c.maxPDFBytes
	c.mu.RUnlock()
	request.Header.Set("User-Agent", userAgent)
	request.Header.Set("Accept", "application/pdf")
	// PDFs are much larger than API responses; a provider call timeout is too
	// short for a 2 MB download on a slow link. The surrounding context still
	// bounds the whole import operation.
	downloadClient := *httpClient
	if downloadClient.Timeout < 5*time.Minute {
		downloadClient.Timeout = 5 * time.Minute
	}
	response, err := downloadClient.Do(request)
	if err != nil {
		return nil, wrapError(CodePDFUnavailable, source, rawURL, err, fmt.Sprintf("PDF download failed: %v", err))
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, wrapError(CodePDFUnavailable, source, rawURL, nil, fmt.Sprintf("PDF download returned HTTP %d", response.StatusCode))
	}
	if response.ContentLength > limit {
		return nil, wrapError(CodePDFTooLarge, source, rawURL, ErrPDFTooLarge, fmt.Sprintf("PDF is %d bytes, limit %d", response.ContentLength, limit))
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, wrapError(CodePDFUnavailable, source, rawURL, err, fmt.Sprintf("cannot read PDF body: %v", err))
	}
	if int64(len(body)) > limit {
		return nil, wrapError(CodePDFTooLarge, source, rawURL, ErrPDFTooLarge, fmt.Sprintf("PDF exceeds %d bytes", limit))
	}
	if !bytes.HasPrefix(body, []byte("%PDF")) {
		return nil, wrapError(CodeInvalidPDF, source, rawURL, ErrInvalidPDF, "downloaded content is not a PDF")
	}
	c.cache.Set(ctx, cacheKeyValue, body)
	return body, nil
}

// SearchYearFromParser is a helper used by API handlers to parse the year_from
// query parameter without duplicating error handling.
func SearchYearFromParser(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 1900 || value > 2200 {
		return 0
	}
	return value
}
