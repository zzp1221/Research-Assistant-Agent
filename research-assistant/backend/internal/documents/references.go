package documents

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ReferenceEntry is one parsed entry from a PDF's references/bibliography
// section. Raw keeps the original line(s) so a wiki Link can cite the exact
// reference text as evidence.
type ReferenceEntry struct {
	Index   int      `json:"index"`
	Raw     string   `json:"raw"`
	Page    int      `json:"page"`
	Authors []string `json:"authors,omitempty"`
	Title   string   `json:"title,omitempty"`
	Year    int      `json:"year,omitempty"`
	Venue   string   `json:"venue,omitempty"`
	DOI     string   `json:"doi,omitempty"`
	ArxivID string   `json:"arxiv_id,omitempty"`
	URL     string   `json:"url,omitempty"`
}

var (
	referenceHeadingRe = regexp.MustCompile("(?i)^\\s*(references?|bibliography|works cited|参考文献|引用文献|参考资料)\\s*[:：]?\\s*$")
	referenceStopRe    = regexp.MustCompile("(?i)^\\s*(appendix|appendices|supplementary|supplemental|acknowledg(e)?ments?|附录|致谢)\\b")
	referenceNumberRe  = regexp.MustCompile("^\\s*(\\[\\d{1,3}\\]|\\(\\d{1,3}\\)|\\d{1,3}[.)])\\s+")
	referenceDOIRe     = regexp.MustCompile("(?i)\\b(10\\.\\d{4,9}/[-._;()/:A-Z0-9]+)\\b")
	referenceArxivRe   = regexp.MustCompile("(?i)arxiv[:\\s]*([0-9]{4}\\.[0-9]{4,5})(v\\d+)?")
	referenceYearRe    = regexp.MustCompile("\\b(19|20)\\d{2}\\b")
	referenceURLRe     = regexp.MustCompile("https?://[^\\s,;]+")
	quotedTitleRe      = regexp.MustCompile("[" + string(rune(34)) + string(rune(8220)) + "](.+?)[" + string(rune(34)) + string(rune(8221)) + "]")
)

type referenceLine struct {
	page int
	text string
}

// ExtractReferences finds the references/bibliography section and returns
// structured entries. It is deliberately heuristic: PDF reference formatting
// varies, so callers must treat entries as candidates (the wiki only creates a
// cites Link when a DOI/arXiv/title match is unambiguous).
func ExtractReferences(document *Document) []ReferenceEntry {
	if document == nil || len(document.Pages) == 0 {
		return nil
	}
	lines := referenceSectionLines(document)
	if len(lines) == 0 {
		return nil
	}
	rawEntries := splitReferenceLines(lines)
	entries := make([]ReferenceEntry, 0, len(rawEntries))
	for _, raw := range rawEntries {
		entry := parseReferenceEntry(raw)
		if strings.TrimSpace(entry.Raw) == "" {
			continue
		}
		entry.Index = len(entries) + 1
		entries = append(entries, entry)
	}
	return entries
}

func referenceSectionLines(document *Document) []referenceLine {
	startPage := -1
	startLine := 0
	for _, page := range document.Pages {
		for index, line := range strings.Split(page.Text, "\n") {
			if isReferenceHeading(line) {
				startPage = page.Number
				startLine = index + 1
				break
			}
		}
		if startPage >= 0 {
			break
		}
	}
	if startPage < 0 {
		return nil
	}
	out := make([]referenceLine, 0, 64)
	for _, page := range document.Pages {
		if page.Number < startPage {
			continue
		}
		lines := strings.Split(page.Text, "\n")
		begin := 0
		if page.Number == startPage {
			begin = startLine
		}
		for index := begin; index < len(lines); index++ {
			text := strings.TrimSpace(lines[index])
			if text == "" {
				out = append(out, referenceLine{page: page.Number})
				continue
			}
			if len(out) > 0 && isReferenceStop(text) {
				return out
			}
			out = append(out, referenceLine{page: page.Number, text: text})
		}
	}
	return out
}

