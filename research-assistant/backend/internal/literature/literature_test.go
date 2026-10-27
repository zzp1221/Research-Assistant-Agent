package literature

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const arxivSearchXML = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <entry>
    <id>http://arxiv.org/abs/1706.03762v7</id>
    <title>Attention Is All You Need</title>
    <summary>The Transformer uses attention.</summary>
    <published>2017-06-12T17:57:34Z</published>
    <author><name>Ashish Vaswani</name></author>
    <author><name>Noam Shazeer</name></author>
    <link href="http://arxiv.org/abs/1706.03762v7" rel="alternate" type="text/html"/>
    <link href="PDF_URL" rel="related" title="pdf" type="application/pdf"/>
  </entry>
</feed>`

const openAlexJSON = `{"results":[{"id":"https://openalex.org/W1","title":"Transformer Retrieval","publication_year":2018,"doi":"https://doi.org/10.1/abc","cited_by_count":42,"authorships":[{"author":{"display_name":"Jane Doe"}}],"primary_location":{"landing_page_url":"https://example.org/work","pdf_url":"PDF_URL"}}]}`

const crossrefSearchJSON = `{"message":{"items":[{"DOI":"10.1/abc","title":["Transformer Retrieval"],"container-title":["NeurIPS"],"URL":"https://doi.org/10.1/abc","author":[{"given":"Jane","family":"Doe"}],"issued":{"date-parts":[[2018]]},"link":[{"URL":"PDF_URL","content-type":"application/pdf"}]}]}}`

const crossrefWorkJSON = `{"message":{"DOI":"10.1234/demo","title":["A DOI Paper"],"container-title":["Journal"],"URL":"https://doi.org/10.1234/demo","author":[{"given":"Ada","family":"Lovelace"}],"issued":{"date-parts":[[2019]]},"link":[{"URL":"PDF_URL","content-type":"application/pdf"}]}}`

const githubJSON = `{"items":[{"full_name":"example/bm25","html_url":"https://github.com/example/bm25","description":"BM25 implementation","stargazers_count":99,"language":"Python"}]}`

func newLiteratureServer(t *testing.T, pdfBody []byte) (*httptest.Server, *int32) {
	t.Helper()
	var counter int32
	mux := http.NewServeMux()
	mux.HandleFunc("/arxiv", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&counter, 1)
		w.Header().Set("Content-Type", "application/atom+xml")
		body := strings.ReplaceAll(arxivSearchXML, "PDF_URL", "http://"+r.Host+"/pdf")
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/openalex", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&counter, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.ReplaceAll(openAlexJSON, "PDF_URL", "http://"+r.Host+"/pdf")))
	})
	mux.HandleFunc("/crossref/", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&counter, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.ReplaceAll(crossrefWorkJSON, "PDF_URL", "http://"+r.Host+"/pdf")))
	})
	mux.HandleFunc("/crossref", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&counter, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.ReplaceAll(crossrefSearchJSON, "PDF_URL", "http://"+r.Host+"/pdf")))
	})
	mux.HandleFunc("/github", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&counter, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(githubJSON))
	})
	mux.HandleFunc("/pdf", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&counter, 1)
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write(pdfBody)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &counter
}

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client := NewClient(Config{CacheDir: t.TempDir(), Timeout: defaultTimeout})
	client.WithEndpoint(SourceArxiv, server.URL+"/arxiv")
	client.WithEndpoint(SourceOpenAlex, server.URL+"/openalex")
	client.WithEndpoint(SourceCrossref, server.URL+"/crossref")
	client.WithEndpoint(SourceGitHub, server.URL+"/github")
	return client
}

func TestSearchAggregatesAndDeduplicatesProviders(t *testing.T) {
	server, counter := newLiteratureServer(t, []byte("%PDF-1.4 test"))
	client := newTestClient(t, server)
	results, err := client.Search(context.Background(), "transformer", SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 {
		t.Fatalf("results=%#v", results)
	}
	if atomic.LoadInt32(counter) < 4 {
		t.Fatalf("expected all providers to be queried, counter=%d", *counter)
	}
	sources := map[string]bool{}
	doiCount := 0
	for _, result := range results {
		sources[result.Source] = true
		if result.DOI == "10.1/abc" {
			doiCount++
		}
	}
	for _, source := range []string{SourceArxiv, SourceOpenAlex, SourceGitHub} {
		if !sources[source] {
			t.Fatalf("missing source %s in %#v", source, results)
		}
	}
	// OpenAlex and Crossref return the same DOI; dedupe keeps one record.
	if doiCount != 1 {
		t.Fatalf("expected one deduplicated DOI record, got %d: %#v", doiCount, results)
	}
}

func TestSearchUsesCache(t *testing.T) {
	server, counter := newLiteratureServer(t, []byte("%PDF-1.4 test"))
	client := newTestClient(t, server)
	if _, err := client.Search(context.Background(), "cache me", SearchOptions{Sources: []string{SourceGitHub}}); err != nil {
		t.Fatal(err)
	}
	first := atomic.LoadInt32(counter)
	if _, err := client.Search(context.Background(), "cache me", SearchOptions{Sources: []string{SourceGitHub}}); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(counter) != first {
		t.Fatalf("cache miss: requests %d -> %d", first, atomic.LoadInt32(counter))
	}
}

func TestImportArxivReturnsPDFAndMetadata(t *testing.T) {
	pdf := []byte("%PDF-1.4 arxiv pdf")
	server, _ := newLiteratureServer(t, pdf)
	client := newTestClient(t, server)
	imported, err := client.Import(context.Background(), "https://arxiv.org/abs/1706.03762")
	if err != nil {
		t.Fatal(err)
	}
	if imported.Title != "Attention Is All You Need" || imported.ArxivID != "1706.03762" {
		t.Fatalf("metadata=%#v", imported.Result)
	}
	if imported.Year != 2017 || len(imported.Authors) != 2 {
		t.Fatalf("year=%d authors=%#v", imported.Year, imported.Authors)
	}
	if string(imported.PDF) != string(pdf) {
		t.Fatalf("pdf=%q", string(imported.PDF))
	}
}

func TestImportDOIRequiresOpenAccessPDF(t *testing.T) {
	server, _ := newLiteratureServer(t, []byte("%PDF-1.4 doi pdf"))
	client := newTestClient(t, server)
	imported, err := client.Import(context.Background(), "10.1234/demo")
	if err != nil {
		t.Fatal(err)
	}
	if imported.Title != "A DOI Paper" || imported.DOI != "10.1234/demo" {
		t.Fatalf("metadata=%#v", imported.Result)
	}
	if string(imported.PDF) != "%PDF-1.4 doi pdf" {
		t.Fatalf("pdf=%q", string(imported.PDF))
	}
}

func TestImportRejectsOversizedAndInvalidPDF(t *testing.T) {
	server, _ := newLiteratureServer(t, []byte("this is not a pdf at all"))
	client := newTestClient(t, server)
	if _, err := client.Import(context.Background(), "https://arxiv.org/abs/1706.03762"); err == nil || !IsCode(err, CodeInvalidPDF) {
		t.Fatalf("expected invalid_pdf, got %v", err)
	}

	bigServer, _ := newLiteratureServer(t, []byte("%PDF-1.4 "+strings.Repeat("x", 4096)))
	bigClient := newTestClient(t, bigServer).WithMaxPDFBytes(16)
	if _, err := bigClient.Import(context.Background(), "https://arxiv.org/abs/1706.03762"); err == nil || !IsCode(err, CodePDFTooLarge) {
		t.Fatalf("expected pdf_too_large, got %v", err)
	}
}

func TestInvalidReferences(t *testing.T) {
	client := NewClient(Config{})
	for _, ref := range []string{"", "not a reference", "ftp://example.com/x.pdf", "https://example.com/page"} {
		if _, err := client.Import(context.Background(), ref); err == nil {
			t.Fatalf("expected %q to be rejected", ref)
		}
	}
	if _, err := client.Import(context.Background(), "https://example.com/paper.pdf"); err == nil {
		t.Fatal("direct PDF import without a reachable server must fail")
	}
}

func TestProviderFailureIsWarningWhenAnotherSourceWorks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/arxiv", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	mux.HandleFunc("/github", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(githubJSON))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client := NewClient(Config{CacheDir: t.TempDir()})
	client.WithEndpoint(SourceArxiv, server.URL+"/arxiv")
	client.WithEndpoint(SourceGitHub, server.URL+"/github")
	results, warnings, err := client.SearchWithWarnings(context.Background(), "anything", SearchOptions{Sources: []string{SourceArxiv, SourceGitHub}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(warnings) != 1 || warnings[0].Source != SourceArxiv {
		t.Fatalf("results=%#v warnings=%#v", results, warnings)
	}
}

func TestAllSourcesFailureReturnsStructuredError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	client := NewClient(Config{})
	client.WithEndpoint(SourceArxiv, server.URL)
	_, err := client.Search(context.Background(), "query", SearchOptions{Sources: []string{SourceArxiv}})
	if err == nil || !errors.Is(err, ErrAllSourcesFailed) {
		t.Fatalf("expected all sources failure, got %v", err)
	}
}
