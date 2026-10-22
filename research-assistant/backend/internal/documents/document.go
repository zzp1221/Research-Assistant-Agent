// Package documents turns uploaded PDFs into page-addressable text that the
// API and agents can reason about without re-parsing the file for every call.
package documents

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const Version = "document.text/v1"

// Status describes whether a usable text layer could be extracted.
type Status string

const (
	StatusExtracted   Status = "extracted"
	StatusEmpty       Status = "empty"
	StatusEncrypted   Status = "encrypted"
	StatusFailed      Status = "failed"
	StatusUnsupported Status = "unsupported"
)

const (
	// MaxExcerptChars is how much paper text is handed to a planner prompt.
	MaxExcerptChars = 24000
	// MaxPageChars caps one page when building assist context.
	MaxPageChars = 6000
	fileName     = "document.json"
)

// Page is one extracted PDF page. Page numbers are 1-based.
type Page struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
	Chars  int    `json:"chars"`
}

// Document is the persisted text representation of one uploaded PDF.
type Document struct {
	Version     string    `json:"version"`
	UploadID    string    `json:"upload_id"`
	Status      Status    `json:"status"`
	Extractor   string    `json:"extractor,omitempty"`
	PageCount   int       `json:"page_count"`
	Characters  int       `json:"characters"`
	Pages       []Page    `json:"pages"`
	Error       string    `json:"error,omitempty"`
	ExtractedAt time.Time `json:"extracted_at"`
}

// HasText reports whether the document can be used for assist or extraction.
func (d *Document) HasText() bool {
	return d != nil && d.Status == StatusExtracted && d.PageCount > 0
}

// Path returns the metadata file location inside one upload directory.
func Path(directory string) string {
	return filepath.Join(directory, fileName)
}

// Save writes the document atomically with owner-only permissions.
func Save(directory string, document *Document) error {
	if document == nil {
		return fmt.Errorf("document is nil")
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode document: %w", err)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create document directory: %w", err)
	}
	target := Path(directory)
	temp := target + ".tmp"
	if err := os.WriteFile(temp, raw, 0o600); err != nil {
		return fmt.Errorf("write document: %w", err)
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("publish document: %w", err)
	}
	return nil
}

// Load reads a previously extracted document.
func Load(directory string) (*Document, error) {
	raw, err := os.ReadFile(Path(directory))
	if err != nil {
		return nil, err
	}
	var document Document
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode document: %w", err)
	}
	if document.Pages == nil {
		document.Pages = []Page{}
	}
	return &document, nil
}

// SplitPages converts raw pdftotext output into clean pages. pdftotext uses a
// form feed as the page separator.
func SplitPages(raw string) []Page {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	parts := strings.Split(raw, "\f")
	pages := make([]Page, 0, len(parts))
	for index, part := range parts {
		text := CleanText(part)
		if index == len(parts)-1 && text == "" {
			continue
		}
		if text == "" && index == len(parts)-1 {
			continue
		}
		pages = append(pages, Page{Number: index + 1, Text: text, Chars: utf8.RuneCountInString(text)})
	}
	// Drop trailing empty pages produced by a final form feed.
	for len(pages) > 0 && pages[len(pages)-1].Text == "" && len(pages) > 1 {
		pages = pages[:len(pages)-1]
	}
	return pages
}

// CleanText removes NUL bytes and normalizes whitespace while keeping the
// layout pdftotext produced.
func CleanText(text string) string {
	text = strings.ReplaceAll(text, "\x00", "")
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" {
			blank++
			if blank > 1 {
				continue
			}
			cleaned = append(cleaned, "")
			continue
		}
		blank = 0
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

// FullText joins every page, preserving page boundaries for prompts.
func FullText(document *Document) string {
	if document == nil {
		return ""
	}
	builder := strings.Builder{}
	for _, page := range document.Pages {
		if page.Text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		fmt.Fprintf(&builder, "[Page %d]\n%s", page.Number, page.Text)
	}
	return builder.String()
}

// Excerpt truncates the full text for planner input.
func Excerpt(document *Document) string {
	text := FullText(document)
	if utf8.RuneCountInString(text) <= MaxExcerptChars {
		return text
	}
	runes := []rune(text)
	return string(runes[:MaxExcerptChars]) + "\n...[truncated]"
}

// ContextForPage returns neighboring page text within a character budget.
func ContextForPage(document *Document, page int, radius int) string {
	if document == nil || page < 1 || page > len(document.Pages) {
		return ""
	}
	if radius < 0 {
		radius = 0
	}
	start := page - 1 - radius
	end := page - 1 + radius
	if start < 0 {
		start = 0
	}
	if end >= len(document.Pages) {
		end = len(document.Pages) - 1
	}
	builder := strings.Builder{}
	for index := start; index <= end; index++ {
		text := document.Pages[index].Text
		if utf8.RuneCountInString(text) > MaxPageChars {
			text = string([]rune(text)[:MaxPageChars]) + "\n...[truncated]"
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		fmt.Fprintf(&builder, "[Page %d]\n%s", document.Pages[index].Number, text)
	}
	return builder.String()
}
