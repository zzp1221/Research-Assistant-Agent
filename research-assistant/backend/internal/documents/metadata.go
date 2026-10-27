package documents

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/paperid"
)

// Metadata sources reported on models.PaperMetadata.Source. Rule-based PDF
// extraction reports SourcePDF; a provider overlay reports the provider source
// so callers can tell where a field came from.
const (
	SourcePDF      = "pdf"
	SourceRule     = "rule"
	SourceArxiv    = "arxiv"
	SourceCrossref = "crossref"
)

const (
	// titleAcceptScore is the minimum score a line must reach to be treated as
	// the paper title. Address blocks and copyright notices always score far
	// below it because of their penalties.
	titleAcceptScore = 45.0

	// ConfidenceLowThreshold is the confidence below which extracted metadata
	// carries a warning. Callers should verify or correct such metadata with a
	// trusted provider before using it for repository search.
	ConfidenceLowThreshold = 0.6

	// maxAuthorBlockLines bounds how far below the title the author block may
	// extend, so a broken layout cannot turn a whole first page into authors.
	maxAuthorBlockLines = 40

	// lowConfidenceWarning and missingAuthorsWarning are the actionable
	// warnings attached by ExtractMetadata.
	lowConfidenceWarning  = "paper metadata confidence is low; verify title, authors and year before trusting repository search"
	missingAuthorsWarning = "paper authors could not be extracted from the first page"
)

// authorNameTrimCutset holds punctuation that can legitimately wrap a name once
// affiliation markers have been stripped.
var authorNameTrimCutset = " ,;:-–—*†‡∗§¶#"

