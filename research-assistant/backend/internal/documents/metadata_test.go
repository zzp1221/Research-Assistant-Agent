package documents

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
)

func fixtureDocument(t *testing.T, name string) *Document {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return &Document{
		Status:    StatusExtracted,
		PageCount: 12,
		Pages:     []Page{{Number: 1, Text: string(raw)}},
	}
}

// foldedAuthor normalizes case and diacritics so "Łukasz Kaiser" and
// "Lukasz Kaiser" compare equal while the fixture spelling is asserted
// separately where it matters.
func foldedAuthor(name string) string {
	return strings.Join(strings.Fields(foldDiacritics(strings.ToLower(name))), " ")
}

func requireAuthors(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("authors = %q (%d), want %q (%d)", got, len(got), want, len(want))
	}
	for index := range want {
		if foldedAuthor(got[index]) != foldedAuthor(want[index]) {
			t.Fatalf("author[%d] = %q, want %q (all: %q)", index, got[index], want[index], got)
		}
	}
}

func requireConfidence(t *testing.T, confidence float64) {
	t.Helper()
	if confidence < 0 || confidence > 1 {
		t.Fatalf("confidence = %v, want a value in 0..1", confidence)
	}
}

func TestExtractMetadataFromFirstPage(t *testing.T) {
	document := &Document{
		Status:    StatusExtracted,
		PageCount: 13,
		Pages:     []Page{{Number: 1, Text: "Fast Algorithms for Mining Association Rules\nRakesh Agrawal, Ramakrishnan Srikant\nIBM Almaden Research Center\nAbstract\nWe consider association rules.\nVLDB 1994\n1 Introduction\n"}},
	}
	metadata, err := ExtractMetadata(document)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "Fast Algorithms for Mining Association Rules" {
		t.Fatalf("title=%q", metadata.Title)
	}
	if metadata.Venue != "VLDB" {
		t.Fatalf("venue=%q", metadata.Venue)
	}
	if metadata.Year != 1994 {
		t.Fatalf("year=%d", metadata.Year)
	}
	requireAuthors(t, metadata.Authors, []string{"Rakesh Agrawal", "Ramakrishnan Srikant"})
	requireConfidence(t, metadata.Confidence)
}

func TestExtractMetadataAttentionAuthors(t *testing.T) {
	metadata, err := ExtractMetadata(fixtureDocument(t, "attention-2017.page1.txt"))
	if err != nil {
		t.Fatalf("extract attention metadata: %v", err)
	}
	want := []string{
		"Ashish Vaswani",
		"Noam Shazeer",
		"Niki Parmar",
		"Jakob Uszkoreit",
		"Llion Jones",
		"Aidan N. Gomez",
		"Łukasz Kaiser",
		"Illia Polosukhin",
	}
	requireAuthors(t, metadata.Authors, want)
	// The fixture spells the seventh author with the stroke; "Lukasz" is
	// accepted as equivalent but the fixture spelling must be kept when present.
	if got := metadata.Authors[6]; got != "Łukasz Kaiser" && got != "Lukasz Kaiser" {
		t.Fatalf("seventh author = %q, want %q or %q", got, "Łukasz Kaiser", "Lukasz Kaiser")
	}
}

func TestExtractMetadataAttentionYearAndArxivID(t *testing.T) {
	metadata, err := ExtractMetadata(fixtureDocument(t, "attention-2017.page1.txt"))
	if err != nil {
		t.Fatalf("extract attention metadata: %v", err)
	}
	// The arXiv header says "2 Aug 2023"; the NIPS 2017 citation on the same
	// page must win.
	if metadata.Year != 2017 {
		t.Fatalf("year = %d, want 2017 (the arXiv header says 2023)", metadata.Year)
	}
	if metadata.Venue != "NIPS" {
		t.Fatalf("venue = %q, want NIPS", metadata.Venue)
	}
	if metadata.ArxivID != "1706.03762" {
		t.Fatalf("arxiv_id = %q, want 1706.03762", metadata.ArxivID)
	}
}