func isReferenceHeading(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || len([]rune(trimmed)) > 40 {
		return false
	}
	return referenceHeadingRe.MatchString(trimmed)
}

func isReferenceStop(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || len([]rune(trimmed)) > 60 {
		return false
	}
	return referenceStopRe.MatchString(trimmed)
}

func splitReferenceLines(lines []referenceLine) []referenceLine {
	numbered := false
	for _, line := range lines {
		if line.text != "" && referenceNumberRe.MatchString(line.text) {
			numbered = true
			break
		}
	}
	entries := make([]referenceLine, 0, 32)
	if numbered {
		current := make([]referenceLine, 0, 8)
		flush := func() {
			if len(current) == 0 {
				return
			}
			entries = append(entries, joinReferenceLines(current))
			current = current[:0]
		}
		for _, line := range lines {
			if line.text == "" {
				continue
			}
			if referenceNumberRe.MatchString(line.text) {
				flush()
				current = append(current, line)
				continue
			}
			if len(current) > 0 {
				current = append(current, line)
			}
		}
		flush()
		return entries
	}
	current := make([]referenceLine, 0, 8)
	flush := func() {
		if len(current) == 0 {
			return
		}
		entries = append(entries, joinReferenceLines(current))
		current = current[:0]
	}
	for _, line := range lines {
		if line.text == "" {
			flush()
			continue
		}
		current = append(current, line)
	}
	flush()
	if len(entries) == 1 && len([]rune(entries[0].text)) > 1200 {
		return splitLikelyReferenceStarts(lines)
	}
	return entries
}

func splitLikelyReferenceStarts(lines []referenceLine) []referenceLine {
	entries := make([]referenceLine, 0, 32)
	current := make([]referenceLine, 0, 8)
	flush := func() {
		if len(current) == 0 {
			return
		}
		entries = append(entries, joinReferenceLines(current))
		current = current[:0]
	}
	for _, line := range lines {
		if line.text == "" {
			continue
		}
		looksStart := len(current) > 0 && referenceYearRe.MatchString(line.text) && startsLikeAuthor(line.text)
		if looksStart {
			flush()
		}
		current = append(current, line)
	}
	flush()
	return entries
}

func startsLikeAuthor(text string) bool {
	for _, runeValue := range strings.TrimSpace(text) {
		if unicode.IsUpper(runeValue) || unicode.Is(unicode.Han, runeValue) {
			return true
		}
		if unicode.IsLetter(runeValue) || unicode.IsDigit(runeValue) {
			return false
		}
	}
	return false
}

func joinReferenceLines(lines []referenceLine) referenceLine {
	parts := make([]string, 0, len(lines))
	page := 0
	for _, line := range lines {
		if strings.TrimSpace(line.text) == "" {
			continue
		}
		if page == 0 {
			page = line.page
		}
		parts = append(parts, strings.TrimSpace(line.text))
	}
	return referenceLine{page: page, text: strings.Join(parts, " ")}
}

func parseReferenceEntry(raw referenceLine) ReferenceEntry {
	text := strings.TrimSpace(referenceNumberRe.ReplaceAllString(raw.text, ""))
	entry := ReferenceEntry{Raw: text, Page: raw.page}
	if text == "" {
		return ReferenceEntry{}
	}
	if match := referenceDOIRe.FindString(text); match != "" {
		entry.DOI = strings.TrimRight(match, ".,;)]}")
	}
	if match := referenceArxivRe.FindStringSubmatch(text); len(match) > 1 {
		entry.ArxivID = match[1]
	}
	if match := referenceURLRe.FindString(text); match != "" {
		entry.URL = strings.TrimRight(match, ".,;")
	}
	if match := referenceYearRe.FindString(text); match != "" {
		if year, err := strconv.Atoi(match); err == nil {
			entry.Year = year
		}
	}
	entry.Authors = parseReferenceAuthors(text, entry.Year)
	entry.Title = parseReferenceTitle(text)
	entry.Venue = parseReferenceVenue(text, entry.Title)
	return entry
}