var (
	arxivIDPattern           = paperid.ArxivIDPattern
	yearPattern              = regexp.MustCompile(`\b(19|20)\d{2}\b`)
	personInitialPattern     = regexp.MustCompile(`\b[A-Z][a-z]+\.`)
	authorColumnSplitPattern = regexp.MustCompile(`\s{2,}|\t+`)
	emailPattern             = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	multiDigitPattern        = regexp.MustCompile(`\d{3,}`)
	venueAcronymPattern      = regexp.MustCompile(`\b(?:NEURIPS|NIPS|ICML|ICLR|EMNLP|NAACL|ACL|CVPR|ICCV|ECCV|AAAI|IJCAI|SIGMOD|VLDB|SIGIR|CIKM|ICDM|KDD|WWW|JMLR|TACL|TPAMI|USENIX|OSDI|SOSP|NSDI|SIGCOMM|CHI|UIST|ICDE|PODS|STOC|FOCS|SODA|AISTATS|COLT|ICRA|IROS|MICCAI)\b`)
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

// institutionTerms are affiliation markers used to reject the address blocks
// that pdftotext interleaves with author names. Matching is accent-folded and
// case-insensitive.
var institutionTerms = []string{
	"university", "universite", "universitat", "universidad", "universita",
	"institute", "instituto", "institution", "department", "dept.", "dept ",
	"faculty", "school", "college", "academy", "laboratory", "laboratoire",
	"research", "labs", "centre", "center", "campus", "avenue", "street",
	"road", "boulevard", "place ", "ecole", "polytechnique", "hospital",
	"po box", "p.o. box", "corporation", "inc.", "ltd.", "gmbh",
	"e-mail", "email", "http", "www", "doi", "arxiv", "copyright",
	"license", "licence", "isbn", "issn", "google brain", "google research",
	"microsoft research", "meta ai", "openai", "deepmind", "ibm", "at&t",
}

// publicationContextTerms mark a line as a venue/publication citation, which is
// a much better year source than the arXiv stamp added when a preprint was
// last updated.
var publicationContextTerms = []string{
	"conference", "proceedings", "symposium", "workshop", "journal",
	"transactions", "annual meeting", "springer", "elsevier", "acm press",
	"ieee", "usenix", "mit press", "cambridge", "oxford", "publisher",
}

// headingTerms reject section headings that would otherwise look like a short
// two-word author line ("Related Work", "1 Introduction").
var headingTerms = []string{
	"abstract", "introduction", "keywords", "references", "acknowledg",
	"appendix", "related work", "background",
}

// authorStopWords are fragments that survive splitting but are never names.
var authorStopWords = map[string]struct{}{
	"and": {}, "et": {}, "al": {}, "the": {}, "of": {}, "for": {},
	"with": {}, "email": {}, "e-mail": {},
}

// ExtractMetadata extracts paper metadata from the first page of a document.
// It intentionally uses deterministic rules first; the caller may fall back to
// an LLM or to a trusted provider (see ApplyProviderMetadata) only when a
// required field is still missing.
func ExtractMetadata(document *Document) (models.PaperMetadata, error) {
	if document == nil || !document.HasText() || len(document.Pages) == 0 {
		return models.PaperMetadata{}, fmt.Errorf("document has no extractable text")
	}
	page := document.Pages[0].Text
	normalized, rawLines := normalizePageLines(page)
	abstractIndex := findAbstractLine(normalized)
	limit := len(normalized)
	if abstractIndex > 0 {
		limit = abstractIndex
	}
	title, titleIndex, titleScore := selectTitle(normalized, limit)
	if title == "" || titleScore < titleAcceptScore {
		return models.PaperMetadata{}, fmt.Errorf("paper title could not be extracted from first page")
	}
	title, titleIndex = mergeWrappedTitle(normalized, titleIndex, limit)
	metadata := models.PaperMetadata{
		Title:  title,
		Source: SourcePDF,
	}
	if titleIndex >= 0 {
		metadata.Authors = extractAuthors(rawLines, normalized, titleIndex+1, limit)
	}
	metadata.Year = detectYear(normalized)
	metadata.Venue = detectVenue(page)
	if id := paperid.Find(page); id != "" {
		metadata.ArxivID = id
	}
	if abstractIndex >= 0 {
		metadata.Abstract = composeAbstract(normalized, abstractIndex)
	}
	metadata.Confidence = metadataConfidence(titleScore, metadata)
	metadata.Warnings = metadataWarnings(metadata)
	return metadata, nil
}

// normalizePageLines keeps one entry per non-empty physical line. The normalized
// slice is used for scoring; the raw slice keeps the pdftotext column gaps that
// author splitting depends on.
func normalizePageLines(page string) ([]string, []string) {
	lines := strings.Split(page, "\n")
	normalized := make([]string, 0, len(lines))
	rawLines := make([]string, 0, len(lines))
	for _, line := range lines {
		clean := strings.Join(strings.Fields(line), " ")
		if clean == "" {
			continue
		}
		normalized = append(normalized, clean)
		rawLines = append(rawLines, line)
	}
	return normalized, rawLines
}

// findAbstractLine locates the abstract heading. "Abstractive ..." must not
// count, so the prefix has to end at a word boundary.
func findAbstractLine(lines []string) int {
	for index, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "abstract" ||
			strings.HasPrefix(lower, "abstract ") ||
			strings.HasPrefix(lower, "abstract.") ||
			strings.HasPrefix(lower, "abstract:") ||
			strings.HasPrefix(lower, "abstract—") ||
			strings.HasPrefix(lower, "abstract–") {
			return index
		}
	}
	return -1
}

// selectTitle scores every line before the abstract and returns the best
// candidate together with its index and score.
func selectTitle(lines []string, limit int) (string, int, float64) {
	title := ""
	titleIndex := -1
	bestScore := 0.0
	for index := 0; index < limit; index++ {
		line := lines[index]
		if !plausibleTitleLine(line) {
			continue
		}
		score := scoreTitleCandidate(lines, index, limit)
		if score > bestScore {
			bestScore = score
			title = line
			titleIndex = index
		}
	}
	return title, titleIndex, bestScore
}

