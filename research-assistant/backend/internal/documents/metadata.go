package documents

import (
	"fmt"
	"regexp"
	"strings"

	"research-assistant-backend/internal/models"
)

var arxivIDPattern = regexp.MustCompile(`\b\d{4}\.\d{4,5}\b`)
var yearPattern = regexp.MustCompile(`\b(19|20)\d{2}\b`)

// ExtractMetadata extracts paper metadata from the first page of a document.
// It intentionally uses deterministic rules first; the caller may fall back to
// an LLM only when a required field is still missing.
func ExtractMetadata(document *Document) (models.PaperMetadata, error) {
	if document == nil || !document.HasText() || len(document.Pages) == 0 {
		return models.PaperMetadata{}, fmt.Errorf("document has no extractable text")
	}
	page := document.Pages[0].Text
	lines := strings.Split(page, "\n")
	normalized := make([]string, 0, len(lines))
	for _, line := range lines {
		clean := strings.Join(strings.Fields(line), " ")
		if clean != "" {
			normalized = append(normalized, clean)
		}
	}
	abstractIndex := -1
	for i, line := range normalized {
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "abstract") || strings.Contains(lower, "abstract") {
			abstractIndex = i
			break
		}
	}
	limit := len(normalized)
	if abstractIndex > 0 {
		limit = abstractIndex
	}
	title := ""
	titleIndex := -1
	for i := 0; i < limit; i++ {
		line := normalized[i]
		if !plausibleTitleLine(line) {
			continue
		}
		if title == "" || len([]rune(line)) > len([]rune(title)) {
			title = line
			titleIndex = i
		}
	}
	if title == "" {
		return models.PaperMetadata{}, fmt.Errorf("paper title could not be extracted from first page")
	}
	metadata := models.PaperMetadata{Title: title}
	if titleIndex >= 0 {
		for i := titleIndex + 1; i < limit && i <= titleIndex+3; i++ {
			line := normalized[i]
			if strings.Contains(strings.ToLower(line), "abstract") {
				break
			}
			if plausibleAuthorLine(line) {
				metadata.Authors = splitAuthors(line)
				break
			}
		}
	}
	if match := yearPattern.FindString(page); match != "" {
		fmt.Sscanf(match, "%d", &metadata.Year)
	}
	metadata.Venue = detectVenue(page)
	if match := arxivIDPattern.FindString(page); match != "" {
		metadata.ArxivID = match
	}
	if abstractIndex >= 0 {
		var builder strings.Builder
		for i := abstractIndex; i < len(normalized); i++ {
			line := normalized[i]
			lower := strings.ToLower(line)
			if i > abstractIndex && (strings.HasPrefix(lower, "1 introduction") || strings.HasPrefix(lower, "introduction") || strings.HasPrefix(lower, "keywords")) {
				break
			}
			if builder.Len() > 0 {
				builder.WriteString(" ")
			}
			builder.WriteString(line)
			if builder.Len() > 2000 {
				break
			}
		}
		metadata.Abstract = strings.TrimSpace(builder.String())
	}
	return metadata, nil
}

func plausibleTitleLine(line string) bool {
	lower := strings.ToLower(line)
	if len([]rune(line)) < 8 || len([]rune(line)) > 300 {
		return false
	}
	for _, blocked := range []string{"proceedings", "conference", "symposium", "copyright", "permission", "ibm", "research center", "university", "@", "http", "www", "doi", "arxiv", "keywords", "abstract"} {
		if strings.Contains(lower, blocked) {
			return false
		}
	}
	letters := 0
	for _, r := range line {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			letters++
		}
	}
	return letters >= 6
}

func plausibleAuthorLine(line string) bool {
	lower := strings.ToLower(line)
	if len([]rune(line)) < 4 || len([]rune(line)) > 240 {
		return false
	}
	for _, blocked := range []string{"abstract", "introduction", "keywords", "university", "research center", "ibm", "@", "http"} {
		if strings.Contains(lower, blocked) {
			return false
		}
	}
	return true
}

func splitAuthors(line string) []string {
	line = strings.ReplaceAll(line, " and ", ", ")
	parts := strings.Split(line, ",")
	authors := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, "0123456789*†‡ ")
		if part != "" && len([]rune(part)) <= 80 {
			authors = append(authors, part)
		}
	}
	return authors
}

func detectVenue(text string) string {
	upper := strings.ToUpper(text)
	for _, venue := range []string{"VLDB", "NEURIPS", "ICML", "ICLR", "ACL", "EMNLP", "CVPR", "ICCV", "AAAI", "KDD", "SIGMOD", "WWW"} {
		if strings.Contains(upper, venue) {
			return venue
		}
	}
	return ""
}
