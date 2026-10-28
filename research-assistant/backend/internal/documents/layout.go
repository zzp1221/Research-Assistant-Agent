package documents

import (
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// LayoutPage is one page parsed from pdftotext -bbox-layout output.
type LayoutPage struct {
	Number int
	Width  float64
	Height float64
	Blocks []LayoutBlock
}

// LayoutBlock is one block with its bounding box in PDF points.
type LayoutBlock struct {
	BBox  [4]float64
	Lines []string
}

// Text returns the block text with lines joined by newlines.
func (b LayoutBlock) Text() string {
	return strings.TrimSpace(strings.Join(b.Lines, "\n"))
}

// parseBBoxLayout parses the XHTML produced by pdftotext -bbox-layout.
// It tolerates both the -bbox (word-only) and -bbox-layout (block/line/word)
// variants by walking elements instead of relying on a fixed tree shape.
func parseBBoxLayout(raw string) ([]LayoutPage, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("empty bbox layout")
	}
	decoder := xml.NewDecoder(strings.NewReader(raw))
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity

	pages := make([]LayoutPage, 0, 8)
	var currentPage *LayoutPage
	var currentBlock *LayoutBlock
	var lineWords []string
	var wordText strings.Builder
	var wordBBox [4]float64
	inWord := false

	appendWord := func() {
		text := strings.TrimSpace(wordText.String())
		wordText.Reset()
		if text == "" {
			return
		}
		if currentBlock != nil {
			expandBBox(&currentBlock.BBox, wordBBox)
		}
		lineWords = append(lineWords, text)
	}
	flushLine := func() {
		if len(lineWords) == 0 || currentBlock == nil {
			lineWords = nil
			return
		}
		currentBlock.Lines = append(currentBlock.Lines, joinLayoutWords(lineWords))
		lineWords = nil
	}
	flushBlock := func() {
		flushLine()
		if currentBlock != nil && currentBlock.Text() != "" {
			currentPage.Blocks = append(currentPage.Blocks, *currentBlock)
		}
		currentBlock = nil
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return pages, fmt.Errorf("decode bbox layout: %w", err)
		}
		switch element := token.(type) {
		case xml.StartElement:
			switch strings.ToLower(element.Name.Local) {
			case "page":
				if currentPage != nil {
					flushBlock()
					pages = append(pages, *currentPage)
				}
				currentPage = &LayoutPage{
					Number: len(pages) + 1,
					Width:  attrFloat(element, "width"),
					Height: attrFloat(element, "height"),
				}
			case "block":
				if currentPage != nil {
					flushBlock()
					currentBlock = &LayoutBlock{BBox: [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}}
				}
			case "line":
				flushLine()
			case "word":
				if currentBlock != nil {
					wordBBox = [4]float64{
						attrFloat(element, "xMin"),
						attrFloat(element, "yMin"),
						attrFloat(element, "xMax"),
						attrFloat(element, "yMax"),
					}
					inWord = true
					wordText.Reset()
				}
			}
		case xml.CharData:
			if inWord {
				wordText.Write([]byte(element))
			}
		case xml.EndElement:
			switch strings.ToLower(element.Name.Local) {
			case "word":
				if inWord {
					appendWord()
					inWord = false
				}
			case "line":
				flushLine()
			case "block":
				flushBlock()
			case "page":
				if currentPage != nil {
					flushBlock()
					pages = append(pages, *currentPage)
					currentPage = nil
				}
			}
		}
	}
	if currentPage != nil {
		flushBlock()
		pages = append(pages, *currentPage)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("bbox layout contains no pages")
	}
	return pages, nil
}

func attrFloat(element xml.StartElement, name string) float64 {
	for _, attr := range element.Attr {
		if strings.EqualFold(attr.Name.Local, name) {
			value := 0.0
			_, _ = fmt.Sscanf(attr.Value, "%f", &value)
			return value
		}
	}
	return 0
}

func expandBBox(box *[4]float64, other [4]float64) {
	if other[0] < box[0] {
		box[0] = other[0]
	}
	if other[1] < box[1] {
		box[1] = other[1]
	}
	if other[2] > box[2] {
		box[2] = other[2]
	}
	if other[3] > box[3] {
		box[3] = other[3]
	}
}

