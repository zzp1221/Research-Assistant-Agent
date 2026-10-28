package documents

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"research-assistant-backend/internal/uploads"
)

const (
	defaultOCRMaxPages    = 5
	defaultOCRPageTimeout = 60
	ocrRenderDPI          = 300
	// ocrOverallGrace is added to the per-page budget to bound the whole
	// fallback operation even when every page takes its full timeout.
	ocrOverallGrace        = 30 * time.Second
	defaultOCRLanguage     = "eng+chi_sim"
	defaultOCRMinTextChars = 20
)

// extractionConfig freezes the knobs that affect the extracted document. It
// participates in the OCR cache key so a configuration change invalidates
// cached results.
type extractionConfig struct {
	OCRMaxPages     int
	OCRPageTimeout  time.Duration
	OCRLanguage     string
	OCRMinTextChars int
	RenderDPI       int
}

func currentExtractionConfig() extractionConfig {
	return extractionConfig{
		OCRMaxPages:     ocrMaxPages(),
		OCRPageTimeout:  ocrPageTimeout(),
		OCRLanguage:     ocrLanguage(),
		OCRMinTextChars: ocrMinTextChars(),
		RenderDPI:       ocrRenderDPI,
	}
}

func (c extractionConfig) signature() string {
	return fmt.Sprintf("v2|max=%d|lang=%s|min=%d|dpi=%d|timeout=%s",
		c.OCRMaxPages, c.OCRLanguage, c.OCRMinTextChars, c.RenderDPI, c.OCRPageTimeout)
}

