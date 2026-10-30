package literature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSemanticScholarSearchParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "query=gnn") {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"data":[{"paperId":"abc123","title":"Graph Neural Networks","abstract":"A survey.","year":2021,"venue":"NeurIPS","url":"https://example.org/p","citationCount":123,"externalIds":{"DOI":"10.1234/gnn","ArXiv":"2101.00001"},"openAccessPdf":{"url":"https://example.org/p.pdf","license":"CC-BY"},"authors":[{"name":"Ada Lovelace"}]}]}`))
	}))
	defer server.Close()
	client := NewClient(Config{CacheDir: t.TempDir()})
	client.WithEndpoint(SourceSemanticScholar, server.URL+"/s2")
	results, err := client.Search(context.Background(), "gnn", SearchOptions{Sources: []string{SourceSemanticScholar}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results=%#v", results)
	}
	result := results[0]
	if result.Source != SourceSemanticScholar || result.Title != "Graph Neural Networks" {
		t.Fatalf("result=%#v", result)
	}
	if result.DOI != "10.1234/gnn" || result.ArxivID != "2101.00001" || result.Citations != 123 {
		t.Fatalf("metadata=%#v", result)
	}
	if !result.OpenAccess || result.License != "CC-BY" || result.PDFURL != "https://example.org/p.pdf" {
		t.Fatalf("open access=%#v", result)
	}
}

func TestPubMedSearchParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "esearch"):
			_, _ = w.Write([]byte(`{"esearchresult":{"idlist":["111","222"]}}`))
		case strings.Contains(r.URL.Path, "esummary"):
			_, _ = w.Write([]byte(`{"result":{"uids":["111","222"],"111":{"title":"A Medical Paper","pubdate":"2020 Mar 15","fulljournalname":"The Lancet","authors":[{"name":"Jane Doe"}],"articleids":[{"idtype":"doi","value":"10.9999/med"}]},"222":{"title":"Another Paper","pubdate":"2019","fulljournalname":"BMJ","authors":[]}}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Config{CacheDir: t.TempDir()})
	client.WithEndpoint(SourcePubMed, server.URL+"/pubmed/esearch")
	results, err := client.Search(context.Background(), "covid", SearchOptions{Sources: []string{SourcePubMed}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results=%#v", results)
	}
	if results[0].DOI != "10.9999/med" || results[0].Year != 2020 || results[0].Venue != "The Lancet" {
		t.Fatalf("first=%#v", results[0])
	}
	if results[0].URL != "https://pubmed.ncbi.nlm.nih.gov/111/" {
		t.Fatalf("url=%q", results[0].URL)
	}
}

