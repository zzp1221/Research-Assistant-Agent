package literature

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRelevanceRankerDoesNotGroupByProviderAndRewardsSurveyCitations(t *testing.T) {
	results := []Result{
		{Source: SourceOpenAlex, Title: "Human pose estimation based on graph neural network: survey", Year: 2026, Citations: 1, ProviderRank: 1},
		{Source: SourceOpenAlex, Title: "Graph neural network for traffic forecasting: A survey", Year: 2022, Citations: 1335, ProviderRank: 3},
		{Source: SourceOpenAlex, Title: "A Comprehensive Survey on Graph Neural Networks", Year: 2020, Citations: 10971, ProviderRank: 9},
		{Source: SourceCrossref, Title: "Survey for Exploring Blockchain with Graph Neural Network", Year: 2024, ProviderRank: 1},
		{Source: SourceCrossref, Title: "A survey of sentiment analysis methods based on graph neural network", Year: 2023, ProviderRank: 3},
	}
	applyRanking(results, "graph neural network survey", []string{SourceArxiv, SourceOpenAlex, SourceCrossref}, "relevance")
	if results[0].Source != SourceOpenAlex || !strings.Contains(strings.ToLower(results[0].Title), "comprehensive survey") {
		t.Fatalf("expected the comprehensive survey first, got %#v", results[0])
	}
	for index, result := range results {
		if result.Source == SourceOpenAlex && strings.Contains(result.Title, "Comprehensive") && index > 2 {
			t.Fatalf("strong OpenAlex result ranked too low: index=%d results=%#v", index, results)
		}
	}
}

func TestRelevanceRankerPrefersTitleMatches(t *testing.T) {
	results := []Result{
		{Source: SourceCrossref, Title: "Unrelated topic in bibliometrics", Abstract: "graph neural network is mentioned once", Citations: 500},
		{Source: SourceCrossref, Title: "Graph neural network survey", Abstract: "", Citations: 0},
	}
	applyRanking(results, "graph neural network survey", []string{SourceCrossref}, "relevance")
	if !strings.Contains(strings.ToLower(results[0].Title), "graph neural network survey") {
		t.Fatalf("title match should win: %#v", results)
	}
}

func TestSearchKeepsResultsWhenOneProviderTimesOut(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write([]byte("[]"))
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"full_name":"example/repo","html_url":"https://github.com/example/repo","description":"x","stargazers_count":1}]}`))
	}))
	defer fast.Close()
	client := NewClient(Config{CacheDir: t.TempDir(), ProviderTimeout: 80 * time.Millisecond})
	client.WithEndpoint(SourceArxiv, slow.URL)
	client.WithEndpoint(SourceGitHub, fast.URL)
	results, warnings, err := client.SearchWithWarnings(context.Background(), "x", SearchOptions{Sources: []string{SourceArxiv, SourceGitHub}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(warnings) == 0 {
		t.Fatalf("results=%#v warnings=%#v", results, warnings)
	}
}
func TestSearchFetchesWiderCandidatePoolThanRequestedLimit(t *testing.T) {
	var gotPerPage string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPerPage = r.URL.Query().Get("per-page")
		_, _ = w.Write([]byte(`{"results":[{"id":"https://openalex.org/W1","title":"Graph neural network survey","publication_year":2020,"cited_by_count":10}]}`))
	}))
	defer server.Close()
	client := NewClient(Config{CacheDir: t.TempDir()})
	client.WithEndpoint(SourceOpenAlex, server.URL)
	if _, err := client.Search(context.Background(), "graph neural network survey", SearchOptions{Sources: []string{SourceOpenAlex}, Limit: 5}); err != nil {
		t.Fatal(err)
	}
	if gotPerPage != "15" {
		t.Fatalf("per-page=%q, want 15 (limit*3 candidate pool)", gotPerPage)
	}
}