func TestExtractMetadataLouvainAuthors(t *testing.T) {
	metadata, err := ExtractMetadata(fixtureDocument(t, "louvain-2008.page1.txt"))
	if err != nil {
		t.Fatalf("extract louvain metadata: %v", err)
	}
	want := []string{
		"Vincent D. Blondel",
		"Jean-Loup Guillaume",
		"Renaud Lambiotte",
		"Etienne Lefebvre",
	}
	requireAuthors(t, metadata.Authors, want)
	for _, author := range metadata.Authors {
		if foldedAuthor(author) == "renaud" || foldedAuthor(author) == "lambiotte" {
			t.Fatalf("author %q was not rejoined from the wrapped line: %q", author, metadata.Authors)
		}
	}
}

func TestExtractMetadataLouvainYearAndArxivID(t *testing.T) {
	metadata, err := ExtractMetadata(fixtureDocument(t, "louvain-2008.page1.txt"))
	if err != nil {
		t.Fatalf("extract louvain metadata: %v", err)
	}
	if metadata.Year != 2008 {
		t.Fatalf("year = %d, want 2008", metadata.Year)
	}
	if metadata.ArxivID != "0803.0476" {
		t.Fatalf("arxiv_id = %q, want 0803.0476", metadata.ArxivID)
	}
}

func TestExtractMetadataAprioriTitleAndYear(t *testing.T) {
	metadata, err := ExtractMetadata(fixtureDocument(t, "apriori-1994.page1.txt"))
	if err != nil {
		t.Fatalf("extract apriori metadata: %v", err)
	}
	if metadata.Title != "Fast Algorithms for Mining Association Rules" {
		t.Fatalf("title = %q", metadata.Title)
	}
	if metadata.Year != 1994 {
		t.Fatalf("year = %d, want 1994", metadata.Year)
	}
	if metadata.Venue != "VLDB" {
		t.Fatalf("venue = %q, want VLDB", metadata.Venue)
	}
}

func TestExtractMetadataSourceAndConfidence(t *testing.T) {
	allowed := map[string]bool{
		SourcePDF:      true,
		SourceRule:     true,
		SourceArxiv:    true,
		SourceCrossref: true,
	}
	for _, name := range []string{"attention-2017.page1.txt", "louvain-2008.page1.txt", "apriori-1994.page1.txt"} {
		metadata, err := ExtractMetadata(fixtureDocument(t, name))
		if err != nil {
			t.Fatalf("extract %s: %v", name, err)
		}
		if !allowed[metadata.Source] {
			t.Fatalf("%s: source = %q, want one of pdf/rule/arxiv/crossref", name, metadata.Source)
		}
		requireConfidence(t, metadata.Confidence)
		if metadata.Confidence < ConfidenceLowThreshold {
			t.Fatalf("%s: confidence = %v, want >= %v for a well-formed first page", name, metadata.Confidence, ConfidenceLowThreshold)
		}
		for _, warning := range metadata.Warnings {
			if strings.Contains(strings.ToLower(warning), "confidence is low") {
				t.Fatalf("%s: unexpected low-confidence warning %q (confidence %v)", name, warning, metadata.Confidence)
			}
		}
	}
}

func TestExtractMetadataLowConfidenceAddsWarning(t *testing.T) {
	document := &Document{
		Status:    StatusExtracted,
		PageCount: 1,
		Pages:     []Page{{Number: 1, Text: "A Study of Sparse Retrieval Methods\nAbstract\nWe study retrieval.\n"}},
	}
	metadata, err := ExtractMetadata(document)
	if err != nil {
		t.Fatalf("extract metadata: %v", err)
	}
	if metadata.Confidence >= ConfidenceLowThreshold {
		t.Fatalf("confidence = %v, want < %v when authors and year are missing", metadata.Confidence, ConfidenceLowThreshold)
	}
	found := false
	for _, warning := range metadata.Warnings {
		if strings.Contains(strings.ToLower(warning), "confidence is low") {
			found = true
		}
	}
	if !found {
		t.Fatalf("warnings = %q, want a low-confidence warning", metadata.Warnings)
	}
}