// joinLayoutWords joins words without a space between CJK characters.
func joinLayoutWords(words []string) string {
	var builder strings.Builder
	for index, word := range words {
		if index > 0 && needsSpaceBetween(words[index-1], word) {
			builder.WriteByte(' ')
		}
		builder.WriteString(word)
	}
	return builder.String()
}

func needsSpaceBetween(previous, next string) bool {
	prevRunes := []rune(previous)
	nextRunes := []rune(next)
	if len(prevRunes) == 0 || len(nextRunes) == 0 {
		return false
	}
	if isCJK(prevRunes[len(prevRunes)-1]) && isCJK(nextRunes[0]) {
		return false
	}
	return true
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
}

var sectionHeadingPattern = regexp.MustCompile(`(?i)^(abstract|introduction|related work|background|method(s|ology)?|approach|experiment(s|al)?|evaluation|result(s)?|discussion|limitation(s)?|conclusion(s)?|references|appendix|摘要|引言|绪论|相关工作|背景|方法|实验|评估|结果|讨论|局限|结论|参考文献|附录)\b`)

// BuildBlocks converts layout pages into document blocks with reading order,
// header/footer classification and title hints. It also returns the rebuilt
// page text in reading order (empty string when no blocks exist).
func BuildBlocks(pages []LayoutPage) ([]Block, map[int]string) {
	blocks := make([]Block, 0, len(pages)*4)
	textByPage := make(map[int]string, len(pages))

	// Count normalized block text across pages so repeated running heads are
	// classified even when they are slightly inside the 5% margin.
	frequency := make(map[string]int)
	for _, page := range pages {
		for _, block := range page.Blocks {
			key := normalizeHeaderText(block.Text())
			if key == "" {
				continue
			}
			frequency[key]++
		}
	}

	for _, page := range pages {
		height := page.Height
		if height <= 0 {
			height = 792
		}
		width := page.Width
		if width <= 0 {
			width = 612
		}
		typed := make([]Block, 0, len(page.Blocks))
		heights := make([]float64, 0, len(page.Blocks))
		for _, block := range page.Blocks {
			text := block.Text()
			if strings.TrimSpace(text) == "" {
				continue
			}
			heights = append(heights, block.BBox[3]-block.BBox[1])
			typed = append(typed, Block{
				Type:       BlockParagraph,
				BBox:       block.BBox,
				Text:       text,
				Confidence: 1,
				Page:       page.Number,
			})
		}
		if len(typed) == 0 {
			continue
		}
		sort.Float64s(heights)
		medianHeight := heights[len(heights)/2]
		for index := range typed {
			blockHeight := typed[index].BBox[3] - typed[index].BBox[1]
			// Running heads are short single lines. Requiring a short block
			// prevents a paragraph that merely starts at the top margin (for
			// example the first block of a two-column page) from being
			// mistaken for a header once its digits are normalized away.
			shortBlock := blockHeight <= 30 && len([]rune(typed[index].Text)) <= 160
			top := shortBlock && typed[index].BBox[1] <= height*0.05
			bottom := shortBlock && typed[index].BBox[3] >= height*0.95
			nearTop := shortBlock && typed[index].BBox[1] <= height*0.12
			nearBottom := shortBlock && typed[index].BBox[3] >= height*0.88
			key := normalizeHeaderText(typed[index].Text)
			repeated := shortBlock && key != "" && frequency[key] >= 2
			switch {
			case top || (nearTop && repeated):
				typed[index].Type = BlockHeader
			case bottom || (nearBottom && repeated):
				typed[index].Type = BlockFooter
			}
		}
		ordered := orderPageBlocks(typed, width, height)
		for index := range ordered {
			ordered[index].ID = fmt.Sprintf("p%d-b%d", page.Number, index+1)
			if ordered[index].Type == BlockParagraph && index == firstBodyIndex(ordered) {
				if medianHeight > 0 && (ordered[index].BBox[3]-ordered[index].BBox[1]) >= medianHeight*1.25 {
					ordered[index].Type = BlockTitle
				} else if sectionHeadingPattern.MatchString(strings.TrimSpace(ordered[index].Text)) {
					ordered[index].Type = BlockTitle
				}
			}
			blocks = append(blocks, ordered[index])
		}
		var builder strings.Builder
		for _, block := range ordered {
			if block.Type == BlockHeader || block.Type == BlockFooter {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(block.Text)
		}
		if builder.Len() > 0 {
			textByPage[page.Number] = builder.String()
		}
	}
	return blocks, textByPage
}

func firstBodyIndex(blocks []Block) int {
	for index, block := range blocks {
		if block.Type != BlockHeader && block.Type != BlockFooter {
			return index
		}
	}
	return -1
}

// orderPageBlocks orders header blocks first, body blocks in reading order
// (two-column aware), and footer blocks last.
func orderPageBlocks(blocks []Block, pageWidth, pageHeight float64) []Block {
	_ = pageHeight
	headers := make([]Block, 0, 2)
	footers := make([]Block, 0, 2)
	body := make([]Block, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case BlockHeader:
			headers = append(headers, block)
		case BlockFooter:
			footers = append(footers, block)
		default:
			body = append(body, block)
		}
	}
	sortBlocksByPosition(headers)
	sortBlocksByPosition(footers)
	orderedBody := orderBodyBlocks(body, pageWidth)
	result := make([]Block, 0, len(blocks))
	result = append(result, headers...)
	result = append(result, orderedBody...)
	result = append(result, footers...)
	return result
}