// ExtractPDF shells out to poppler's pdftotext, which handles real academic
// PDFs (including CJK text layers) far better than a hand-written parser.
//
// The extraction is page-oriented: pages with a usable text layer keep it,
// pages with little or no text are rendered and OCR'd (up to the
// PDF_OCR_MAX_PAGES budget), and every page records how it was produced so
// the API can expose an honest quality report.
func ExtractPDF(ctx context.Context, uploadID, path string) *Document {
	document := &Document{
		Version:     Version,
		UploadID:    uploadID,
		Status:      StatusFailed,
		Pages:       []Page{},
		Blocks:      []Block{},
		Tables:      []Table{},
		Figures:     []Figure{},
		Quality:     []PageQuality{},
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
	if cached := loadDocumentCache(path, uploadID); cached != nil {
		return cached
	}

	totalPages := pdfPageCount(path)
	plainText, plainErr := runPDFTool(ctx, pdfParseTimeout(), binary, "-layout", "-enc", "UTF-8", path, "-")
	if plainErr != nil {
		message := strings.TrimSpace(plainErr.Error())
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
	pages := SplitPages(string(plainText))
	if totalPages < len(pages) {
		totalPages = len(pages)
	}
	if totalPages == 0 {
		totalPages = len(pages)
	}

	layouts := []LayoutPage{}
	if raw, layoutErr := runPDFTool(ctx, pdfParseTimeout(), binary, "-bbox-layout", "-enc", "UTF-8", path, "-"); layoutErr == nil {
		if parsed, parseErr := parseBBoxLayout(string(raw)); parseErr == nil {
			layouts = parsed
		}
	}
	blocks, textByPage := BuildBlocks(layouts)

	config := currentExtractionConfig()
	resultPages := make([]Page, totalPages)
	quality := make([]PageQuality, totalPages)
	ocrBlocks := make([]Block, 0)
	ocrUsed := 0
	ocrConfidenceSum := 0.0
	ocrConfidenceCount := 0
	failedPages := 0
	skippedPages := 0

	for pageNumber := 1; pageNumber <= totalPages; pageNumber++ {
		text := ""
		if pageNumber-1 < len(pages) {
			text = pages[pageNumber-1].Text
		}
		chars := utf8.RuneCountInString(text)
		source := PageSourceText
		pageQuality := PageQuality{Page: pageNumber, Source: source, Chars: chars}
		needsOCR := chars < config.OCRMinTextChars
		if needsOCR {
			switch {
			case !ocrAvailable():
				pageQuality.Skipped = true
				pageQuality.Error = "OCR tools are unavailable"
				skippedPages++
			case ocrUsed >= config.OCRMaxPages:
				pageQuality.Skipped = true
				pageQuality.Error = fmt.Sprintf("OCR budget of %d pages exhausted", config.OCRMaxPages)
				skippedPages++
			default:
				ocrUsed++
				result, ocrErr := ocrPageText(ctx, path, pageNumber, config)
				if ocrErr != nil {
					pageQuality.Failed = true
					pageQuality.Error = truncateMessage(ocrErr.Error())
					failedPages++
				} else if strings.TrimSpace(result.Text) == "" {
					// A blank page is not an OCR failure; keep the page empty
					// and do not degrade the document status.
					pageQuality.Source = "blank"
					source = "blank"
				} else {
					if chars == 0 {
						text = result.Text
						source = PageSourceOCR
					} else {
						text = strings.TrimSpace(text) + "\n" + result.Text
						source = PageSourceMixed
					}
					pageQuality.OCRConfidence = result.Confidence
					ocrConfidenceSum += result.Confidence
					ocrConfidenceCount++
					for _, block := range result.Blocks {
						block.Page = pageNumber
						ocrBlocks = append(ocrBlocks, block)
					}
				}
			}
		}
		pageQuality.Source = source
		resultPages[pageNumber-1] = Page{Number: pageNumber, Text: CleanText(text), Chars: utf8.RuneCountInString(CleanText(text)), Source: source}
		pageQuality.Chars = resultPages[pageNumber-1].Chars
		quality[pageNumber-1] = pageQuality
	}

	// Reading order: when bbox blocks cover a meaningful share of a page's
	// text, prefer the block order over pdftotext's line order.
	for pageNumber, blockText := range textByPage {
		if pageNumber < 1 || pageNumber > len(resultPages) {
			continue
		}
		original := resultPages[pageNumber-1].Text
		if float64(utf8.RuneCountInString(blockText)) >= float64(utf8.RuneCountInString(original))*0.5 {
			resultPages[pageNumber-1].Text = blockText
			resultPages[pageNumber-1].Chars = utf8.RuneCountInString(blockText)
			quality[pageNumber-1].Chars = resultPages[pageNumber-1].Chars
		}
	}

	if len(ocrBlocks) > 0 {
		blocks = append(blocks, ocrBlocks...)
		sort.SliceStable(blocks, func(i, j int) bool { return blocks[i].Page < blocks[j].Page })
	}

	tables := make([]Table, 0)
	figures := make([]Figure, 0)
	characters := 0
	for _, page := range resultPages {
		characters += page.Chars
		tables = append(tables, extractTablesFromPageText(page.Number, page.Text)...)
		figures = append(figures, extractFiguresFromPageText(page.Number, page.Text)...)
	}
	if len(figures) > 0 {
		figures = attachFigureImages(path, figures)
	}

	document.Pages = resultPages
	document.PageCount = totalPages
	document.TotalPages = totalPages
	document.Characters = characters
	document.Blocks = blocks
	document.Tables = tables
	document.Figures = figures
	document.Quality = quality
	document.OCRStats = OCRStats{
		Engine:            "tesseract",
		Language:          config.OCRLanguage,
		OCRPages:          ocrUsed,
		AverageConfidence: averageConfidence(ocrConfidenceSum, ocrConfidenceCount),
		FailedPages:       failedPages,
		SkippedPages:      skippedPages,
		TotalPages:        totalPages,
		Partial:           failedPages > 0 || skippedPages > 0,
	}
	if ocrUsed > 0 {
		document.Extractor = "pdftotext+tesseract-ocr"
	}

	switch {
	case characters == 0:
		document.Status = StatusEmpty
		if ocrUsed > 0 {
			document.Error = "the PDF has no extractable text layer; OCR produced no text"
		} else {
			document.Error = "the PDF has no extractable text layer (it may be a scan)"
		}
	case failedPages > 0 || skippedPages > 0:
		document.Status = StatusPartial
		document.Error = fmt.Sprintf("%d page(s) failed OCR, %d page(s) skipped", failedPages, skippedPages)
	default:
		document.Status = StatusExtracted
	}
	saveDocumentCache(path, document)
	return document
}

func averageConfidence(sum float64, count int) float64 {
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// runPDFTool runs one poppler command with a timeout and returns stdout.
func runPDFTool(ctx context.Context, timeout time.Duration, binary string, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, binary, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("PDF command timed out")
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("%s", message)
	}
	return stdout.Bytes(), nil
}

// pdfPageCount reads the real page count from pdfinfo. It returns 0 when
// pdfinfo is unavailable or cannot parse the file.
func pdfPageCount(path string) int {
	binary, err := exec.LookPath("pdfinfo")
	if err != nil {
		return 0
	}
	output, err := exec.Command(binary, path).Output()
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(line)), "pages:") {
			value := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			if parsed, parseErr := strconv.Atoi(value); parseErr == nil && parsed > 0 {
				return parsed
			}
		}
	}
	return 0
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