func parseReferenceAuthors(text string, year int) []string {
	head := text
	if index := strings.Index(text, "("+strconv.Itoa(year)+")"); year > 0 && index > 0 {
		head = text[:index]
	} else if year > 0 {
		if index := strings.Index(text, strconv.Itoa(year)); index > 0 {
			head = text[:index]
		}
	}
	if index := strings.Index(head, ". "); index > 0 {
		head = head[:index]
	}
	head = strings.NewReplacer(" and ", ", ", " & ", ", ").Replace(head)
	parts := strings.Split(head, ",")
	authors := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(strings.Trim(part, ".;:"))
		if name == "" || strings.EqualFold(name, "et al") {
			continue
		}
		if len([]rune(name)) > 60 {
			continue
		}
		authors = append(authors, name)
		if len(authors) >= 10 {
			break
		}
	}
	return authors
}

func parseReferenceTitle(text string) string {
	if match := quotedTitleRe.FindStringSubmatch(text); len(match) > 1 {
		return strings.TrimSpace(match[1])
	}
	segments := strings.Split(text, ". ")
	if len(segments) == 1 {
		segments = strings.Split(text, ".")
	}
	for index, segment := range segments {
		if isYearSegment(segment) && index+1 < len(segments) {
			candidate := cleanTitleSegment(segments[index+1])
			if candidate != "" {
				return candidate
			}
		}
	}
	best := ""
	bestScore := 0
	for _, segment := range segments {
		candidate := cleanTitleSegment(segment)
		if candidate == "" {
			continue
		}
		score := len([]rune(candidate))
		if score > bestScore {
			best = candidate
			bestScore = score
		}
	}
	return best
}

func isYearSegment(segment string) bool {
	trimmed := strings.TrimSpace(strings.Trim(segment, "()[].,;:"))
	return len(trimmed) == 4 && referenceYearRe.MatchString(trimmed)
}

func cleanTitleSegment(segment string) string {
	candidate := strings.TrimSpace(strings.Trim(segment, " .,;:"))
	if len([]rune(candidate)) < 8 {
		return ""
	}
	lower := strings.ToLower(candidate)
	for _, marker := range []string{"doi", "arxiv", "http", "vol.", "pp.", "proceedings", "conference", "journal", "press", "university", "transactions", "neurips", "icml", "iclr", "acl", "naacl", "cvpr", "iccv", "eccv", "springer", "elsevier", "ieee"} {
		if strings.Contains(lower, marker) {
			return ""
		}
	}
	if referenceYearRe.MatchString(candidate) && len([]rune(candidate)) < 20 {
		return ""
	}
	return candidate
}

func parseReferenceVenue(text, title string) string {
	remaining := text
	if title != "" {
		remaining = strings.Replace(remaining, title, " ", 1)
	}
	remaining = referenceDOIRe.ReplaceAllString(remaining, " ")
	remaining = referenceArxivRe.ReplaceAllString(remaining, " ")
	remaining = referenceURLRe.ReplaceAllString(remaining, " ")
	segments := strings.Split(remaining, ". ")
	best := ""
	for _, segment := range segments {
		candidate := strings.TrimSpace(strings.Trim(segment, " .,;:"))
		lower := strings.ToLower(candidate)
		if len([]rune(candidate)) < 3 || len([]rune(candidate)) > 80 {
			continue
		}
		for _, marker := range []string{"conference", "proceedings", "journal", "transactions", "workshop", "symposium", "neurips", "icml", "iclr", "acl", "naacl", "cvpr", "iccv", "eccv", "springer", "elsevier", "ieee", "press"} {
			if strings.Contains(lower, marker) {
				best = candidate
				break
			}
		}
		if best != "" {
			break
		}
	}
	return best
}