func sortBlocksByPosition(blocks []Block) {
	sort.SliceStable(blocks, func(i, j int) bool {
		if math.Abs(blocks[i].BBox[1]-blocks[j].BBox[1]) > 2 {
			return blocks[i].BBox[1] < blocks[j].BBox[1]
		}
		return blocks[i].BBox[0] < blocks[j].BBox[0]
	})
}

// orderBodyBlocks sorts body blocks by reading order. A page is treated as
// two-column when at least three narrow blocks sit on each side of its centre.
func orderBodyBlocks(blocks []Block, pageWidth float64) []Block {
	if len(blocks) <= 1 {
		return blocks
	}
	left := make([]Block, 0, len(blocks))
	right := make([]Block, 0, len(blocks))
	full := make([]Block, 0, 4)
	for _, block := range blocks {
		width := block.BBox[2] - block.BBox[0]
		if pageWidth > 0 && width >= pageWidth*0.65 {
			full = append(full, block)
			continue
		}
		center := (block.BBox[0] + block.BBox[2]) / 2
		if pageWidth > 0 && center < pageWidth/2 {
			left = append(left, block)
		} else {
			right = append(right, block)
		}
	}
	if len(left) < 3 || len(right) < 3 {
		sortBlocksByPosition(blocks)
		return blocks
	}
	sortBlocksByPosition(left)
	sortBlocksByPosition(right)
	if len(full) == 0 {
		return append(left, right...)
	}
	// Full-width blocks (titles, section banners) split the page into bands.
	sortBlocksByPosition(full)
	result := make([]Block, 0, len(blocks))
	remainingLeft := left
	remainingRight := right
	for _, banner := range full {
		bandLeft, nextLeft := takeAbove(remainingLeft, banner.BBox[1])
		bandRight, nextRight := takeAbove(remainingRight, banner.BBox[1])
		result = append(result, bandLeft...)
		result = append(result, bandRight...)
		result = append(result, banner)
		remainingLeft, remainingRight = nextLeft, nextRight
	}
	result = append(result, remainingLeft...)
	result = append(result, remainingRight...)
	return result
}

// takeAbove splits blocks whose top edge is above limit from the rest.
func takeAbove(blocks []Block, limit float64) ([]Block, []Block) {
	above := make([]Block, 0, len(blocks))
	rest := make([]Block, 0, len(blocks))
	for _, block := range blocks {
		if block.BBox[1] < limit {
			above = append(above, block)
		} else {
			rest = append(rest, block)
		}
	}
	return above, rest
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9\p{Han}]+`)

// normalizeHeaderText strips page numbers and punctuation so a running head
// repeated across pages hashes to the same key.
func normalizeHeaderText(text string) string {
	lower := strings.ToLower(strings.TrimSpace(text))
	lower = nonAlnum.ReplaceAllString(lower, " ")
	return strings.Join(strings.Fields(lower), " ")
}