// ocrMaxPages is the OCR budget for one extraction (PDF_OCR_MAX_PAGES,
// default 5, capped at 50). It is a budget over pages that need OCR, not a
// cap on the document's page count.
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

// ocrLanguage selects the tesseract language pack(s). The default includes
// Chinese because the backend image ships chi_sim.
func ocrLanguage() string {
	raw := strings.TrimSpace(os.Getenv("PDF_OCR_LANG"))
	if raw == "" {
		return defaultOCRLanguage
	}
	return raw
}

// ocrMinTextChars is the per-page threshold below which a page is considered
// to need OCR (PDF_OCR_MIN_TEXT_CHARS, default 20).
func ocrMinTextChars() int {
	raw := strings.TrimSpace(os.Getenv("PDF_OCR_MIN_TEXT_CHARS"))
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return defaultOCRMinTextChars
	}
	if value > 1000 {
		return 1000
	}
	return value
}

// ocrPageTimeout bounds one page's render+OCR pair. It is configurable with
// PDF_OCR_TIMEOUT_SECONDS (default 60, capped at 600).
func ocrPageTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("PDF_OCR_TIMEOUT_SECONDS"))
	value := defaultOCRPageTimeout
	if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
		value = parsed
	}
	if value > 600 {
		value = 600
	}
	return time.Duration(value) * time.Second
}

// OCRPageResult is one page's OCR output.
type OCRPageResult struct {
	Text       string
	Confidence float64
	Blocks     []Block
}

// ocrPageText renders one page and runs tesseract with TSV output so word
// confidences and block boxes survive into the document.
func ocrPageText(ctx context.Context, pdfPath string, page int, config extractionConfig) (*OCRPageResult, error) {
	if reason := ocrUnavailableReason(); reason != "" {
		return nil, fmt.Errorf("OCR unavailable: %s", reason)
	}
	pdftoppm, _ := exec.LookPath("pdftoppm")
	tesseract, _ := exec.LookPath("tesseract")
	workDir, err := os.MkdirTemp("", "pdf-ocr-")
	if err != nil {
		return nil, fmt.Errorf("OCR failed: %s", truncateMessage(err.Error()))
	}
	defer os.RemoveAll(workDir)

	pageCtx, cancel := context.WithTimeout(ctx, config.OCRPageTimeout)
	defer cancel()
	prefix := filepath.Join(workDir, fmt.Sprintf("page-%04d", page))
	render := exec.CommandContext(pageCtx, pdftoppm, ocrRenderArgs(pdfPath, prefix, page, config.RenderDPI)...)
	var stderr bytes.Buffer
	render.Stderr = &stderr
	if err := render.Run(); err != nil {
		if pageCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("page rendering timed out after %s", config.OCRPageTimeout)
		}
		return nil, fmt.Errorf("pdftoppm failed: %s", firstNonEmpty(stderr.String(), err.Error()))
	}
	image := findRenderedImage(prefix)
	if image == "" {
		return nil, fmt.Errorf("pdftoppm produced no image")
	}

	var stdout bytes.Buffer
	stderr.Reset()
	ocr := exec.CommandContext(pageCtx, tesseract, image, "stdout", "-l", config.OCRLanguage, "tsv")
	ocr.Stdout = &stdout
	ocr.Stderr = &stderr
	if err := ocr.Run(); err != nil {
		if pageCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("OCR timed out after %s", config.OCRPageTimeout)
		}
		return nil, fmt.Errorf("tesseract failed: %s", firstNonEmpty(stderr.String(), err.Error()))
	}
	text, confidence, blocks := parseTesseractTSV(stdout.String(), config.RenderDPI)
	return &OCRPageResult{Text: text, Confidence: confidence, Blocks: blocks}, nil
}

