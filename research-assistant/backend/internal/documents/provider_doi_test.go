package documents

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestApplyProviderRecordKeepsDOI(t *testing.T) {
	base := models.PaperMetadata{Title: "Attention Is All You Need", Year: 2017}
	merged, doi := ApplyProviderRecord(base, ProviderRecord{
		Source: "arxiv", Title: "Attention Is All You Need", Year: 2017,
		DOI: "https://doi.org/10.48550/arXiv.1706.03762", ArxivID: "1706.03762v5",
	})
	if doi != "10.48550/arXiv.1706.03762" {
		t.Fatalf("doi=%q", doi)
	}
	if merged.DOI != doi || merged.ArxivID != "1706.03762" {
		t.Fatalf("merged=%+v", merged)
	}
}
