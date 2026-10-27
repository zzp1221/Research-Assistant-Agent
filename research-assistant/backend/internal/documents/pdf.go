package documents

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultOCRMaxPages    = 5
	defaultOCRPageTimeout = 60
	ocrRenderDPI          = 300
	// ocrOverallGrace is added to the per-page budget to bound the whole
	// fallback operation even when every page takes its full timeout.
	ocrOverallGrace = 30 * time.Second
)

// ExtractPDF shells out to poppler's pdftotext, which handles real academic
// PDFs (including CJK text layers) far better than a hand-written parser.
// The backend image installs poppler-utils; other environments receive an
// explicit "unsupported" status instead of a silent empty document.
//
// Scanned PDFs (no text layer) fall back to OCR when pdftoppm and tesseract
// are both available on PATH. See ocrFallbackPages.
func ExtractPDF(ctx context.Context, uploadID, path string) *Document {
	document := &Document{
		Version:     Version,
		UploadID:    uploadID,
		Status:      StatusFailed,
		Pages:       []Page{},
		ExtractedAt: time.Now().UTC(),
	}

	// Reject oversized inputs before spawning the parser so a malicious or
	// accidental huge PDF cannot exhaust parser memory/CPU.
	if limit := pdfParseMaxBytes(); limit > 0 {
		if info, statErr := os.Stat(path); statErr == nil && info.Size() > limit {
			document.Status = StatusUnsupported
			document.Error = fmt.Sprintf("PDF is %d bytes, exceeding the %d byte parse limit", info.Size(), limit)
			return document
		}
	}

	binary, err := exec.LookPath("pdftotext")
	if err != nil {
		document.Status = StatusUnsupported
		document.Error = "pdftotext is not installed; PDF text extraction is unavailable"
		return document
	}
	document.Extractor = "pdftotext"

	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, pdfParseTimeout())
	defer cancel()

	command := exec.CommandContext(runCtx, binary, "-layout", "-enc", "UTF-8", path, "-")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err = command.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		document.Error = "PDF text extraction timed out"
		return document
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		lower := strings.ToLower(message)
		switch {
		case strings.Contains(lower, "password") || strings.Contains(lower, "encrypted"):
			document.Status = StatusEncrypted
		case runtime.GOOS == "windows" && strings.Contains(lower, "not recognized"):
			document.Status = StatusUnsupported
		}
		document.Error = truncateMessage(message)
		return document
	}

	pages := SplitPages(stdout.String())
	document.Pages = pages
	document.PageCount = len(pages)
	for _, page := range pages {
		document.Characters += page.Chars
	}
	if document.Characters == 0 {
		return extractScannedPDF(ctx, document, path)
	}
	if document.PageCount == 0 {
		document.Status = StatusEmpty
		document.Error = "the PDF produced no pages"
		return document
	}
	document.Status = StatusExtracted
	return document
}

// pdfParseTimeout bounds one pdftotext run (PDF_PARSE_TIMEOUT_SECONDS).
func pdfParseTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PDF_PARSE_TIMEOUT_SECONDS"))
	value := 90
	if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
		value = parsed
	}
	if value > 600 {
		value = 600
	}
	return time.Duration(value) * time.Second
}

// pdfParseMaxBytes caps the input size handed to the parser
// (PDF_PARSE_MAX_MB, default 128 MiB).
func pdfParseMaxBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("PDF_PARSE_MAX_MB"))
	megabytes := 128
	if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
		megabytes = parsed
	}
	return int64(megabytes) * 1024 * 1024
}

// extractScannedPDF handles the zero-character case: it tries OCR and keeps a
// clear, actionable StatusEmpty error when that is not possible.
func extractScannedPDF(ctx context.Context, document *Document, path string) *Document {
	document.Status = StatusEmpty
	document.Error = "the PDF has no extractable text layer (it may be a scan)"

	pages, err := ocrFallbackPages(ctx, path)
	if err != nil {
		document.Error = truncateMessage(document.Error + "; " + err.Error())
		return document
	}
	document.Pages = pages
	document.PageCount = len(pages)
	document.Characters = 0
	for _, page := range pages {
		document.Characters += page.Chars
	}
	if document.Characters == 0 || document.PageCount == 0 {
		document.Error = truncateMessage(document.Error + "; OCR fallback produced no text")
		return document
	}
	document.Status = StatusExtracted
	document.Extractor = "tesseract-ocr"
	document.Error = ""
	return document
}

// ocrAvailable reports whether both commands needed by the OCR fallback are on
// PATH. It is the single availability check used by the fallback.
func ocrAvailable() bool {
	return ocrUnavailableReason() == ""
}

// ocrUnavailableReason names the missing OCR commands, or returns "" when the
// fallback can run.
func ocrUnavailableReason() string {
	missing := make([]string, 0, 2)
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		missing = append(missing, "pdftoppm")
	}
	if _, err := exec.LookPath("tesseract"); err != nil {
		missing = append(missing, "tesseract")
	}
	if len(missing) == 0 {
		return ""
	}
	return strings.Join(missing, " and ") + " not found on PATH"
}

// ocrMaxPages is the number of leading pages OCR renders. It is configurable
// with PDF_OCR_MAX_PAGES (default 5, capped at 50).
func ocrMaxPages() int {
	raw := strings.TrimSpace(os.Getenv("PDF_OCR_MAX_PAGES"))
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return defaultOCRMaxPages
	}
	if value > 50 {
		return 50
	}
	return value
}

// ocrPageTimeout bounds one page's render+OCR pair. It is configurable with
// PDF_OCR_TIMEOUT_SECONDS (default 60, capped at 600).
func ocrPageTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PDF_OCR_TIMEOUT_SECONDS"))
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		value = defaultOCRPageTimeout
	}
	if value > 600 {
		value = 600
	}
	return time.Duration(value) * time.Second
}

