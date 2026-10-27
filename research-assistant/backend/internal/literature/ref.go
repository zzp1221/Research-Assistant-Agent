package literature

import (
	"net/url"
	"path"
	"regexp"
	"strings"
)

type refKind int

const (
	refKindInvalid refKind = iota
	refKindArxiv
	refKindDOI
	refKindPDF
)

type parsedRef struct {
	kind  refKind
	value string
}

var (
	arxivModernPattern = regexp.MustCompile(`^(\d{4}\.\d{4,5})(?:v\d+)?$`)
	arxivOldPattern    = regexp.MustCompile(`^([A-Za-z-]+(?:\.[A-Za-z]{2})?/\d{7})(?:v\d+)?$`)
	doiPattern         = regexp.MustCompile(`^10\.\d{4,9}/\S+$`)
)

// parseReference classifies an arXiv identifier or URL, a DOI or DOI URL, or
// a direct PDF URL. Anything else is rejected as invalid_ref.
func parseReference(raw string) (parsedRef, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return parsedRef{}, wrapError(CodeInvalidRef, "", raw, ErrInvalidRef, "reference is empty")
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "arxiv:") {
		if id, ok := parseArxivID(value[len("arxiv:"):]); ok {
			return parsedRef{kind: refKindArxiv, value: id}, nil
		}
		return parsedRef{}, invalidRefError(raw, "invalid arXiv identifier")
	}
	if strings.HasPrefix(lower, "doi:") {
		return parseDOIRef(value[len("doi:"):], raw)
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		scheme := strings.ToLower(parsed.Scheme)
		if scheme != "http" && scheme != "https" {
			return parsedRef{}, invalidRefError(raw, "only http and https URLs can be imported")
		}
		host := strings.ToLower(parsed.Hostname())
		if host == "" {
			return parsedRef{}, invalidRefError(raw, "URL has no host")
		}
		switch {
		case host == "arxiv.org" || strings.HasSuffix(host, ".arxiv.org"):
			if id, ok := arxivIDFromPath(parsed.Path); ok {
				return parsedRef{kind: refKindArxiv, value: id}, nil
			}
			if ids := parsed.Query().Get("id_list"); ids != "" {
				first := strings.Split(ids, ",")[0]
				if id, ok := parseArxivID(first); ok {
					return parsedRef{kind: refKindArxiv, value: id}, nil
				}
			}
			return parsedRef{}, invalidRefError(raw, "arXiv URL does not contain a valid identifier")
		case host == "doi.org" || host == "dx.doi.org" || strings.HasSuffix(host, ".doi.org"):
			doi := strings.TrimPrefix(parsed.Path, "/")
			if decoded, err := url.PathUnescape(doi); err == nil {
				doi = decoded
			}
			return parseDOIRef(doi, raw)
		default:
			return parsedRef{kind: refKindPDF, value: value}, nil
		}
	}
	if id, ok := parseArxivID(value); ok {
		return parsedRef{kind: refKindArxiv, value: id}, nil
	}
	if _, ok := parseDOI(value); ok {
		return parseDOIRef(value, raw)
	}
	return parsedRef{}, invalidRefError(raw, "reference is not an arXiv ID, DOI or PDF URL")
}

func parseDOIRef(rawDOI, original string) (parsedRef, error) {
	doi, ok := parseDOI(rawDOI)
	if !ok {
		return parsedRef{}, invalidRefError(original, "invalid DOI")
	}
	if id, ok := arxivIDFromArxivDOI(doi); ok {
		return parsedRef{kind: refKindArxiv, value: id}, nil
	}
	return parsedRef{kind: refKindDOI, value: doi}, nil
}

func invalidRefError(ref, message string) *Error {
	return wrapError(CodeInvalidRef, "", ref, ErrInvalidRef, message)
}

func parseArxivID(raw string) (string, bool) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", false
	}
	if strings.EqualFold(path.Ext(id), ".pdf") {
		id = strings.TrimSuffix(id, path.Ext(id))
	}
	if match := arxivModernPattern.FindStringSubmatch(id); match != nil {
		return match[1], true
	}
	if match := arxivOldPattern.FindStringSubmatch(id); match != nil {
		return match[1], true
	}
	return "", false
}

func arxivIDFromPath(rawPath string) (string, bool) {
	trimmed := strings.Trim(rawPath, "/")
	if trimmed == "" {
		return "", false
	}
	parts := strings.Split(trimmed, "/")
	if len(parts) >= 2 {
		switch strings.ToLower(parts[0]) {
		case "abs", "pdf", "format", "html":
			candidate := strings.Join(parts[1:], "/")
			return parseArxivID(candidate)
		}
	}
	if len(parts) == 1 {
		return parseArxivID(parts[0])
	}
	return "", false
}

func arxivIDFromURL(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	return arxivIDFromPath(parsed.Path)
}

func arxivIDFromArxivDOI(rawDOI string) (string, bool) {
	doi := normalizeDOI(rawDOI)
	const prefix = "10.48550/arxiv."
	if !strings.HasPrefix(strings.ToLower(doi), prefix) {
		return "", false
	}
	return parseArxivID(doi[len(prefix):])
}

func normalizedArxivID(raw string) string {
	if id, ok := parseArxivID(raw); ok {
		return id
	}
	if id, ok := arxivIDFromURL(raw); ok {
		return id
	}
	return strings.TrimSpace(raw)
}

func parseDOI(raw string) (string, bool) {
	doi := normalizeDOI(raw)
	if doi == "" || !doiPattern.MatchString(doi) {
		return "", false
	}
	return doi, true
}

func normalizeDOI(raw string) string {
	value := strings.TrimSpace(raw)
	lower := strings.ToLower(value)
	for _, prefix := range []string{
		"https://doi.org/",
		"http://doi.org/",
		"https://dx.doi.org/",
		"http://dx.doi.org/",
	} {
		if strings.HasPrefix(lower, prefix) {
			value = value[len(prefix):]
			break
		}
	}
	if strings.HasPrefix(strings.ToLower(value), "doi:") {
		value = value[len("doi:"):]
	}
	if decoded, err := url.PathUnescape(value); err == nil {
		value = decoded
	}
	value = strings.TrimSpace(value)
	value = strings.TrimRight(value, `.,;:)]}"'`)
	return value
}

func directPDFResult(raw string) Result {
	result := Result{Source: SourceURL, ID: raw, URL: raw, PDFURL: raw}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		result.Title = raw
		return result
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	name := ""
	if len(segments) > 0 {
		name = segments[len(segments)-1]
	}
	if decoded, decodeErr := url.PathUnescape(name); decodeErr == nil {
		name = decoded
	}
	name = strings.TrimSpace(name)
	if extension := path.Ext(name); strings.EqualFold(extension, ".pdf") {
		name = strings.TrimSpace(strings.TrimSuffix(name, extension))
	}
	if name == "" {
		name = parsed.Hostname()
	}
	if name == "" {
		name = raw
	}
	result.Title = name
	return result
}
