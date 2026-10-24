package documents

import (
	"testing"
)

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