func TestDBLPSearchParsing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":{"hits":{"hit":[{"info":{"title":"Database Systems","authors":{"author":[{"text":"Alice"},{"text":"Bob"}]},"year":"2018","venue":"SIGMOD","doi":"10.1/db","ee":"https://doi.org/10.1/db","key":"conf/sigmod/x"}}]}}}`))
	}))
	defer server.Close()
	client := NewClient(Config{CacheDir: t.TempDir()})
	client.WithEndpoint(SourceDBLP, server.URL+"/dblp")
	results, err := client.Search(context.Background(), "database", SearchOptions{Sources: []string{SourceDBLP}, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Venue != "SIGMOD" || len(results[0].Authors) != 2 {
		t.Fatalf("results=%#v", results)
	}
	if results[0].DOI != "10.1/db" || results[0].Year != 2018 {
		t.Fatalf("result=%#v", results[0])
	}
}

func TestSearchRetriesOn429WithRetryAfter(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"full_name":"example/repo","html_url":"https://github.com/example/repo","description":"x","stargazers_count":1}]}`))
	}))
	defer server.Close()
	client := NewClient(Config{CacheDir: t.TempDir(), MaxRetries: 2, RetryBaseDelay: time.Millisecond})
	client.WithEndpoint(SourceGitHub, server.URL+"/github")
	results, err := client.Search(context.Background(), "x", SearchOptions{Sources: []string{SourceGitHub}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("results=%#v attempts=%d", results, attempts)
	}
}

func TestSearchRateLimitExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "0")
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer server.Close()
	client := NewClient(Config{CacheDir: t.TempDir(), MaxRetries: 1, RetryBaseDelay: time.Millisecond})
	client.WithEndpoint(SourceGitHub, server.URL+"/github")
	_, err := client.Search(context.Background(), "x", SearchOptions{Sources: []string{SourceGitHub}})
	if err == nil || !IsCode(err, CodeAllSourcesFailed) || !strings.Contains(err.Error(), "rate_limited") {
		t.Fatalf("expected all_sources_failed containing rate_limited, got %v", err)
	}
}

func TestApplyRankingModes(t *testing.T) {
	results := []Result{
		{Source: SourceArxiv, Title: "A", Year: 2020, Citations: 5},
		{Source: SourceOpenAlex, Title: "B", Year: 2022, Citations: 100},
		{Source: SourceCrossref, Title: "C", Year: 2021, Citations: 50},
	}
	applyRanking(results, "graph neural network", []string{SourceArxiv, SourceOpenAlex, SourceCrossref}, "citations")
	if results[0].Title != "B" || results[0].Rank != 1 || results[0].RankReason != "citations desc" {
		t.Fatalf("citations ranking=%#v", results)
	}
	applyRanking(results, "graph neural network", []string{SourceArxiv, SourceOpenAlex, SourceCrossref}, "year")
	if results[0].Title != "B" || results[0].RankReason != "year desc" {
		t.Fatalf("year ranking=%#v", results)
	}
	applyRanking(results, "graph neural network", []string{SourceArxiv, SourceOpenAlex, SourceCrossref}, "")
	if results[0].RankReason != "lexical relevance + citations + provider rank" {
		t.Fatalf("relevance ranking=%#v", results)
	}
}

func TestLibraryUpsertIsolationAndBibTeX(t *testing.T) {
	store := NewLibraryStore(t.TempDir())
	first := Result{Source: SourceCrossref, DOI: "10.1/abc", Title: "Paper One", Authors: []string{"Ada", "Bob"}, Year: 2020, Venue: "Journal", Citations: 3}
	record, created, err := store.UpsertResult("owner-a", first, "upload-1")
	if err != nil || !created {
		t.Fatalf("create err=%v created=%v", err, created)
	}
	if record.ID == "" || record.UploadID != "upload-1" {
		t.Fatalf("record=%#v", record)
	}
	second := Result{Source: SourceOpenAlex, DOI: "10.1/ABC", Title: "Paper One", Citations: 30, Abstract: "abstract"}
	merged, createdAgain, err := store.UpsertResult("owner-a", second, "")
	if err != nil || createdAgain {
		t.Fatalf("merge err=%v created=%v", err, createdAgain)
	}
	if merged.Citations != 30 || merged.Abstract != "abstract" || merged.UploadID != "upload-1" {
		t.Fatalf("merged=%#v", merged)
	}
	records, err := store.List("owner-a", "paper", "", 10)
	if err != nil || len(records) != 1 {
		t.Fatalf("list=%#v err=%v", records, err)
	}
	if other, _ := store.List("owner-b", "", "", 10); len(other) != 0 {
		t.Fatalf("owner isolation failed: %#v", other)
	}
	tagged, err := store.UpdateTags("owner-a", record.ID, []string{"gnn", "survey", "GNN"})
	if err != nil || len(tagged.Tags) != 2 {
		t.Fatalf("tags=%#v err=%v", tagged, err)
	}
	bib := BibTeX(tagged)
	for _, want := range []string{"@article{", "title = {Paper One}", "author = {Ada and Bob}", "year = {2020}", "doi = {10.1/abc}"} {
		if !strings.Contains(bib, want) {
			t.Fatalf("bibtex missing %q: %s", want, bib)
		}
	}
	deleted, err := store.Delete("owner-a", record.ID)
	if err != nil || !deleted {
		t.Fatalf("delete=%v err=%v", deleted, err)
	}
	if _, found, _ := store.Get("owner-a", record.ID); found {
		t.Fatal("record still present after delete")
	}
}

func TestCitationsGraph(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.RawQuery
		switch {
		case strings.Contains(query, "filter=doi:"):
			_, _ = w.Write([]byte(`{"results":[{"id":"https://openalex.org/W100","title":"Seed Paper","doi":"https://doi.org/10.1/seed","cited_by_count":7,"referenced_works":["https://openalex.org/W200","https://openalex.org/W201"]}]}`))
		case strings.Contains(query, "filter=openalex_id:"):
			_, _ = w.Write([]byte(`{"results":[{"id":"https://openalex.org/W200","title":"Reference A","publication_year":2019,"cited_by_count":2},{"id":"https://openalex.org/W201","title":"Reference B","publication_year":2020,"cited_by_count":1}]}`))
		case strings.Contains(query, "filter=cites:"):
			_, _ = w.Write([]byte(`{"results":[{"id":"https://openalex.org/W300","title":"Citing Paper","publication_year":2023,"cited_by_count":4}]}`))
		default:
			t.Errorf("unexpected query %s", query)
		}
	}))
	defer server.Close()
	client := NewClient(Config{CacheDir: t.TempDir()})
	client.WithEndpoint(SourceOpenAlex, server.URL+"/openalex")
	graph, err := client.Citations(context.Background(), "10.1/seed", 10)
	if err != nil {
		t.Fatal(err)
	}
	if graph.DOI != "10.1/seed" || graph.OpenAlexID != "W100" || graph.CitedByCount != 7 {
		t.Fatalf("graph=%#v", graph)
	}
	if len(graph.References) != 2 || len(graph.CitedBy) != 1 {
		t.Fatalf("refs=%d cited=%d", len(graph.References), len(graph.CitedBy))
	}
	if graph.References[0].Title != "Reference A" || graph.CitedBy[0].Title != "Citing Paper" {
		t.Fatalf("graph=%#v", graph)
	}
}

func TestUnpaywallResolvesDOI(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "unpaywall"):
			_, _ = w.Write([]byte(`{"is_oa":true,"best_oa_location":{"url_for_pdf":"http://` + r.Host + `/pdf","license":"cc-by"}}`))
		case strings.Contains(r.URL.Path, "crossref"):
			_, _ = w.Write([]byte(`{"message":{"DOI":"10.1234/oa","title":["OA Paper"],"URL":"https://doi.org/10.1234/oa","issued":{"date-parts":[[2020]]}}}`))
		case strings.Contains(r.URL.Path, "pdf"):
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.4 test"))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := NewClient(Config{CacheDir: t.TempDir()})
	client.WithEndpoint(SourceCrossref, server.URL+"/crossref")
	client.WithEndpoint(SourceUnpaywall, server.URL+"/unpaywall")
	imported, err := client.Import(context.Background(), "10.1234/oa")
	if err != nil {
		t.Fatal(err)
	}
	if string(imported.PDF) != "%PDF-1.4 test" || !imported.OpenAccess {
		t.Fatalf("imported=%#v", imported)
	}
}
