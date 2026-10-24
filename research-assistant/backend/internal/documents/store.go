package documents

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	uploadspkg "research-assistant-backend/internal/uploads"
)

// Chunk is one retrievable unit of a document with page/section provenance.
type Chunk struct {
	ID         string  `json:"id"`
	DocumentID string  `json:"document_id"`
	Page       int     `json:"page"`
	Section    string  `json:"section,omitempty"`
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

func (s *Store) Search(ctx context.Context, ownerID, documentID, query string, topK int) ([]Chunk, error) {
	document, err := s.GetDocument(ctx, ownerID, documentID)
	if err != nil {
		return nil, err
	}
	if topK <= 0 {
		topK = 8
	}
	if topK > 20 {
		topK = 20
	}
	chunks := buildChunks(document)
	tokens := queryTokens(query)
	if len(tokens) == 0 {
		if len(chunks) > topK {
			chunks = chunks[:topK]
		}
		return chunks, nil
	}
	for i := range chunks {
		chunks[i].Score = scoreChunk(chunks[i].Text, tokens)
	}
	sort.SliceStable(chunks, func(i, j int) bool { return chunks[i].Score > chunks[j].Score })
	filtered := make([]Chunk, 0, topK)
	for _, chunk := range chunks {
		if chunk.Score <= 0 {
			continue
		}
		filtered = append(filtered, chunk)
		if len(filtered) >= topK {
			break
		}
	}
	if len(filtered) == 0 {
		if len(chunks) > topK {
			chunks = chunks[:topK]
		}
		return chunks, nil
	}
	return filtered, nil
}

func buildChunks(document *Document) []Chunk {
	if document == nil {
		return nil
	}
	chunks := make([]Chunk, 0, len(document.Pages)*2)
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

func scoreChunk(text string, tokens []string) float64 {
	lower := strings.ToLower(text)
	score := 0.0
	for _, token := range tokens {
		count := strings.Count(lower, token)
		if count > 0 {
			score += float64(count) * (1.0 + math.Log1p(float64(len([]rune(token)))))
		}
	}
	if score == 0 {
		return 0
	}
	return score / (1.0 + float64(len([]rune(text)))/1200.0)
}