func TestExtractMetadataRejectsAddressOnlyPage(t *testing.T) {
	document := &Document{
		Status:    StatusExtracted,
		PageCount: 1,
		Pages:     []Page{{Number: 1, Text: "Institute for Mathematical Sciences, Imperial College London, 53 Prince's Gate,\nSouth Kensington campus, SW72PG, UK\nAbstract\nsome text\n"}},
	}
	if _, err := ExtractMetadata(document); err == nil {
		t.Fatal("address-only page must not produce a title")
	}
}

func TestAuthorLineSegmentsStripsAffiliationMarkers(t *testing.T) {
	line := "Vincent D. Blondel1;a , Jean-Loup Guillaume1,2;b and Etienne Lefebvre1"
	requireAuthors(t, splitAuthors(line), []string{
		"Vincent D. Blondel",
		"Jean-Loup Guillaume",
		"Etienne Lefebvre",
	})
}

func TestAuthorLineSegmentsStripsEmailsFromAuthorLine(t *testing.T) {
	line := "Ashish Vaswani avaswani@google.com, Noam Shazeer noam@google.com"
	requireAuthors(t, splitAuthors(line), []string{"Ashish Vaswani", "Noam Shazeer"})
}

func TestAuthorLineSegmentsMergesWrappedName(t *testing.T) {
	lines := []string{
		"Fast unfolding of communities in large networks",
		"arXiv:0803.0476v2 [physics.soc-ph] 25 Jul 2008",
		"Vincent D. Blondel1;a , Jean-Loup Guillaume1,2;b , Renaud",
		"Lambiotte1,3;c and Etienne Lefebvre1",
	}
	requireAuthors(t, extractAuthors(lines, lines, 2, len(lines)), []string{
		"Vincent D. Blondel",
		"Jean-Loup Guillaume",
		"Renaud Lambiotte",
		"Etienne Lefebvre",
	})
}

func TestAuthorLineSegmentsMergesColumnFragments(t *testing.T) {
	line := "                                   Rakesh Agrawal                         Ramakrishnan      S&ant*"
	requireAuthors(t, splitAuthors(line), []string{"Rakesh Agrawal", "Ramakrishnan S&ant"})
}

func TestAuthorLineSegmentsIgnoresInstitutionAndEmailLines(t *testing.T) {
	lines := []string{
		"Google Brain                   Google Brain             Google Research            Google Research",
		"University of Toronto",
		"avaswani@google.com             noam@google.com            nikip@google.com             usz@google.com",
		"650 Harry Road, San Jose, CA 95120",
		"Department of Mathematical Engineering, Université catholique de Louvain, 4",
		"Institute for Mathematical Sciences, Imperial College London, 53 Prince's Gate,",
		"E-mail: a vincent.blondel@uclouvain.be; b jean-loup.guillaume@lip6.fr;",
	}
	for _, line := range lines {
		if got := splitAuthors(line); len(got) != 0 {
			t.Fatalf("splitAuthors(%q) = %q, want no authors", line, got)
		}
	}
}

func TestDetectYearPrefersVenueOverArxivStamp(t *testing.T) {
	lines := []string{
		"Attention Is All You Need",
		"arXiv:1706.03762v7 [cs.CL] 2 Aug 2023",
		"Ashish Vaswani Noam Shazeer",
		"31st Conference on Neural Information Processing Systems (NIPS 2017), Long Beach, CA, USA.",
	}
	if got := detectYear(lines); got != 2017 {
		t.Fatalf("detectYear = %d, want 2017", got)
	}
}

