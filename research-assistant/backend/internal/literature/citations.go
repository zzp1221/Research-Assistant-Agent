package literature

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// CitationGraph is one DOI's reference / citation neighborhood.
type CitationGraph struct {
	DOI          string    `json:"doi"`
	OpenAlexID   string    `json:"openalex_id,omitempty"`
	Title        string    `json:"title,omitempty"`
	CitedByCount int       `json:"cited_by_count"`
	References   []Result  `json:"references"`
	CitedBy      []Result  `json:"cited_by"`
	Warnings     []Warning `json:"warnings,omitempty"`
}

// Citations resolves the DOI in OpenAlex and returns its references and
// citing works. Partial failures degrade to warnings instead of failing the
// whole lookup.
func (c *Client) Citations(ctx context.Context, rawDOI string, limit int) (*CitationGraph, error) {
	if c == nil {
		return nil, newError(CodeInternal, "", rawDOI, "literature client is nil")
	}
	doi := normalizeDOI(rawDOI)
	if doi == "" {
		return nil, invalidRefError(rawDOI, "invalid DOI")
	}
	if limit <= 0 {
		limit = 25
	}
	if limit > 100 {
		limit = 100
	}
	ctx = nonNilContext(ctx)

	endpoint := c.endpoint(SourceOpenAlex)
	workURL := endpoint + "?filter=doi:" + url.QueryEscape(doi)
	body, err := c.fetch(ctx, SourceOpenAlex, workURL, nil)
	if err != nil {
		return nil, err
	}
	var workPayload struct {
		Results []openAlexWork `json:"results"`
	}
	if err := json.Unmarshal(body, &workPayload); err != nil {
		return nil, malformedPayloadError(SourceOpenAlex, err)
	}
	if len(workPayload.Results) == 0 {
		return nil, notFoundError(SourceOpenAlex, doi, "DOI not found in OpenAlex")
	}
	work := workPayload.Results[0]
	graph := &CitationGraph{
		DOI:          doi,
		OpenAlexID:   strings.TrimPrefix(work.ID, "https://openalex.org/"),
		Title:        firstNonEmpty(cleanText(work.Title), cleanText(work.DisplayName)),
		CitedByCount: work.Citations,
		References:   []Result{},
		CitedBy:      []Result{},
	}

	if len(work.ReferencedWorks) > 0 {
		ids := work.ReferencedWorks
		if len(ids) > limit {
			ids = ids[:limit]
		}
		filter := make([]string, 0, len(ids))
		for _, id := range ids {
			filter = append(filter, strings.TrimPrefix(id, "https://openalex.org/"))
		}
		referenceURL := endpoint + "?filter=openalex_id:" + url.QueryEscape(strings.Join(filter, "|")) +
			"&per-page=" + strconv.Itoa(len(filter))
		if referenceBody, referenceErr := c.fetch(ctx, SourceOpenAlex, referenceURL, nil); referenceErr != nil {
			graph.Warnings = append(graph.Warnings, Warning{Source: SourceOpenAlex, Message: "references unavailable: " + referenceErr.Error()})
		} else {
			var payload struct {
				Results []openAlexWork `json:"results"`
			}
			if err := json.Unmarshal(referenceBody, &payload); err == nil {
				for _, reference := range payload.Results {
					graph.References = append(graph.References, openAlexResult(reference))
				}
			}
		}
	}

	if graph.OpenAlexID != "" {
		citingURL := endpoint + "?filter=cites:" + url.QueryEscape(graph.OpenAlexID) +
			"&per-page=" + strconv.Itoa(limit) + "&sort=cited_by_count:desc"
		if citingBody, citingErr := c.fetch(ctx, SourceOpenAlex, citingURL, nil); citingErr != nil {
			graph.Warnings = append(graph.Warnings, Warning{Source: SourceOpenAlex, Message: "citing works unavailable: " + citingErr.Error()})
		} else {
			var payload struct {
				Results []openAlexWork `json:"results"`
			}
			if err := json.Unmarshal(citingBody, &payload); err == nil {
				for _, citing := range payload.Results {
					graph.CitedBy = append(graph.CitedBy, openAlexResult(citing))
				}
			}
		}
	}
	return graph, nil
}

// String renders a compact citation graph for logs.
func (g *CitationGraph) String() string {
	if g == nil {
		return "<nil>"
	}
	return fmt.Sprintf("doi=%s references=%d cited_by=%d", g.DOI, len(g.References), len(g.CitedBy))
}
