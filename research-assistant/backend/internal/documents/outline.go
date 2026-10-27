package documents

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// SectionInfo describes one heading-delimited section of a document.
// StartPage and EndPage are inclusive 1-based page numbers.
type SectionInfo struct {
	Title      string `json:"title"`
	StartPage  int    `json:"start_page"`
	EndPage    int    `json:"end_page"`
	Chunks     int    `json:"chunks"`
	Characters int    `json:"characters"`
}

// OutlineSection is an alias used by callers that prefer the document
// vocabulary ("outline section") over the retrieval vocabulary.
type OutlineSection = SectionInfo

// Outline returns the heading-delimited sections of document in page order.
// Documents without recognizable headings return an empty list; callers
// should then fall back to document-wide retrieval.
func Outline(document *Document) []SectionInfo {
	if document == nil {
		return nil
	}
	chunks := buildChunks(document)
	if len(chunks) == 0 {
		return nil
	}

	type accumulator struct {
		title      string
		startPage  int
		endPage    int
		chunks     int
		characters int
	}
	order := make([]string, 0, 8)
	byTitle := make(map[string]*accumulator, 8)
	for _, chunk := range chunks {
		title := strings.TrimSpace(chunk.Section)
		if title == "" {
			continue
		}
		key := strings.ToLower(title)
		section, ok := byTitle[key]
		if !ok {
			section = &accumulator{title: title, startPage: chunk.Page, endPage: chunk.Page}
			byTitle[key] = section
			order = append(order, key)
		}
		if chunk.Page < section.startPage {
			section.startPage = chunk.Page
		}
		if chunk.Page > section.endPage {
			section.endPage = chunk.Page
		}
		section.chunks++
		section.characters += len([]rune(chunk.Text))
	}

	sections := make([]SectionInfo, 0, len(order))
	for _, key := range order {
		section := byTitle[key]
		sections = append(sections, SectionInfo{
			Title:      section.title,
			StartPage:  section.startPage,
			EndPage:    section.endPage,
			Chunks:     section.chunks,
			Characters: section.characters,
		})
	}
	sort.SliceStable(sections, func(i, j int) bool {
		if sections[i].StartPage != sections[j].StartPage {
			return sections[i].StartPage < sections[j].StartPage
		}
		return sections[i].Title < sections[j].Title
	})
	return sections
}

// Outline returns the section list for one stored document.
func (s *Store) Outline(ctx context.Context, ownerID, documentID string) ([]SectionInfo, error) {
	if s == nil {
		return nil, fmt.Errorf("document store is nil")
	}
	document, err := s.GetDocument(ctx, ownerID, documentID)
	if err != nil {
		return nil, err
	}
	return Outline(document), nil
}