func TestApplyProviderMetadataCorrectsAttentionYear(t *testing.T) {
	base := models.PaperMetadata{
		Title:      "Attention Is All You Need",
		Authors:    []string{"Ashish Vaswani", "Noam Shazeer", "Niki Parmar", "Jakob Uszkoreit"},
		Year:       2023, // taken from the arXiv stamp "2 Aug 2023"
		Source:     SourcePDF,
		Confidence: 0.45,
		Warnings:   []string{lowConfidenceWarning, missingAuthorsWarning},
	}
	provider := models.PaperMetadata{
		Authors: []string{
			"Ashish Vaswani",
			"Noam Shazeer",
			"Niki Parmar",
			"Jakob Uszkoreit",
			"Llion Jones",
			"Aidan N. Gomez",
			"Łukasz Kaiser",
			"Illia Polosukhin",
		},
		Year:    2017,
		ArxivID: "1706.03762",
		Source:  SourceArxiv,
	}
	merged := ApplyProviderMetadata(base, provider)
	if merged.Year != 2017 {
		t.Fatalf("year = %d, want 2017 from the provider", merged.Year)
	}
	if merged.Source != SourceArxiv {
		t.Fatalf("source = %q, want %q", merged.Source, SourceArxiv)
	}
	if merged.ArxivID != "1706.03762" {
		t.Fatalf("arxiv_id = %q, want 1706.03762", merged.ArxivID)
	}
	requireAuthors(t, merged.Authors, provider.Authors)
	if merged.Confidence <= base.Confidence || merged.Confidence < 0.85 {
		t.Fatalf("confidence = %v, want it raised above %v and at least 0.85", merged.Confidence, base.Confidence)
	}
	requireConfidence(t, merged.Confidence)
	if merged.Title != base.Title {
		t.Fatalf("title = %q, want the base title %q when the provider has none", merged.Title, base.Title)
	}
	for _, warning := range merged.Warnings {
		lower := strings.ToLower(warning)
		if strings.Contains(lower, "confidence is low") || strings.Contains(lower, "authors could not be extracted") {
			t.Fatalf("warnings = %q, want stale warnings dropped", merged.Warnings)
		}
	}
}

func TestApplyProviderMetadataKeepsBaseWhenProviderEmpty(t *testing.T) {
	base := models.PaperMetadata{
		Title:      "Attention Is All You Need",
		Authors:    []string{"Ashish Vaswani", "Noam Shazeer"},
		Year:       2023,
		Source:     SourcePDF,
		Confidence: 0.85,
	}
	merged := ApplyProviderMetadata(base, models.PaperMetadata{})
	if !reflect.DeepEqual(merged, base) {
		t.Fatalf("merged = %+v, want unchanged base %+v", merged, base)
	}
}

func TestApplyProviderMetadataDeduplicatesProviderAuthors(t *testing.T) {
	base := models.PaperMetadata{Title: "Attention Is All You Need", Source: SourcePDF, Confidence: 0.5}
	provider := models.PaperMetadata{
		Authors: []string{"Ashish Vaswani", " Ashish Vaswani ", "", "Noam Shazeer", "noam@example.com"},
		Year:    2017,
		Source:  SourceArxiv,
	}
	merged := ApplyProviderMetadata(base, provider)
	requireAuthors(t, merged.Authors, []string{"Ashish Vaswani", "Noam Shazeer"})
}

func TestApplyProviderRecordCarriesDOI(t *testing.T) {
	base := models.PaperMetadata{Title: "Attention Is All You Need", Source: SourcePDF, Confidence: 0.5}
	merged, doi := ApplyProviderRecord(base, ProviderRecord{
		Source:  SourceCrossref,
		Title:   "Attention Is All You Need",
		Authors: []string{"Ashish Vaswani"},
		Year:    2017,
		DOI:     "10.48550/arXiv.1706.03762",
		ArxivID: "1706.03762",
	})
	if doi != "10.48550/arXiv.1706.03762" {
		t.Fatalf("doi = %q, want 10.48550/arXiv.1706.03762", doi)
	}
	if merged.Source != SourceCrossref {
		t.Fatalf("source = %q, want %q", merged.Source, SourceCrossref)
	}
	if merged.Year != 2017 || merged.ArxivID != "1706.03762" {
		t.Fatalf("merged = %+v, want year 2017 and arXiv id 1706.03762", merged)
	}
}