// composeAbstract joins the abstract paragraphs, stopping at the first section
// heading that follows them.
func composeAbstract(lines []string, abstractIndex int) string {
	builder := strings.Builder{}
	for index := abstractIndex; index < len(lines); index++ {
		line := lines[index]
		lower := strings.ToLower(line)
		if index > abstractIndex && (strings.HasPrefix(lower, "1 introduction") || strings.HasPrefix(lower, "introduction") || strings.HasPrefix(lower, "keywords")) {
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
	return strings.TrimSpace(builder.String())
}

// metadataConfidence folds the title score and the presence of the other
// identifying fields into one 0..1 number.
func metadataConfidence(titleScore float64, metadata models.PaperMetadata) float64 {
	confidence := titleConfidence(titleScore)
	if len(metadata.Authors) == 0 {
		confidence -= 0.3
	}
	if metadata.Year == 0 {
		confidence -= 0.15
	}
	if metadata.Venue != "" {
		confidence += 0.03
	}
	if metadata.ArxivID != "" {
		confidence += 0.05
	}
	return clampConfidence(confidence)
}

func metadataWarnings(metadata models.PaperMetadata) []string {
	warnings := make([]string, 0, 2)
	if len(metadata.Authors) == 0 {
		warnings = append(warnings, missingAuthorsWarning)
	}
	if metadata.Confidence < ConfidenceLowThreshold {
		warnings = append(warnings, lowConfidenceWarning)
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

func clampConfidence(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// detectYear picks the most trustworthy year on the first page. A publication
// citation beats the arXiv stamp: for the attention-2017 fixture the stamp says
// "2 Aug 2023" while the paper was published at NIPS 2017.
func detectYear(lines []string) int {
	bestYear := 0
	bestScore := 0
	for index, line := range lines {
		lower := strings.ToLower(line)
		arxivStamp := isArxivStampLine(line)
		score := 10
		switch {
		case arxivStamp:
			score = 30
		case hasCopyrightMark(lower):
			score = 60
		}
		if !arxivStamp && hasPublicationContext(lines, index) {
			score += 100
		}
		for _, match := range yearPattern.FindAllString(line, -1) {
			year, err := strconv.Atoi(match)
			if err != nil || year < 1900 || year > 2099 {
				continue
			}
			if score > bestScore {
				bestScore = score
				bestYear = year
			}
		}
	}
	return bestYear
}

// hasPublicationContext reports whether the line or one of its neighbours cites
// a venue, which makes a year on the line a publication year.
func hasPublicationContext(lines []string, index int) bool {
	for offset := -1; offset <= 1; offset++ {
		position := index + offset
		if position < 0 || position >= len(lines) {
			continue
		}
		line := lines[position]
		if venueAcronymPattern.MatchString(strings.ToUpper(line)) {
			return true
		}
		lower := strings.ToLower(line)
		for _, term := range publicationContextTerms {
			if strings.Contains(lower, term) {
				return true
			}
		}
	}
	return false
}

func hasCopyrightMark(lower string) bool {
	return strings.Contains(lower, "copyright") || strings.Contains(lower, "©") || strings.Contains(lower, "(c) ")
}

func isArxivStampLine(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(lower, "arxiv:") || strings.Contains(lower, "arxiv.org")
}

// detectVenue returns the first venue acronym on the page.
func detectVenue(text string) string {
	if match := venueAcronymPattern.FindString(strings.ToUpper(text)); match != "" {
		return match
	}
	return ""
}

// extractAuthors reads the author block between the title and the abstract.
// Affiliation and email lines are skipped instead of ending the block, because
// pdftotext interleaves them between author columns.
func extractAuthors(rawLines, normalized []string, start, limit int) []string {
	if start < 0 {
		start = 0
	}
	end := limit
	if end > len(rawLines) {
		end = len(rawLines)
	}
	if end > len(normalized) {
		end = len(normalized)
	}
	if end > start+maxAuthorBlockLines {
		end = start + maxAuthorBlockLines
	}
	names := make([]string, 0, 8)
	previousLine := -1
	for index := start; index < end; index++ {
		line := normalized[index]
		if strings.Contains(strings.ToLower(line), "abstract") {
			break
		}
		if isArxivStampLine(line) {
			continue
		}
		segments := authorLineSegments(rawLines[index])
		if len(segments) == 0 {
			continue
		}
		// A name continues on the next line only when the two author lines are
		// adjacent: an affiliation or email line between them ends the block.
		if previousLine == index-1 && len(names) > 0 && isWrappedName(names[len(names)-1], segments[0]) {
			names[len(names)-1] += " " + segments[0]
			segments = segments[1:]
		}
		names = append(names, segments...)
		previousLine = index
	}
	return dedupeAuthors(names)
}

// authorLineSegments turns one physical line into cleaned author names. It
// strips affiliation markers and emails, ignores institution lines, and rejoins
// a name that pdftotext split into two columns.
func authorLineSegments(rawLine string) []string {
	trimmed := strings.TrimSpace(rawLine)
	if trimmed == "" {
		return nil
	}
	withoutEmail := strings.TrimSpace(emailPattern.ReplaceAllString(trimmed, " "))
	if withoutEmail == "" || !looksLikeAuthorBlock(withoutEmail) {
		return nil
	}
	columns := authorColumnSplitPattern.Split(withoutEmail, -1)
	segments := make([]string, 0, 8)
	for index := 0; index < len(columns); {
		column := strings.TrimSpace(columns[index])
		index++
		if column == "" {
			continue
		}
		names := splitAuthorColumn(column)
		if len(names) == 0 {
			continue
		}
		// pdftotext occasionally emits "Ramakrishnan" and "Srikant" as two
		// columns of one name. Two bare tokens in a row are rejoined; a name
		// that already contains a space is left alone.
		if len(names) == 1 && isSingleNameWord(names[0]) {
			next := index
			for next < len(columns) && strings.TrimSpace(columns[next]) == "" {
				next++
			}
			if next < len(columns) {
				nextNames := splitAuthorColumn(strings.TrimSpace(columns[next]))
				if len(nextNames) == 1 && isSingleNameWord(nextNames[0]) {
					segments = append(segments, names[0]+" "+nextNames[0])
					index = next + 1
					continue
				}
			}
		}
		segments = append(segments, names...)
	}
	return segments
}

// splitAuthorColumn splits one column into names on commas and the word "and"
// after affiliation markers have been cut from every token.
func splitAuthorColumn(column string) []string {
	cleaned := stripAttachedMarkers(column)
	if cleaned == "" {
		return nil
	}
	cleaned = strings.ReplaceAll(cleaned, " and ", ",")
	cleaned = strings.ReplaceAll(cleaned, " & ", ",")
	names := make([]string, 0, 4)
	for _, part := range strings.Split(cleaned, ",") {
		if name := normalizeAuthorName(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// stripAttachedMarkers cuts affiliation superscripts and footnote letters from
// each token: "Blondel1;a" -> "Blondel", "Gomez ∗ †" -> "Gomez", "Jones∗" ->
// "Jones". The cut starts at the first marker rune of the token.
func stripAttachedMarkers(column string) string {
	fields := strings.Fields(column)
	kept := make([]string, 0, len(fields))
	for _, field := range fields {
		builder := strings.Builder{}
		for _, r := range field {
			if isAffiliationMarker(r) {
				break
			}
			builder.WriteRune(r)
		}
		cleaned := strings.TrimSpace(builder.String())
		if cleaned != "" {
			kept = append(kept, cleaned)
		}
	}
	return strings.Join(kept, " ")
}

// normalizeAuthorName rejects fragments that are not name shaped, so an
// address line that slipped past the institution filter cannot become an
// author.
func normalizeAuthorName(value string) string {
	name := strings.Join(strings.Fields(value), " ")
	name = strings.Trim(name, authorNameTrimCutset)
	name = strings.TrimSpace(name)
	if name == "" || !looksLikePersonName(name) {
		return ""
	}
	return name
}

func looksLikePersonName(name string) bool {
	length := utf8.RuneCountInString(name)
	if length < 2 || length > 60 {
		return false
	}
	letters := 0
	hasLower := false
	for _, r := range name {
		switch {
		case unicode.IsLetter(r):
			letters++
			if unicode.IsLower(r) {
				hasLower = true
			}
		case unicode.IsDigit(r), r == '@', isAffiliationMarker(r):
			return false
		case r == ' ' || r == '-' || r == '\'' || r == '’' || r == '.' || r == '&' || r == '(' || r == ')':
			// Separators that can appear inside a real name.
		default:
			return false
		}
	}
	if letters < 2 || !hasLower {
		return false
	}
	fields := strings.Fields(name)
	if len(fields) == 0 || len(fields) > 6 {
		return false
	}
	for _, field := range fields {
		if _, blocked := authorStopWords[strings.ToLower(field)]; blocked {
			return false
		}
	}
	first := []rune(fields[0])[0]
	if !unicode.IsUpper(first) {
		return false
	}
	return true
}

// looksLikeAuthorBlock is the line-level gate for the author region. Lines that
// merely contain capitalised words (titles, affiliations) are rejected here.
func looksLikeAuthorBlock(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > 240 {
		return false
	}
	if looksLikeAffiliationLine(trimmed) {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, term := range headingTerms {
		if strings.Contains(lower, term) {
			return false
		}
	}
	if hasAffiliationMarkerRun(trimmed) {
		return true
	}
	if strings.Contains(lower, " and ") || strings.Contains(trimmed, ",") || strings.Contains(trimmed, "&") {
		return true
	}
	// Finally accept a short line made only of name-shaped tokens.
	words := strings.Fields(trimmed)
	if len(words) < 2 || len(words) > 6 {
		return false
	}
	for _, word := range words {
		if !isNameWord(strings.Trim(word, ",.;:")) {
			return false
		}
	}
	return true
}

// looksLikeAffiliationLine rejects institutions, addresses, emails and page
// furniture. Accents are folded so "Université" matches "universite".
func looksLikeAffiliationLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	if multiDigitPattern.MatchString(trimmed) {
		return true
	}
	folded := foldDiacritics(strings.ToLower(trimmed))
	for _, term := range institutionTerms {
		if strings.Contains(folded, term) {
			return true
		}
	}
	return false
}

// hasAffiliationMarkerRun reports whether a marker is attached to a letter,
// which is what affiliation superscripts look like in pdftotext output.
func hasAffiliationMarkerRun(line string) bool {
	previousLetter := false
	for _, r := range line {
		switch {
		case unicode.IsLetter(r):
			previousLetter = true
		case isAffiliationMarker(r):
			if previousLetter {
				return true
			}
			previousLetter = false
		default:
			previousLetter = false
		}
	}
	return false
}

func isAffiliationMarker(r rune) bool {
	if unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '*', '†', '‡', '∗', '§', '¶', '#', ';':
		return true
	}
	switch r {
	case '⁰', '¹', '²', '³', '⁴', '⁵', '⁶', '⁷', '⁸', '⁹':
		return true
	}
	return false
}

// isNameWord accepts one token of a personal name.
func isNameWord(word string) bool {
	letters := 0
	first := rune(0)
	for _, r := range word {
		switch {
		case unicode.IsLetter(r):
			if first == 0 {
				first = r
			}
			letters++
		case unicode.IsDigit(r), isAffiliationMarker(r):
			return false
		case r == '-' || r == '\'' || r == '’' || r == '.' || r == '&' || r == '(' || r == ')':
			// Separators that can appear inside a real name.
		default:
			return false
		}
	}
	if letters < 2 {
		return false
	}
	return unicode.IsUpper(first)
}

func isSingleNameWord(name string) bool {
	fields := strings.Fields(name)
	return len(fields) == 1 && isNameWord(fields[0])
}

// isWrappedName reports whether next continues previous, e.g. "Renaud" on one
// line and "Lambiotte1,3;c" on the next.
func isWrappedName(previous, next string) bool {
	return isSingleNameWord(previous) && isSingleNameWord(next)
}

// splitAuthors is the single-line entry point of the author-correction step.
func splitAuthors(rawLine string) []string {
	return authorLineSegments(rawLine)
}

// foldDiacritics lowers the accents that pdftotext keeps in European author and
// institution names so institution matching is not accent sensitive.
func foldDiacritics(value string) string {
	builder := strings.Builder{}
	builder.Grow(len(value))
	for _, r := range value {
		switch r {
		case 'á', 'à', 'â', 'ä', 'ã', 'å', 'ā':
			builder.WriteRune('a')
		case 'é', 'è', 'ê', 'ë', 'ē':
			builder.WriteRune('e')
		case 'í', 'ì', 'î', 'ï', 'ī':
			builder.WriteRune('i')
		case 'ó', 'ò', 'ô', 'ö', 'õ', 'ø', 'ō':
			builder.WriteRune('o')
		case 'ú', 'ù', 'û', 'ü', 'ū':
			builder.WriteRune('u')
		case 'ç':
			builder.WriteRune('c')
		case 'ñ':
			builder.WriteRune('n')
		case 'ł':
			builder.WriteRune('l')
		case 'š':
			builder.WriteRune('s')
		case 'ž':
			builder.WriteRune('z')
		case 'ß':
			builder.WriteString("ss")
		case 'æ':
			builder.WriteString("ae")
		case 'œ':
			builder.WriteString("oe")
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

// ProviderRecord is a trusted metadata record returned by an external provider
// (arXiv, Crossref, Semantic Scholar). DOI lives here because
// models.PaperMetadata has no DOI field yet; callers that need to persist it
// take the second return value of ApplyProviderRecord.
type ProviderRecord struct {
	Source     string
	Title      string
	Authors    []string
	Year       int
	Venue      string
	DOI        string
	ArxivID    string
	Abstract   string
	Confidence float64
}

// ApplyProviderMetadata overlays trusted provider metadata on top of rule-based
// PDF extraction. Non-empty provider fields win (abstract and repository URL
// only fill gaps), the merged record reports the provider source, and a provider
// record that identifies the paper raises confidence and clears stale warnings.
// A provider that supplies authors, year and an arXiv id lifts even a badly
// parsed PDF above the low-confidence threshold.
func ApplyProviderMetadata(base models.PaperMetadata, provider models.PaperMetadata) models.PaperMetadata {
	merged := base
	contributed := false
	identified := false
	if title := strings.TrimSpace(provider.Title); title != "" {
		merged.Title = title
		contributed = true
		identified = true
	}
	if authors := cleanProviderAuthors(provider.Authors); len(authors) > 0 {
		merged.Authors = authors
		contributed = true
		identified = true
	}
	if provider.Year > 0 {
		merged.Year = provider.Year
		contributed = true
		identified = true
	}
	if venue := strings.TrimSpace(provider.Venue); venue != "" {
		merged.Venue = venue
		contributed = true
		identified = true
	}
	if arxivID := paperid.StripVersion(provider.ArxivID); arxivID != "" {
		merged.ArxivID = arxivID
		contributed = true
		identified = true
	}
	if abstract := strings.TrimSpace(provider.Abstract); abstract != "" && strings.TrimSpace(merged.Abstract) == "" {
		merged.Abstract = provider.Abstract
		contributed = true
	}
	if repo := strings.TrimSpace(provider.RepoURL); repo != "" && strings.TrimSpace(merged.RepoURL) == "" {
		merged.RepoURL = repo
		contributed = true
	}
	if !contributed {
		return merged
	}
	if source := normalizeProviderSource(provider.Source); source != "" {
		merged.Source = source
	}
	if identified {
		merged.Confidence = providerOverlayConfidence(base.Confidence, provider.Confidence, merged)
		merged.Warnings = pruneStaleWarnings(merged.Warnings, merged)
	}
	return merged
}

// ApplyProviderRecord merges a full provider record and returns the merged
// metadata plus the provider DOI (empty when absent). The DOI is returned
// separately because models.PaperMetadata has no DOI field yet.
func ApplyProviderRecord(base models.PaperMetadata, record ProviderRecord) (models.PaperMetadata, string) {
	merged := ApplyProviderMetadata(base, models.PaperMetadata{
		Title:      record.Title,
		Authors:    record.Authors,
		Year:       record.Year,
		Venue:      record.Venue,
		ArxivID:    record.ArxivID,
		Abstract:   record.Abstract,
		Source:     record.Source,
		Confidence: record.Confidence,
	})
	return merged, strings.TrimSpace(record.DOI)
}

func normalizeProviderSource(source string) string {
	normalized := strings.ToLower(strings.TrimSpace(source))
	switch normalized {
	case "":
		return ""
	case "pdf":
		return SourcePDF
	case "rule", "rules", "heuristic":
		return SourceRule
	case "arxiv", "arxiv.org":
		return SourceArxiv
	case "crossref", "cross-ref":
		return SourceCrossref
	default:
		return normalized
	}
}

// cleanProviderAuthors keeps trusted provider names intact but drops duplicates,
// empty entries and stray emails.
func cleanProviderAuthors(authors []string) []string {
	cleaned := make([]string, 0, len(authors))
	for _, author := range authors {
		name := emailPattern.ReplaceAllString(author, " ")
		name = strings.Join(strings.Fields(name), " ")
		name = strings.TrimSpace(strings.Trim(name, authorNameTrimCutset))
		if name == "" {
			continue
		}
		cleaned = append(cleaned, name)
	}
	return dedupeAuthors(cleaned)
}

// providerOverlayConfidence reports how much the merged record can be trusted.
// It never lowers the PDF-rule confidence, honours a provider-reported value,
// and uses field coverage as a floor so a provider that identifies the paper
// lifts a badly parsed PDF above the low-confidence threshold.
func providerOverlayConfidence(baseConfidence, providerConfidence float64, merged models.PaperMetadata) float64 {
	confidence := clampConfidence(baseConfidence)
	if providerConfidence > 0 {
		confidence = math.Max(confidence, clampConfidence(providerConfidence))
	}
	coverage := 0
	for _, present := range []bool{
		strings.TrimSpace(merged.Title) != "",
		len(merged.Authors) > 0,
		merged.Year > 0,
		strings.TrimSpace(merged.Venue) != "",
		strings.TrimSpace(merged.ArxivID) != "",
	} {
		if present {
			coverage++
		}
	}
	return clampConfidence(math.Max(confidence, 0.75+0.04*float64(coverage)))
}

// pruneStaleWarnings drops warnings that a provider overlay has resolved:
// low-confidence warnings once confidence is high enough and the missing-author
// warning once authors are present.
func pruneStaleWarnings(warnings []string, metadata models.PaperMetadata) []string {
	if len(warnings) == 0 {
		return nil
	}
	pruned := make([]string, 0, len(warnings))
	seen := map[string]struct{}{}
	for _, warning := range warnings {
		lower := strings.ToLower(warning)
		if strings.Contains(lower, "confidence is low") && metadata.Confidence >= ConfidenceLowThreshold {
			continue
		}
		if strings.Contains(lower, "authors could not be extracted") && len(metadata.Authors) > 0 {
			continue
		}
		if _, duplicate := seen[warning]; duplicate {
			continue
		}
		seen[warning] = struct{}{}
		pruned = append(pruned, warning)
	}
	if len(pruned) == 0 {
		return nil
	}
	return pruned
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