// ocrFallbackPages renders the first PDF_OCR_MAX_PAGES pages, runs tesseract
// over them and converts the OCR text into document pages. The whole operation
// is bounded by per-page timeouts plus a fixed grace period, so a hostile or
// broken PDF cannot hold the extractor forever.
func ocrFallbackPages(ctx context.Context, pdfPath string) ([]Page, error) {
	if reason := ocrUnavailableReason(); reason != "" {
		return nil, fmt.Errorf("OCR fallback is unavailable: %s", reason)
	}

	maxPages := ocrMaxPages()
	pageTimeout := ocrPageTimeout()
	overall := time.Duration(maxPages)*pageTimeout + ocrOverallGrace
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithTimeout(ctx, overall)
	defer cancel()

	pdftoppm, err := exec.LookPath("pdftoppm")
	if err != nil {
		return nil, fmt.Errorf("OCR fallback is unavailable: %s", ocrUnavailableReason())
	}
	tesseract, err := exec.LookPath("tesseract")
	if err != nil {
		return nil, fmt.Errorf("OCR fallback is unavailable: %s", ocrUnavailableReason())
	}

	workDir, err := os.MkdirTemp("", "pdf-ocr-")
	if err != nil {
		return nil, fmt.Errorf("OCR fallback failed: %s", truncateMessage(err.Error()))
	}
	defer os.RemoveAll(workDir)

	texts := make([]string, 0, maxPages)
	failures := make([]string, 0, maxPages)
	for page := 1; page <= maxPages; page++ {
		if runCtx.Err() != nil {
			failures = append(failures, fmt.Sprintf("page %d: OCR fallback timed out", page))
			texts = append(texts, "")
			continue
		}
		text, pageErr := ocrPage(runCtx, pdftoppm, tesseract, pdfPath, workDir, page, pageTimeout)
		if pageErr != nil {
			failures = append(failures, fmt.Sprintf("page %d: %s", page, pageErr.Error()))
			texts = append(texts, "")
			continue
		}
		texts = append(texts, text)
	}

	pages := pagesFromOCRTexts(texts)
	characters := 0
	for _, page := range pages {
		characters += page.Chars
	}
	if characters == 0 {
		detail := "tesseract produced no text"
		if len(failures) > 0 {
			detail = failures[0]
		}
		return nil, fmt.Errorf("OCR fallback failed: %s", truncateMessage(detail))
	}
	return pages, nil
}

// ocrPage renders exactly one page and runs tesseract over the image. Both
// commands share the per-page timeout.
func ocrPage(ctx context.Context, pdftoppm, tesseract, pdfPath, workDir string, page int, timeout time.Duration) (string, error) {
	pageCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	prefix := filepath.Join(workDir, fmt.Sprintf("page-%04d", page))
	var stdout, stderr bytes.Buffer
	render := exec.CommandContext(pageCtx, pdftoppm, ocrRenderArgs(pdfPath, prefix, page, ocrRenderDPI)...)
	render.Stdout = &stdout
	render.Stderr = &stderr
	if err := render.Run(); err != nil {
		if pageCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("page rendering timed out after %s", timeout)
		}
		return "", fmt.Errorf("pdftoppm failed: %s", firstNonEmpty(stderr.String(), err.Error()))
	}
	image := findRenderedImage(prefix)
	if image == "" {
		return "", fmt.Errorf("pdftoppm produced no image")
	}

	stdout.Reset()
	stderr.Reset()
	ocr := exec.CommandContext(pageCtx, tesseract, ocrTesseractArgs(image)...)
	ocr.Stdout = &stdout
	ocr.Stderr = &stderr
	if err := ocr.Run(); err != nil {
		if pageCtx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("OCR timed out after %s", timeout)
		}
		return "", fmt.Errorf("tesseract failed: %s", firstNonEmpty(stderr.String(), err.Error()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// ocrRenderArgs is the pdftoppm invocation for one page. Exposed as a helper so
// tests can validate argument construction without poppler installed.
func ocrRenderArgs(pdfPath, prefix string, page, dpi int) []string {
	return []string{
		"-f", strconv.Itoa(page),
		"-l", strconv.Itoa(page),
		"-r", strconv.Itoa(dpi),
		"-png",
		"-singlefile",
		pdfPath,
		prefix,
	}
}

// ocrTesseractArgs is the tesseract invocation for one rendered page. It
// writes UTF-8 text to stdout. PDF_OCR_LANG selects the language pack(s) and
// defaults to "eng".
func ocrTesseractArgs(imagePath string) []string {
	language := strings.TrimSpace(os.Getenv("PDF_OCR_LANG"))
	if language == "" {
		language = "eng"
	}
	return []string{imagePath, "stdout", "-l", language}
}

// findRenderedImage locates pdftoppm's output. With -singlefile poppler writes
// "<prefix>.png"; other builds append the page number, so check both forms.
func findRenderedImage(prefix string) string {
	for _, candidate := range []string{prefix + ".png", prefix + "-1.png", prefix + "-01.png", prefix} {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

// pagesFromOCRTexts joins per-page OCR text with the form-feed separator used
// by pdftotext and reuses SplitPages so OCR pages share the same cleaning and
// numbering rules as the text-layer path.
func pagesFromOCRTexts(texts []string) []Page {
	if len(texts) == 0 {
		return []Page{}
	}
	return SplitPages(strings.Join(texts, ""))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func truncateMessage(message string) string {
	if len(message) > 500 {
		return message[:500] + "..."
	}
	return fmt.Sprint(message)
}
