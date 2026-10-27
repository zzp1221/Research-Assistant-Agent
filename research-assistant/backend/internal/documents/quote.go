package documents

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// LocateQuote finds quote inside document and returns its 1-based page plus
// byte offsets into that page's Text. ok is false when the quote does not
// appear in any page.
//
// Matching tolerates the artifacts PDF extraction introduces:
//   - case is ignored;
//   - runs of whitespace (including newlines and non-breaking spaces) collapse
//     to a single space;
//   - line-break hyphenation is ignored, so "informa-\ntion" matches
//     "information"; a genuine compound hyphen ("large-scale") still matches a
//     line-broken occurrence ("large-\nscale").
//
// The returned start/end offsets address the original, unnormalized page text,
// so callers can highlight the exact span they were given.
func LocateQuote(document *Document, quote string) (page int, start int, end int, ok bool) {
	if document == nil || len(document.Pages) == 0 {
		return 0, 0, 0, false
	}
	normalizedQuote := normalizeMatchText(strings.TrimSpace(quote))
	if normalizedQuote.text == "" {
		return 0, 0, 0, false
	}
	needles := []string{normalizedQuote.text}
	if withoutHyphens := strings.ReplaceAll(normalizedQuote.text, "-", ""); withoutHyphens != normalizedQuote.text && withoutHyphens != "" {
		needles = append(needles, withoutHyphens)
	}

	for _, candidate := range document.Pages {
		normalizedPage := normalizeMatchText(candidate.Text)
		for _, needle := range needles {
			index := strings.Index(normalizedPage.text, needle)
			if index < 0 {
				continue
			}
			last := index + len(needle) - 1
			return candidate.Number, normalizedPage.starts[index], normalizedPage.ends[last], true
		}
	}
	return 0, 0, 0, false
}

// normalizedMatchText is text plus a byte-offset mapping back into the
// original string. starts[i] is the original byte offset where the rune that
// produced normalized byte i began; ends[i] is the offset just after it.
type normalizedMatchText struct {
	text   string
	starts []int
	ends   []int
}

type matchRune struct {
	r     rune
	start int
	end   int
}

// normalizeMatchText lowercases text, collapses whitespace and removes
// hyphenation across a line break ("informa-\ntion" becomes "information").
// Genuine hyphens inside a line are preserved.
func normalizeMatchText(text string) normalizedMatchText {
	spans := make([]matchRune, 0, utf8.RuneCountInString(text))
	for index, r := range text {
		if len(spans) > 0 {
			spans[len(spans)-1].end = index
		}
		spans = append(spans, matchRune{r: r, start: index})
	}
	if len(spans) > 0 {
		spans[len(spans)-1].end = len(text)
	}

	buffer := make([]byte, 0, len(text))
	starts := make([]int, 0, len(text))
	ends := make([]int, 0, len(text))
	appendBytes := func(value string, span matchRune) {
		for offset := 0; offset < len(value); offset++ {
			buffer = append(buffer, value[offset])
			starts = append(starts, span.start)
			ends = append(ends, span.end)
		}
	}

	for index := 0; index < len(spans); index++ {
		span := spans[index]
		switch {
		case span.r == '\u00ad':
			// Soft hyphens are invisible formatting characters.
			continue
		case span.r == '-' && index+1 < len(spans) && (spans[index+1].r == '\n' || spans[index+1].r == '\r'):
			// Drop the hyphen and the line break: this is a hyphenation
			// artifact, not a real dash.
			index++
			if spans[index].r == '\r' && index+1 < len(spans) && spans[index+1].r == '\n' {
				index++
			}
		case unicode.IsSpace(span.r):
			if len(buffer) == 0 || buffer[len(buffer)-1] == ' ' {
				continue
			}
			buffer = append(buffer, ' ')
			starts = append(starts, span.start)
			ends = append(ends, span.end)
		default:
			appendBytes(string(unicode.ToLower(span.r)), span)
		}
	}

	for len(buffer) > 0 && buffer[len(buffer)-1] == ' ' {
		buffer = buffer[:len(buffer)-1]
		starts = starts[:len(starts)-1]
		ends = ends[:len(ends)-1]
	}
	return normalizedMatchText{text: string(buffer), starts: starts, ends: ends}
}