// ocrFallbackPages renders the first PDF_OCR_MAX_PAGES pages, runs tesseract
// over them and converts the OCR text into document pages. It is retained for
// the legacy all-scan fallback and for compatibility with existing callers.
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

// ocrTesseractArgs is the plain-text tesseract invocation for one rendered
// page. PDF_OCR_LANG selects the language pack(s); the default includes
// English and Simplified Chinese.
func ocrTesseractArgs(imagePath string) []string {
	return []string{imagePath, "stdout", "-l", ocrLanguage()}
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
	return SplitPages(strings.Join(texts, "\f"))
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

// ---- OCR cache ----

func documentCacheEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("DOCUMENT_CACHE_DISABLED")), "true")
}

func documentCacheDir() string {
	if configured := strings.TrimSpace(os.Getenv("DOCUMENT_CACHE_DIR")); configured != "" {
		return configured
	}
	return filepath.Join(filepath.Dir(uploads.Root()), "document-cache")
}

// documentCacheKey hashes the PDF bytes and the extraction configuration.
func documentCacheKey(path string, config extractionConfig) (string, bool) {
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", false
	}
	hash.Write([]byte(config.signature()))
	return hex.EncodeToString(hash.Sum(nil))[:40], true
}

func loadDocumentCache(pdfPath, uploadID string) *Document {
	if !documentCacheEnabled() {
		return nil
	}
	key, ok := documentCacheKey(pdfPath, currentExtractionConfig())
	if !ok {
		return nil
	}
	entryDir := filepath.Join(documentCacheDir(), key)
	document, err := Load(entryDir)
	if err != nil {
		return nil
	}
	targetDir := filepath.Dir(pdfPath)
	copyDirectory(filepath.Join(entryDir, "tables"), filepath.Join(targetDir, "tables"))
	copyDirectory(filepath.Join(entryDir, "figures"), filepath.Join(targetDir, "figures"))
	document.UploadID = uploadID
	document.ExtractedAt = time.Now().UTC()
	return document
}

func saveDocumentCache(pdfPath string, document *Document) {
	if !documentCacheEnabled() || document == nil {
		return
	}
	key, ok := documentCacheKey(pdfPath, currentExtractionConfig())
	if !ok {
		return
	}
	entryDir := filepath.Join(documentCacheDir(), key)
	if err := Save(entryDir, document); err != nil {
		return
	}
	copyDirectory(filepath.Join(filepath.Dir(pdfPath), "figures"), filepath.Join(entryDir, "figures"))
}

// copyDirectory copies regular files from source to target; missing sources
// are ignored so cache misses stay cheap.
func copyDirectory(source, target string) {
	entries, err := os.ReadDir(source)
	if err != nil {
		return
	}
	_ = os.MkdirAll(target, 0o700)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			continue
		}
		_ = os.WriteFile(filepath.Join(target, entry.Name()), raw, 0o600)
	}
}
