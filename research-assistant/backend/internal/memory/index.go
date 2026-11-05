package memory

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Index is an in-memory inverted index with BM25 scoring and entity/kind/status
// posting lists. It is rebuilt from the store on startup and updated
// incrementally on writes.
type Index struct {
	inverted map[string]map[string]int
	terms    map[string][]string
	docLen   map[string]int
	byEntity map[string]map[string]struct{}
	byKind   map[string]map[string]struct{}
	byStatus map[string]map[string]struct{}
	totalLen int
}

// BM25Hit is one ranked document.
type BM25Hit struct {
	ID    string
	Score float64
}

func NewIndex() *Index {
	return &Index{
		inverted: map[string]map[string]int{},
		terms:    map[string][]string{},
		docLen:   map[string]int{},
		byEntity: map[string]map[string]struct{}{},
		byKind:   map[string]map[string]struct{}{},
		byStatus: map[string]map[string]struct{}{},
	}
}

// Tokenize splits mixed Chinese/English text into BM25 terms. CJK text is
// indexed as bigrams (plus a unigram for single-character runs) because there
// is no whitespace tokenizer for Chinese.
func Tokenize(text string) []string {
	var out []string
	var latin strings.Builder
	var cjk []rune
	flushLatin := func() {
		if latin.Len() == 0 {
			return
		}
		word := strings.ToLower(latin.String())
		latin.Reset()
		if len(word) > 64 {
			word = word[:64]
		}
		out = append(out, word)
	}
	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		if len(cjk) == 1 {
			out = append(out, string(cjk[0]))
		} else {
			for i := 0; i+1 < len(cjk); i++ {
				out = append(out, string(cjk[i:i+2]))
			}
		}
		cjk = cjk[:0]
	}
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			flushLatin()
			cjk = append(cjk, r)
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-':
			flushCJK()
			latin.WriteRune(unicode.ToLower(r))
		default:
			flushLatin()
			flushCJK()
		}
	}
	flushLatin()
	flushCJK()
	return out
}

func entityKey(entity Entity) string {
	return strings.ToLower(strings.TrimSpace(entity.Type)) + ":" + strings.ToLower(strings.TrimSpace(entity.Value))
}

func (i *Index) Add(rec Record) {
	i.Remove(rec)
	parts := []string{rec.Title, rec.Content, strings.Join(rec.Tags, " ")}
	for _, entity := range rec.Entities {
		parts = append(parts, entity.Value)
	}
	tokens := Tokenize(strings.Join(parts, " "))
	if len(tokens) == 0 {
		return
	}
	counts := map[string]int{}
	for _, token := range tokens {
		counts[token]++
	}
	i.terms[rec.ID] = tokens
	i.docLen[rec.ID] = len(tokens)
	i.totalLen += len(tokens)
	for token, count := range counts {
		posting := i.inverted[token]
		if posting == nil {
			posting = map[string]int{}
			i.inverted[token] = posting
		}
		posting[rec.ID] = count
	}
	for _, entity := range rec.Entities {
		key := entityKey(entity)
		if key == ":" {
			continue
		}
		set := i.byEntity[key]
		if set == nil {
			set = map[string]struct{}{}
			i.byEntity[key] = set
		}
		set[rec.ID] = struct{}{}
	}
	kindKey := string(rec.Kind)
	if i.byKind[kindKey] == nil {
		i.byKind[kindKey] = map[string]struct{}{}
	}
	i.byKind[kindKey][rec.ID] = struct{}{}
	statusKey := string(rec.Status)
	if i.byStatus[statusKey] == nil {
		i.byStatus[statusKey] = map[string]struct{}{}
	}
	i.byStatus[statusKey][rec.ID] = struct{}{}
}

func (i *Index) Remove(rec Record) {
	if tokens, ok := i.terms[rec.ID]; ok {
		for _, token := range tokens {
			if posting, exists := i.inverted[token]; exists {
				delete(posting, rec.ID)
				if len(posting) == 0 {
					delete(i.inverted, token)
				}
			}
		}
		delete(i.terms, rec.ID)
	}
	if length, ok := i.docLen[rec.ID]; ok {
		i.totalLen -= length
		delete(i.docLen, rec.ID)
	}
	for _, entity := range rec.Entities {
		key := entityKey(entity)
		if set := i.byEntity[key]; set != nil {
			delete(set, rec.ID)
			if len(set) == 0 {
				delete(i.byEntity, key)
			}
		}
	}
	if set := i.byKind[string(rec.Kind)]; set != nil {
		delete(set, rec.ID)
	}
	if set := i.byStatus[string(rec.Status)]; set != nil {
		delete(set, rec.ID)
	}
}

// SearchBM25 returns the top limit documents by BM25 score.
func (i *Index) SearchBM25(query string, limit int) []BM25Hit {
	return i.SearchBM25In(query, limit, nil)
}

// SearchBM25In ranks within the visible corpus, including its document-frequency
// statistics. Unrelated owners/projects cannot consume the candidate limit or
// change the ranking of a caller's own records. A nil set means the full index.
func (i *Index) SearchBM25In(query string, limit int, allowed map[string]struct{}) []BM25Hit {
	tokens := Tokenize(query)
	if len(tokens) == 0 || len(i.docLen) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = 50
	}
	unique := map[string]struct{}{}
	for _, token := range tokens {
		unique[token] = struct{}{}
	}
	count, total := len(i.docLen), i.totalLen
	if allowed != nil {
		count, total = 0, 0
		for id := range allowed {
			if length, ok := i.docLen[id]; ok {
				count++
				total += length
			}
		}
	}
	if count == 0 {
		return nil
	}
	n := float64(count)
	avg := 1.0
	if count > 0 {
		avg = float64(total) / float64(count)
		if avg <= 0 {
			avg = 1
		}
	}
	const k1 = 1.2
	const b = 0.75
	scores := map[string]float64{}
	for token := range unique {
		posting := i.inverted[token]
		if len(posting) == 0 {
			continue
		}
		df := float64(len(posting))
		if allowed != nil {
			df = 0
			for id := range posting {
				if _, ok := allowed[id]; ok {
					df++
				}
			}
		}
		idf := math.Log(1 + (n-df+0.5)/(df+0.5))
		for id, tf := range posting {
			if allowed != nil {
				if _, ok := allowed[id]; !ok {
					continue
				}
			}
			length := float64(i.docLen[id])
			score := idf * (float64(tf) * (k1 + 1)) / (float64(tf) + k1*(1-b+b*length/avg))
			scores[id] += score
		}
	}
	hits := make([]BM25Hit, 0, len(scores))
	for id, score := range scores {
		hits = append(hits, BM25Hit{ID: id, Score: score})
	}
	sort.Slice(hits, func(a, b int) bool {
		if hits[a].Score == hits[b].Score {
			return hits[a].ID < hits[b].ID
		}
		return hits[a].Score > hits[b].Score
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// EntityIDs returns the ids tagged with any of the query entities.
func (i *Index) EntityIDs(entities []Entity) []string {
	seen := map[string]struct{}{}
	for _, entity := range entities {
		key := entityKey(entity)
		for id := range i.byEntity[key] {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// KindIDs returns ids of one kind.
func (i *Index) KindIDs(kind Kind) []string {
	out := make([]string, 0)
	for id := range i.byKind[string(kind)] {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
