package documents

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOCRCommandArgumentBuilders(t *testing.T) {
	rendered := ocrRenderArgs("/tmp/paper.pdf", "/tmp/page-0001", 3, 300)
	wantRendered := []string{
		"-f", "3", "-l", "3", "-r", "300", "-png", "-singlefile",
		"/tmp/paper.pdf", "/tmp/page-0001",
	}
	if !reflect.DeepEqual(rendered, wantRendered) {
		t.Fatalf("ocrRenderArgs=%#v, want %#v", rendered, wantRendered)
	}

	t.Setenv("PDF_OCR_LANG", "")
	if got := ocrTesseractArgs("/tmp/page-0001.png"); !reflect.DeepEqual(got, []string{"/tmp/page-0001.png", "stdout", "-l", "eng"}) {
		t.Fatalf("default tesseract args=%#v", got)
	}
	t.Setenv("PDF_OCR_LANG", "eng+chi_sim")
	if got := ocrTesseractArgs("/tmp/page-0001.png"); !reflect.DeepEqual(got, []string{"/tmp/page-0001.png", "stdout", "-l", "eng+chi_sim"}) {
		t.Fatalf("configured tesseract args=%#v", got)
	}
}

func TestOCRPageLimitAndTimeoutEnvironment(t *testing.T) {
	t.Setenv("PDF_OCR_MAX_PAGES", "")
	if got := ocrMaxPages(); got != defaultOCRMaxPages {
		t.Fatalf("default max pages=%d, want %d", got, defaultOCRMaxPages)
	}
	t.Setenv("PDF_OCR_MAX_PAGES", "3")
	if got := ocrMaxPages(); got != 3 {
		t.Fatalf("max pages=%d, want 3", got)
	}
	t.Setenv("PDF_OCR_MAX_PAGES", "not-a-number")
	if got := ocrMaxPages(); got != defaultOCRMaxPages {
		t.Fatalf("invalid max pages=%d, want %d", got, defaultOCRMaxPages)
	}
	t.Setenv("PDF_OCR_MAX_PAGES", "999")
	if got := ocrMaxPages(); got != 50 {
		t.Fatalf("capped max pages=%d, want 50", got)
	}

	t.Setenv("PDF_OCR_TIMEOUT_SECONDS", "")
	if got := ocrPageTimeout(); got != defaultOCRPageTimeout*time.Second {
		t.Fatalf("default timeout=%s", got)
	}
	t.Setenv("PDF_OCR_TIMEOUT_SECONDS", "12")
	if got := ocrPageTimeout(); got != 12*time.Second {
		t.Fatalf("timeout=%s, want 12s", got)
	}
	t.Setenv("PDF_OCR_TIMEOUT_SECONDS", "nope")
	if got := ocrPageTimeout(); got != defaultOCRPageTimeout*time.Second {
		t.Fatalf("invalid timeout=%s", got)
	}
	t.Setenv("PDF_OCR_TIMEOUT_SECONDS", "9999")
	if got := ocrPageTimeout(); got != 600*time.Second {
		t.Fatalf("capped timeout=%s, want 600s", got)
	}
}

func TestOCRFallbackSkippedWhenToolsMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("PDF_OCR_MAX_PAGES", "2")
	if ocrAvailable() {
		t.Fatal("OCR should be unavailable with an empty PATH")
	}
	if reason := ocrUnavailableReason(); !strings.Contains(reason, "not found on PATH") {
		t.Fatalf("unavailable reason=%q", reason)
	}
	pages, err := ocrFallbackPages(context.Background(), filepath.Join(t.TempDir(), "missing.pdf"))
	if err == nil {
		t.Fatal("expected an OCR availability error")
	}
	if len(pages) != 0 {
		t.Fatalf("fallback returned pages without tools: %#v", pages)
	}
	if !strings.Contains(err.Error(), "OCR fallback is unavailable") {
		t.Fatalf("error=%q", err.Error())
	}
	if !strings.Contains(err.Error(), "pdftoppm") || !strings.Contains(err.Error(), "tesseract") {
		t.Fatalf("error should name missing OCR tools: %q", err.Error())
	}
}

func TestPagesFromOCRTextsUsesSplitPages(t *testing.T) {
	pages := pagesFromOCRTexts([]string{"page one text", "page two text"})
	if len(pages) != 2 {
		t.Fatalf("pages=%#v", pages)
	}
	if pages[0].Number != 1 || pages[1].Number != 2 {
		t.Fatalf("page numbers=%#v", pages)
	}
	if pages[1].Text != "page two text" || pages[1].Chars != len([]rune("page two text")) {
		t.Fatalf("page two=%#v", pages[1])
	}

	middleEmpty := pagesFromOCRTexts([]string{"one", "", "three"})
	if len(middleEmpty) != 3 || middleEmpty[1].Text != "" || middleEmpty[2].Number != 3 {
		t.Fatalf("empty middle page lost its numbering: %#v", middleEmpty)
	}
}

func TestFindRenderedImageSupportsPopplerVariants(t *testing.T) {
	directory := t.TempDir()
	prefix := filepath.Join(directory, "page-0001")
	if got := findRenderedImage(prefix); got != "" {
		t.Fatalf("missing image should not be found: %q", got)
	}
	numbered := prefix + "-1.png"
	if err := os.WriteFile(numbered, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findRenderedImage(prefix); got != numbered {
		t.Fatalf("numbered image=%q, want %q", got, numbered)
	}
	single := prefix + ".png"
	if err := os.WriteFile(single, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := findRenderedImage(prefix); got != single {
		t.Fatalf("singlefile image=%q, want %q", got, single)
	}
}
