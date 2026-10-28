package documents

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	uploadspkg "research-assistant-backend/internal/uploads"
)

// Chunk is one retrievable unit of a document with page/section provenance.
// Kind distinguishes body text from structured tables and figure captions so
// callers can prioritize evidence types.
type Chunk struct {
	ID         string  `json:"id"`
	DocumentID string  `json:"document_id"`
	Page       int     `json:"page"`
	Section    string  `json:"section,omitempty"`
	Kind       string  `json:"kind,omitempty"`
	BlockID    string  `json:"block_id,omitempty"`
	TableID    string  `json:"table_id,omitempty"`
	FigureID   string  `json:"figure_id,omitempty"`
	Text       string  `json:"text"`
	Score      float64 `json:"score,omitempty"`
}

// Store provides owner-scoped access to persisted documents and retrieval.
type Store struct {
	root string
}

func NewStore(root string) *Store {
	if strings.TrimSpace(root) == "" {
		root = uploadspkg.Root()
	}
	return &Store{root: root}
}

func (s *Store) directory(ownerID, documentID string) string {
	if s != nil && strings.TrimSpace(s.root) != "" {
		return uploadspkg.Directory(ownerID, documentID)
	}
	return uploadspkg.Directory(ownerID, documentID)
}

func (s *Store) GetDocument(ctx context.Context, ownerID, documentID string) (*Document, error) {
	_ = ctx
	document, err := Load(s.directory(ownerID, documentID))
	if err != nil {
		return nil, fmt.Errorf("document %s is unavailable: %w", documentID, err)
	}
	return document, nil
}

func (s *Store) FullText(ctx context.Context, ownerID, documentID string) (string, error) {
	document, err := s.GetDocument(ctx, ownerID, documentID)
	if err != nil {
		return "", err
	}
	return FullText(document), nil
}

func (s *Store) ReadPage(ctx context.Context, ownerID, documentID string, page int) (string, error) {
	document, err := s.GetDocument(ctx, ownerID, documentID)
	if err != nil {
		return "", err
	}
	if page < 1 || page > len(document.Pages) {
		return "", fmt.Errorf("page %d is out of range", page)
	}
	return document.Pages[page-1].Text, nil
}

func (s *Store) ReadSection(ctx context.Context, ownerID, documentID, section string) (string, error) {
	document, err := s.GetDocument(ctx, ownerID, documentID)
	if err != nil {
		return "", err
	}
	needle := strings.ToLower(strings.TrimSpace(section))
	var builder strings.Builder
	for _, chunk := range buildChunks(document) {
		if needle != "" && strings.Contains(strings.ToLower(chunk.Section), needle) {
			if builder.Len() > 0 {
				builder.WriteString("\n\n")
			}
			builder.WriteString(chunk.Text)
		}
	}
	if builder.Len() == 0 {
		return "", fmt.Errorf("section %q was not found", section)
	}
	return builder.String(), nil
}

// Search returns the best BM25 chunks for query. It is kept for existing
// callers and delegates to SearchWithOptions.
func (s *Store) Search(ctx context.Context, ownerID, documentID, query string, topK int) ([]Chunk, error) {
	return s.SearchWithOptions(ctx, ownerID, documentID, query, SearchOptions{TopK: topK})
}

// SearchWithOptions returns the best BM25 chunks for query after applying the
// page/section filters in opts.
func (s *Store) SearchWithOptions(ctx context.Context, ownerID, documentID, query string, opts SearchOptions) ([]Chunk, error) {
	document, err := s.GetDocument(ctx, ownerID, documentID)
	if err != nil {
		return nil, err
	}
	return RankDocument(document, query, opts), nil
}

func buildChunks(document *Document) []Chunk {
	if document == nil {
		return nil
	}
	chunks := make([]Chunk, 0, len(document.Pages)*2+len(document.Tables)+len(document.Figures))
	section := ""
	for _, page := range document.Pages {
		lines := strings.Split(page.Text, "\n")
		var builder strings.Builder
		flush := func() {
			text := strings.TrimSpace(builder.String())
			if text != "" {
				chunks = append(chunks, Chunk{
					ID:         fmt.Sprintf("%s-p%d-%d", document.UploadID, page.Number, len(chunks)+1),
					DocumentID: document.UploadID,
					Page:       page.Number,
					Section:    section,
					Kind:       "text",
					Text:       text,
				})
			}
			builder.Reset()
		}
		for _, line := range lines {
			clean := strings.TrimSpace(line)
			if clean == "" {
				continue
			}
			if looksLikeSectionHeading(clean) {
				section = clean
			}
			if builder.Len() > 0 && builder.Len()+len(clean) > 1000 {
				flush()
			}
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(clean)
		}
		flush()
	}

	for index, table := range document.Tables {
		var builder strings.Builder
		fmt.Fprintf(&builder, "Table %d", index+1)
		if strings.TrimSpace(table.Caption) != "" {
			fmt.Fprintf(&builder, ": %s", strings.TrimSpace(table.Caption))
		}
		for _, row := range table.Rows {
			builder.WriteString("\n")
			builder.WriteString(strings.Join(row, " | "))
		}
		chunks = append(chunks, Chunk{
			ID:         fmt.Sprintf("%s-%s", document.UploadID, table.ID),
			DocumentID: document.UploadID,
			Page:       table.Page,
			Section:    section,
			Kind:       "table",
			TableID:    table.ID,
			Text:       strings.TrimSpace(builder.String()),
		})
	}
	for index, figure := range document.Figures {
		caption := strings.TrimSpace(figure.Caption)
		if caption == "" {
			caption = fmt.Sprintf("Figure %d", index+1)
		}
		chunks = append(chunks, Chunk{
			ID:         fmt.Sprintf("%s-%s", document.UploadID, figure.ID),
			DocumentID: document.UploadID,
			Page:       figure.Page,
			Section:    section,
			Kind:       "figure",
			FigureID:   figure.ID,
			Text:       caption,
		})
	}
	return chunks
}

func looksLikeSectionHeading(line string) bool {
	if len([]rune(line)) > 100 {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	first := []rune(trimmed)[0]
	if unicode.IsDigit(first) {
		return strings.Contains(trimmed, ".") || strings.Contains(trimmed, " ")
	}
	upper := strings.ToUpper(trimmed)
	return upper == trimmed && len([]rune(trimmed)) >= 4 && !strings.Contains(trimmed, ".")
}

func queryTokens(query string) []string {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil
	}
	tokens := make([]string, 0, 16)
	seen := map[string]struct{}{}
	add := func(token string) {
		token = strings.TrimSpace(token)
		if token == "" || len([]rune(token)) < 2 {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	for _, word := range strings.Fields(query) {
		add(word)
	}
	// Chinese bigrams: BM25 without a tokenizer would miss Chinese queries.
	runes := []rune(query)
	for i := 0; i+1 < len(runes); i++ {
		if unicode.Is(unicode.Han, runes[i]) && unicode.Is(unicode.Han, runes[i+1]) {
			add(string(runes[i : i+2]))
		}
	}
	return tokens
}
