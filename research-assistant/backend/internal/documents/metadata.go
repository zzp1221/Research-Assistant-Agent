package documents

import (
	"fmt"
	"regexp"
	"strings"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/paperid"
)

var arxivIDPattern = paperid.ArxivIDPattern
var yearPattern = regexp.MustCompile(`\b(19|20)\d{2}\b`)
var personInitialPattern = regexp.MustCompile(`\b[A-Z][a-z]+\.`)

const (
	// titleAcceptScore is the minimum score a line must reach to be treated as
	// the paper title. Address blocks and copyright notices always score far
	// below it because of their penalties.
	titleAcceptScore = 45.0
	// titleConfidenceThreshold marks rule-based extraction below which callers
	// should cross-check the title with the model.
	titleConfidenceThreshold = 0.6
)

// titlePenaltyTerms are institution/address markers. They used to be missing
// from the title blacklist, which made "Institute for Mathematical Sciences,
// Imperial College London..." win by being the longest plausible line.
var titlePenaltyTerms = []string{
	"institute", "college", "department", "campus", "université", "universite",
	"university", "avenue", "street", "road", "laboratory", "school of",
	"faculty", "academy", "research center", "research centre",
}

var copyrightPenaltyTerms = []string{
	"copyright", "permission", "all rights reserved", "provided proper attribution",
	"reproduce the tables", "solely for use", "journalistic", "scholarly works",
	"scholarly", "provided proper",
	"license", "licence", "isbn", "issn",
}

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
	rawLines := make([]string, 0, len(lines))
	for _, line := range lines {
		clean := strings.Join(strings.Fields(line), " ")
		if clean != "" {
			normalized = append(normalized, clean)
			rawLines = append(rawLines, line)
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
	bestScore := 0.0
	for i := 0; i < limit; i++ {
		line := normalized[i]
		if !plausibleTitleLine(line) {
			continue
		}
		score := scoreTitleCandidate(normalized, i, limit)
		if score > bestScore {
			bestScore = score
			title = line
			titleIndex = i
		}
	}
	if title == "" || bestScore < titleAcceptScore {
		return models.PaperMetadata{}, fmt.Errorf("paper title could not be extracted from first page")
	}
	title, titleIndex = mergeWrappedTitle(normalized, titleIndex, limit)
	metadata := models.PaperMetadata{
		Title:      title,
		Source:     "rule",
		Confidence: titleConfidence(bestScore),
	}
	if titleIndex >= 0 {
		for i := titleIndex + 1; i < limit && i <= titleIndex+5; i++ {
			line := normalized[i]
			if strings.Contains(strings.ToLower(line), "abstract") {
				break
			}
			if arxivIDPattern.MatchString(line) {
				continue
			}
			if !plausibleAuthorLine(line) {
				if len(metadata.Authors) > 0 {
					break
				}
				continue
			}
			metadata.Authors = append(metadata.Authors, splitAuthors(rawLines[i])...)
		}
		metadata.Authors = dedupeAuthors(metadata.Authors)
	}
	if metadata.Confidence > 0 && metadata.Confidence < titleConfidenceThreshold {
		metadata.Warnings = append(metadata.Warnings, "paper title extraction confidence is low; verify paper_metadata.title before trusting repository search")
	}
	if match := yearPattern.FindString(page); match != "" {
		fmt.Sscanf(match, "%d", &metadata.Year)
	}
	metadata.Venue = detectVenue(page)
	if id := paperid.Find(page); id != "" {
		metadata.ArxivID = id
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

// scoreTitleCandidate scores one line as a title candidate using position and
// structural features instead of raw length.
func scoreTitleCandidate(lines []string, index, total int) float64 {
	line := lines[index]
	score := 0.0
	ratio := 1.0
	if total > 0 {
		ratio = float64(index) / float64(total)
	}
	switch {
	case ratio <= 0.4:
		score += 40
	case ratio <= 0.6:
		score += 20
	}
	runes := len([]rune(line))
	switch {
	case runes >= 20 && runes <= 120:
		score += 15
	case runes >= 8 && runes < 20:
		score += 5
	}
	if index > 0 && arxivIDPattern.MatchString(lines[index-1]) {
		score += 20
	}
	if index+1 < len(lines) && arxivIDPattern.MatchString(lines[index+1]) {
		score += 20
	}
	if index+1 < len(lines) && plausibleAuthorLine(lines[index+1]) {
		score += 20
	}
	if looksLikeAuthorLine(line) {
		// A long author list can otherwise outscore the real title by length
		// and position alone, which is exactly the D1 failure mode.
		score -= 60
	}
	lower := strings.ToLower(line)
	for _, term := range titlePenaltyTerms {
		if strings.Contains(lower, term) {
			score -= 80
			break
		}
	}
	for _, term := range copyrightPenaltyTerms {
		if strings.Contains(lower, term) {
			score -= 80
			break
		}
	}
	if startsWithDigit(line) {
		score -= 60
	}
	return score
}

func titleConfidence(score float64) float64 {
	switch {
	case score >= 90:
		return 0.95
	case score >= 75:
		return 0.85
	case score >= titleAcceptScore:
		return 0.7
	default:
		return 0.3
	}
}

func startsWithDigit(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	return trimmed[0] >= '0' && trimmed[0] <= '9'
}

func looksLikeAuthorLine(line string) bool {
	lower := strings.ToLower(line)
	if strings.Contains(lower, " and ") && commaCount(line) >= 1 {
		return true
	}
	if commaCount(line) >= 2 && personInitialPattern.MatchString(line) {
		return true
	}
	// Author blocks in pdftotext output usually carry affiliation superscripts
	// (1, 2, *, †, ‡) and several capitalized names, while titles do not.
	hasAffiliationMark := strings.ContainsAny(line, "0123456789*†‡∗§¶")
	if !hasAffiliationMark {
		return false
	}
	capitalized := 0
	for _, token := range strings.Fields(line) {
		trimmed := strings.Trim(token, "0123456789*†‡∗§¶,;:")
		if len([]rune(trimmed)) < 2 {
			continue
		}
		first := []rune(trimmed)[0]
		if first >= 'A' && first <= 'Z' {
			capitalized++
		}
	}
	return capitalized >= 2
}

func commaCount(line string) int {
	return strings.Count(line, ",")
}

// mergeWrappedTitle joins a title that continues on the next physical line.
func mergeWrappedTitle(lines []string, index, limit int) (string, int) {
	if index < 0 || index+1 >= limit {
		return indexOrEmpty(lines, index), index
	}
	next := lines[index+1]
	if arxivIDPattern.MatchString(next) || plausibleAuthorLine(next) || strings.Contains(strings.ToLower(next), "abstract") {
		return lines[index], index
	}
	score := scoreTitleCandidate(lines, index+1, limit) - 40 // ignore position bonus
	if len([]rune(next)) < 12 || score < 15 || strings.HasSuffix(next, ".") {
		return lines[index], index
	}
	return strings.TrimSpace(lines[index] + " " + next), index
}

func indexOrEmpty(lines []string, index int) string {
	if index < 0 || index >= len(lines) {
		return ""
	}
	return lines[index]
}

func plausibleTitleLine(line string) bool {
	lower := strings.ToLower(line)
	if len([]rune(line)) < 8 || len([]rune(line)) > 300 {
		return false
	}
	blocked := []string{
		"proceedings", "conference", "symposium", "copyright", "permission", "ibm",
		"research center", "research centre", "university", "@", "http", "www",
		"doi", "arxiv", "keywords", "abstract",
	}
	for _, term := range append(append([]string{}, blocked...), titlePenaltyTerms...) {
		if strings.Contains(lower, term) {
			return false
		}
	}
	for _, term := range copyrightPenaltyTerms {
		if strings.Contains(lower, term) {
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
	blocked := []string{
		"abstract", "introduction", "keywords", "university", "research center",
		"research centre", "institute", "department", "college", "campus",
		"avenue", "street", "road", "ibm", "@", "http", "www", "doi", "arxiv",
		"e-mail", "email", "copyright", "license", "google brain", "google research",
		"microsoft research", "meta ai", "openai", "labs", "laboratory", "school of",
	}
	for _, term := range blocked {
		if strings.Contains(lower, term) {
			return false
		}
	}
	// Positive features: name-like lines carry "and", commas, or initials.
	if strings.Contains(lower, " and ") || commaCount(line) >= 1 || personInitialPattern.MatchString(line) {
		return true
	}
	words := 0
	for _, token := range strings.Fields(line) {
		trimmed := strings.Trim(token, "0123456789*†‡∗§¶,;:")
		if trimmed == "" {
			continue
		}
		first := []rune(trimmed)[0]
		if first >= 'A' && first <= 'Z' {
			words++
		}
	}
	return words >= 2
}

var authorFootnotePattern = regexp.MustCompile(`[0-9,;:*†‡∗§¶]+[a-z]?$`)

var authorColumnSplitPattern = regexp.MustCompile(`\s{2,}|\t+`)

// splitAuthors understands both comma/"and" author lists and the multi-column
// author blocks produced by pdftotext for two-column papers.
func splitAuthors(rawLine string) []string {
	authors := make([]string, 0, 8)
	for _, column := range authorColumnSplitPattern.Split(strings.TrimSpace(rawLine), -1) {
		column = strings.TrimSpace(column)
		if column == "" {
			continue
		}
		column = strings.ReplaceAll(column, " and ", ", ")
		for _, part := range strings.Split(column, ",") {
			part = strings.TrimSpace(part)
			part = strings.Trim(part, "0123456789*†‡∗§¶ ")
			part = strings.TrimSpace(authorFootnotePattern.ReplaceAllString(part, ""))
			if part != "" && len([]rune(part)) <= 80 {
				authors = append(authors, part)
			}
		}
	}
	return authors
}

func dedupeAuthors(authors []string) []string {
	out := make([]string, 0, len(authors))
	seen := map[string]struct{}{}
	for _, author := range authors {
		key := strings.ToLower(strings.TrimSpace(author))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, strings.TrimSpace(author))
	}
	return out
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
