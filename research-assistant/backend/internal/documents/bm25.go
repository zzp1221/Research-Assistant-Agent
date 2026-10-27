package documents

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

// BM25 parameters shared by every ranking path. They match the classic
// Okapi defaults (k1=1.2, b=0.75) used by the retrieval adapters in this
// repository.
const (
	BM25K1 = 1.2
	BM25B  = 0.75

	defaultSearchTopK = 8
	maxSearchTopK     = 20
)

// SearchOptions narrows and tunes BM25 retrieval over one document.
type SearchOptions struct {
	// TopK caps the number of returned chunks. Values <= 0 default to 8 and
	// values above 20 are clamped to 20, matching Store.Search.
	TopK int
	// Section restricts results to chunks whose section heading contains
	// Section, case-insensitively. Empty means every section.
	Section string
	// Page restricts results to chunks on this 1-based page. Zero means every
	// page.
	Page int
	// SectionBoost multiplies the BM25 score of chunks inside Section. Values
	// <= 0 keep the default 1.0. It has no effect when Section is empty.
	SectionBoost float64
}

// RankDocument scores every chunk of document against query with BM25 and
// returns at most TopK matching chunks, best first.
//
// Statistics are collected over the whole document corpus (N chunks,
// average chunk length and per-term document frequency) before SearchOptions
// filters are applied. That keeps scores comparable across calls that restrict
// retrieval to different sections, which is what the Librarian agent relies on
// when it merges per-section results.
//
// Query terms come from queryTokens, so English words and Chinese bigrams are
// supported without an external segmenter. A term is counted whenever it
// appears as a normalized substring of a chunk, and chunk length is measured
// in runes; this keeps recall for unstemmed derivatives ("experiment" matches
// "experiments") while still applying BM25 saturation (k1) and length
// normalization (b).
func RankDocument(document *Document, query string, opts SearchOptions) []Chunk {
	chunks := buildChunks(document)
	if len(chunks) == 0 {
		return []Chunk{}
	}

	topK := opts.TopK
	if topK <= 0 {
		topK = defaultSearchTopK
	}
	if topK > maxSearchTopK {
		topK = maxSearchTopK
	}
	section := strings.ToLower(strings.TrimSpace(opts.Section))
	boost := opts.SectionBoost
	if boost <= 0 {
		boost = 1
	}

	tokens := queryTokens(query)
	if len(tokens) == 0 {
		return firstMatchingChunks(chunks, section, opts.Page, topK)
	}

	// Corpus statistics: document frequency is per chunk, and the average
	// length is measured in runes over the same chunks.
	lowerTexts := make([]string, len(chunks))
	lengths := make([]int, len(chunks))
	totalLength := 0
	for index := range chunks {
		lowerTexts[index] = strings.ToLower(chunks[index].Text)
		lengths[index] = utf8.RuneCountInString(lowerTexts[index])
		totalLength += lengths[index]
	}
	averageLength := float64(totalLength) / float64(len(chunks))
	if averageLength < 1 {
		averageLength = 1
	}

	documentFrequency := make(map[string]int, len(tokens))
	for _, token := range tokens {
		for index := range lowerTexts {
			if strings.Contains(lowerTexts[index], token) {
				documentFrequency[token]++
			}
		}
	}
	corpusSize := float64(len(chunks))
	inverseDocumentFrequency := make(map[string]float64, len(tokens))
	for _, token := range tokens {
		df := float64(documentFrequency[token])
		inverseDocumentFrequency[token] = math.Log(1 + (corpusSize-df+0.5)/(df+0.5))
	}

	type scoredChunk struct {
		chunk Chunk
		order int
	}
	candidates := make([]scoredChunk, 0, len(chunks))
	for index := range chunks {
		chunk := chunks[index]
		if !sectionMatches(chunk.Section, section) {
			continue
		}
		if opts.Page > 0 && chunk.Page != opts.Page {
			continue
		}
		score := 0.0
		lengthNorm := 1 - BM25B + BM25B*float64(lengths[index])/averageLength
		for _, token := range tokens {
			termFrequency := float64(strings.Count(lowerTexts[index], token))
			if termFrequency == 0 {
				continue
			}
			score += inverseDocumentFrequency[token] *
				(termFrequency * (BM25K1 + 1)) /
				(termFrequency + BM25K1*lengthNorm)
		}
		if score <= 0 {
			continue
		}
		if section != "" {
			score *= boost
		}
		chunk.Score = score
		candidates = append(candidates, scoredChunk{chunk: chunk, order: index})
	}

	// Deterministic ordering: score first, then page, then the chunk's
	// position inside the document.
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].chunk.Score != candidates[j].chunk.Score {
			return candidates[i].chunk.Score > candidates[j].chunk.Score
		}
		if candidates[i].chunk.Page != candidates[j].chunk.Page {
			return candidates[i].chunk.Page < candidates[j].chunk.Page
		}
		return candidates[i].order < candidates[j].order
	})

	if len(candidates) > topK {
		candidates = candidates[:topK]
	}
	results := make([]Chunk, len(candidates))
	for index := range candidates {
		results[index] = candidates[index].chunk
	}
	return results
}

// firstMatchingChunks returns unfiltered chunks in document order. It is used
// for empty queries, where no BM25 signal exists.
func firstMatchingChunks(chunks []Chunk, section string, page, topK int) []Chunk {
	results := make([]Chunk, 0, topK)
	for _, chunk := range chunks {
		if !sectionMatches(chunk.Section, section) {
			continue
		}
		if page > 0 && chunk.Page != page {
			continue
		}
		results = append(results, chunk)
		if len(results) >= topK {
			break
		}
	}
	return results
}

func sectionMatches(chunkSection, needle string) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(chunkSection), needle)
}
