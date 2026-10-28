package documents

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type tsvWord struct {
	block  int
	par    int
	line   int
	word   int
	left   int
	top    int
	width  int
	height int
	conf   float64
	text   string
}

// parseTesseractTSV converts tesseract's TSV output into page text, mean word
// confidence (0..1) and block geometry (PDF points). It is intentionally
// tolerant: malformed rows are skipped rather than failing the whole page.
func parseTesseractTSV(raw string, dpi int) (string, float64, []Block) {
	if dpi <= 0 {
		dpi = ocrRenderDPI
	}
	scale := 72.0 / float64(dpi)
	words := make([]tsvWord, 0, 256)
	for index, line := range strings.Split(raw, "\n") {
		if index == 0 {
			continue
		}
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(line, "\t", 12)
		if len(fields) < 12 {
			continue
		}
		level, err := strconv.Atoi(strings.TrimSpace(fields[0]))
		if err != nil || level != 5 {
			continue
		}
		text := strings.TrimSpace(fields[11])
		if text == "" {
			continue
		}
		conf, err := strconv.ParseFloat(strings.TrimSpace(fields[10]), 64)
		if err != nil || conf < 0 {
			continue
		}
		block, _ := strconv.Atoi(strings.TrimSpace(fields[2]))
		par, _ := strconv.Atoi(strings.TrimSpace(fields[3]))
		lineNumber, _ := strconv.Atoi(strings.TrimSpace(fields[4]))
		wordNumber, _ := strconv.Atoi(strings.TrimSpace(fields[5]))
		left, _ := strconv.Atoi(strings.TrimSpace(fields[6]))
		top, _ := strconv.Atoi(strings.TrimSpace(fields[7]))
		width, _ := strconv.Atoi(strings.TrimSpace(fields[8]))
		height, _ := strconv.Atoi(strings.TrimSpace(fields[9]))
		words = append(words, tsvWord{
			block: block, par: par, line: lineNumber, word: wordNumber,
			left: left, top: top, width: width, height: height,
			conf: conf, text: text,
		})
	}
	if len(words) == 0 {
		return "", 0, nil
	}
	sort.SliceStable(words, func(i, j int) bool {
		if words[i].block != words[j].block {
			return words[i].block < words[j].block
		}
		if words[i].par != words[j].par {
			return words[i].par < words[j].par
		}
		if words[i].line != words[j].line {
			return words[i].line < words[j].line
		}
		return words[i].word < words[j].word
	})

	lineKeys := make([]string, 0, len(words))
	lineWords := make(map[string][]tsvWord, len(words))
	for _, word := range words {
		key := fmt.Sprintf("%d/%d/%d", word.block, word.par, word.line)
		if _, ok := lineWords[key]; !ok {
			lineKeys = append(lineKeys, key)
		}
		lineWords[key] = append(lineWords[key], word)
	}
	// lineKeys already follows the sorted word order; keep it stable.
	textBuilder := strings.Builder{}
	for _, key := range lineKeys {
		parts := lineWords[key]
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			texts = append(texts, part.text)
		}
		if textBuilder.Len() > 0 {
			textBuilder.WriteString("\n")
		}
		textBuilder.WriteString(joinLayoutWords(texts))
	}

	// Group words into tesseract blocks for bbox-aware retrieval.
	blockOrder := make([]int, 0, 8)
	byBlock := make(map[int][]tsvWord, 8)
	confidenceSum := 0.0
	for _, word := range words {
		if _, ok := byBlock[word.block]; !ok {
			blockOrder = append(blockOrder, word.block)
		}
		byBlock[word.block] = append(byBlock[word.block], word)
		confidenceSum += word.conf
	}
	blocks := make([]Block, 0, len(blockOrder))
	for _, blockNumber := range blockOrder {
		parts := byBlock[blockNumber]
		box := [4]float64{1e9, 1e9, -1e9, -1e9}
		blockConfidence := 0.0
		lineSet := make([]string, 0, len(parts))
		seenLines := make(map[string]bool, len(parts))
		lineTexts := make(map[string][]string, len(parts))
		lineOrder := make([]string, 0, len(parts))
		for _, part := range parts {
			key := fmt.Sprintf("%d/%d", part.par, part.line)
			if !seenLines[key] {
				seenLines[key] = true
				lineOrder = append(lineOrder, key)
			}
			lineTexts[key] = append(lineTexts[key], part.text)
			left := float64(part.left) * scale
			top := float64(part.top) * scale
			right := float64(part.left+part.width) * scale
			bottom := float64(part.top+part.height) * scale
			if left < box[0] {
				box[0] = left
			}
			if top < box[1] {
				box[1] = top
			}
			if right > box[2] {
				box[2] = right
			}
			if bottom > box[3] {
				box[3] = bottom
			}
			blockConfidence += part.conf
		}
		for _, key := range lineOrder {
			lineSet = append(lineSet, joinLayoutWords(lineTexts[key]))
		}
		blocks = append(blocks, Block{
			Type:       BlockParagraph,
			BBox:       box,
			Text:       strings.Join(lineSet, "\n"),
			Confidence: blockConfidence / float64(len(parts)) / 100.0,
		})
	}
	mean := confidenceSum / float64(len(words)) / 100.0
	return strings.TrimSpace(textBuilder.String()), mean, blocks
}
