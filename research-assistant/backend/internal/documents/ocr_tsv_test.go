package documents

import (
	"math"
	"testing"
)

const sampleTSV = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n" +
	"1\t1\t1\t1\t1\t1\t100\t200\t50\t20\t-1\t\n" +
	"5\t1\t1\t1\t1\t1\t100\t200\t50\t20\t96.5\tHello\n" +
	"5\t1\t1\t1\t1\t2\t160\t200\t50\t20\t90.0\tworld\n" +
	"5\t1\t1\t1\t2\t1\t100\t230\t80\t20\t80.0\tSecond\n" +
	"5\t1\t2\t1\t1\t1\t100\t400\t40\t20\t70.0\tBlock2\n"

func TestParseTesseractTSV(t *testing.T) {
	text, confidence, blocks := parseTesseractTSV(sampleTSV, 300)
	if text != "Hello world\nSecond\nBlock2" {
		t.Fatalf("text=%q", text)
	}
	wantConfidence := (96.5 + 90.0 + 80.0 + 70.0) / 4 / 100
	if math.Abs(confidence-wantConfidence) > 1e-9 {
		t.Fatalf("confidence=%v want=%v", confidence, wantConfidence)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks=%d", len(blocks))
	}
	if blocks[0].Text != "Hello world\nSecond" {
		t.Fatalf("block text=%q", blocks[0].Text)
	}
	// 100px at 300dpi is 24pt.
	if math.Abs(blocks[0].BBox[0]-24) > 1e-6 {
		t.Fatalf("bbox=%v", blocks[0].BBox)
	}
}

func TestParseTesseractTSVToleratesGarbage(t *testing.T) {
	text, confidence, blocks := parseTesseractTSV("garbage\nnot\ta\ttsv\n", 300)
	if text != "" || confidence != 0 || len(blocks) != 0 {
		t.Fatalf("unexpected parse: %q %v %v", text, confidence, blocks)
	}
}
