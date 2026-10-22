package documents

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ExtractPDF shells out to poppler's pdftotext, which handles real academic
// PDFs (including CJK text layers) far better than a hand-written parser.
// The backend image installs poppler-utils; other environments receive an
// explicit "unsupported" status instead of a silent empty document.
func ExtractPDF(ctx context.Context, uploadID, path string) *Document {
	document := &Document{
		Version:     Version,
		UploadID:    uploadID,
		Status:      StatusFailed,
		Pages:       []Page{},
		ExtractedAt: time.Now().UTC(),
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
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
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
		document.Status = StatusEmpty
		document.Error = "the PDF has no extractable text layer (it may be a scan)"
		return document
	}
	if document.PageCount == 0 {
		document.Status = StatusEmpty
		document.Error = "the PDF produced no pages"
		return document
	}
	document.Status = StatusExtracted
	return document
}

func truncateMessage(message string) string {
	if len(message) > 500 {
		return message[:500] + "..."
	}
	return fmt.Sprint(message)
}
