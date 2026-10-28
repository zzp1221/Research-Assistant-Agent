package literature

import "testing"

func TestLibraryFindByDOIAndArxiv(t *testing.T) {
	store := NewLibraryStore(t.TempDir())
	record, _, err := store.UpsertResult("alice", Result{
		Source: SourceCrossref, DOI: "10.1234/Find", ArxivID: "2101.99999", Title: "Findable",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	byDOI, found, err := store.FindByDOI("alice", "https://doi.org/10.1234/find")
	if err != nil || !found || byDOI.ID != record.ID {
		t.Fatalf("byDOI=%#v found=%v err=%v", byDOI, found, err)
	}
	byArxiv, found, err := store.FindByArxiv("alice", "2101.99999")
	if err != nil || !found || byArxiv.ID != record.ID {
		t.Fatalf("byArxiv=%#v found=%v err=%v", byArxiv, found, err)
	}
	if _, found, _ := store.FindByDOI("bob", "10.1234/find"); found {
		t.Fatal("owner isolation failed for FindByDOI")
	}
}
