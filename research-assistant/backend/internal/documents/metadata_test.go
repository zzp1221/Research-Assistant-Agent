package documents

import (
	"os"
	"strings"
	"testing"
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
}

func TestExtractMetadataLouvainFixture(t *testing.T) {
	metadata, err := ExtractMetadata(fixtureDocument(t, "louvain-2008.page1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "Fast unfolding of communities in large networks" {
		t.Fatalf("title=%q", metadata.Title)
	}
	joined := strings.Join(metadata.Authors, " | ")
	for _, want := range []string{"Blondel", "Guillaume", "Lambiotte", "Lefebvre"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("authors=%q missing %q", joined, want)
		}
	}
	if metadata.ArxivID != "0803.0476" {
		t.Fatalf("arxiv_id=%q, want 0803.0476", metadata.ArxivID)
	}
}

func TestExtractMetadataAttentionFixture(t *testing.T) {
	metadata, err := ExtractMetadata(fixtureDocument(t, "attention-2017.page1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "Attention Is All You Need" {
		t.Fatalf("title=%q", metadata.Title)
	}
	if len(metadata.Authors) == 0 || metadata.Authors[0] != "Ashish Vaswani" {
		t.Fatalf("authors=%q, want first author Ashish Vaswani", metadata.Authors)
	}
	if metadata.ArxivID != "1706.03762" {
		t.Fatalf("arxiv_id=%q", metadata.ArxivID)
	}
}

func TestExtractMetadataAprioriFixture(t *testing.T) {
	metadata, err := ExtractMetadata(fixtureDocument(t, "apriori-1994.page1.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Title != "Fast Algorithms for Mining Association Rules" {
		t.Fatalf("title=%q", metadata.Title)
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
