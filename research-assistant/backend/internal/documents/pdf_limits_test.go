package documents

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractPDFRejectsOversizedInputBeforeParsing(t *testing.T) {
	t.Setenv("PDF_PARSE_MAX_MB", "1")
	path := filepath.Join(t.TempDir(), "huge.pdf")
	if err := os.WriteFile(path, make([]byte, 2*1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	document := ExtractPDF(context.Background(), "upload-huge", path)
	if document.Status != StatusUnsupported {
		t.Fatalf("status=%s error=%s", document.Status, document.Error)
	}
	if !strings.Contains(document.Error, "parse limit") {
		t.Fatalf("error=%q", document.Error)
	}
}

func TestPDFParseConfigDefaults(t *testing.T) {
	t.Setenv("PDF_PARSE_TIMEOUT_SECONDS", "")
	t.Setenv("PDF_PARSE_MAX_MB", "")
	if got := pdfParseTimeout(); got != 90*time.Second {
		t.Fatalf("timeout=%v", got)
	}
	if got := pdfParseMaxBytes(); got != 128*1024*1024 {
		t.Fatalf("max bytes=%d", got)
	}
	t.Setenv("PDF_PARSE_TIMEOUT_SECONDS", "5")
	t.Setenv("PDF_PARSE_MAX_MB", "2")
	if got := pdfParseTimeout(); got != 5*time.Second {
		t.Fatalf("configured timeout=%v", got)
	}
	if got := pdfParseMaxBytes(); got != 2*1024*1024 {
		t.Fatalf("configured max bytes=%d", got)
	}
}
