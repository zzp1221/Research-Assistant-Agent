package documents

import (
	"strings"
	"testing"
)

const sampleBBoxLayout = `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><body><doc>
<page width="600.000000" height="800.000000">
<block><line><word xMin="40" yMin="20" xMax="200" yMax="32">Journal of Tests</word></line></block>
<block><line><word xMin="40" yMin="100" xMax="120" yMax="112">Left</word><word xMin="130" yMin="100" xMax="200" yMax="112">one</word></line></block>
<block><line><word xMin="40" yMin="200" xMax="120" yMax="212">Left</word><word xMin="130" yMin="200" xMax="200" yMax="212">two</word></line></block>
<block><line><word xMin="40" yMin="300" xMax="120" yMax="312">Left</word><word xMin="130" yMin="300" xMax="200" yMax="312">three</word></line></block>
<block><line><word xMin="320" yMin="100" xMax="400" yMax="112">Right</word><word xMin="410" yMin="100" xMax="480" yMax="112">one</word></line></block>
<block><line><word xMin="320" yMin="200" xMax="400" yMax="212">Right</word><word xMin="410" yMin="200" xMax="480" yMax="212">two</word></line></block>
<block><line><word xMin="320" yMin="300" xMax="400" yMax="312">Right</word><word xMin="410" yMin="300" xMax="480" yMax="312">three</word></line></block>
<block><line><word xMin="40" yMin="760" xMax="100" yMax="772">1</word></line></block>
</page>
<page width="600.000000" height="800.000000">
<block><line><word xMin="40" yMin="20" xMax="200" yMax="32">Journal of Tests</word></line></block>
<block><line><word xMin="40" yMin="100" xMax="200" yMax="112">Body</word></line></block>
</page>
</doc></body></html>`

func TestParseBBoxLayoutAndReadingOrder(t *testing.T) {
	pages, err := parseBBoxLayout(sampleBBoxLayout)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("pages=%d", len(pages))
	}
	if pages[0].Width != 600 || pages[0].Height != 800 {
		t.Fatalf("page size=%v x %v", pages[0].Width, pages[0].Height)
	}
	if len(pages[0].Blocks) != 8 {
		t.Fatalf("blocks=%d", len(pages[0].Blocks))
	}
	if got := pages[0].Blocks[1].Text(); got != "Left one" {
		t.Fatalf("block text=%q", got)
	}
	blocks, textByPage := BuildBlocks(pages)
	if len(blocks) == 0 {
		t.Fatal("no blocks built")
	}
	// Header must come first and be classified as header on both pages.
	if blocks[0].Type != BlockHeader || !strings.Contains(blocks[0].Text, "Journal of Tests") {
		t.Fatalf("first block=%#v", blocks[0])
	}
	// Two-column reading order: left column before right column.
	var bodyTexts []string
	for _, block := range blocks {
		if block.Page == 1 && block.Type != BlockHeader && block.Type != BlockFooter {
			bodyTexts = append(bodyTexts, block.Text)
		}
	}
	want := []string{"Left one", "Left two", "Left three", "Right one", "Right two", "Right three"}
	if strings.Join(bodyTexts, "|") != strings.Join(want, "|") {
		t.Fatalf("reading order=%q want=%q", bodyTexts, want)
	}
	if !strings.Contains(textByPage[1], "Left one") || !strings.Contains(textByPage[1], "Right three") {
		t.Fatalf("page text=%q", textByPage[1])
	}
	if strings.Index(textByPage[1], "Left three") > strings.Index(textByPage[1], "Right one") {
		t.Fatalf("columns interleaved: %q", textByPage[1])
	}
	if blocks[0].Page != 1 {
		t.Fatalf("block page=%d", blocks[0].Page)
	}
}

func TestBuildBlocksSingleColumnAndFooter(t *testing.T) {
	pages := []LayoutPage{{
		Number: 1, Width: 600, Height: 800,
		Blocks: []LayoutBlock{
			{BBox: [4]float64{40, 20, 300, 32}, Lines: []string{"Running head"}},
			{BBox: [4]float64{40, 90, 560, 130}, Lines: []string{"A Large Document Title"}},
			{BBox: [4]float64{40, 200, 560, 220}, Lines: []string{"Second wide paragraph block."}},
			{BBox: [4]float64{40, 770, 100, 782}, Lines: []string{"1"}},
		},
	}}
	blocks, _ := BuildBlocks(pages)
	if len(blocks) != 4 {
		t.Fatalf("blocks=%d", len(blocks))
	}
	if blocks[0].Type != BlockHeader {
		t.Fatalf("header type=%q", blocks[0].Type)
	}
	if blocks[len(blocks)-1].Type != BlockFooter {
		t.Fatalf("footer type=%q", blocks[len(blocks)-1].Type)
	}
	if blocks[1].Type != BlockTitle {
		t.Fatalf("first body block should be title, got %q", blocks[1].Type)
	}
}
