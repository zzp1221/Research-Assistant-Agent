package scheduler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
)

func TestBuildRepoDiscoveryQuery_PrefersStructuredInputs(t *testing.T) {
	task := &models.Task{
		Description: "任务目标: 检索并定位论文对应的高可信公开仓库",
		Inputs: map[string]any{
			"paper_title":        "Attention Is All You Need",
			"paper_search_query": "Transformer",
			"parsed_paper":       "论文标题：Some Other Paper",
		},
	}

	query := buildRepoDiscoveryQuery(task)
	if query != "Attention Is All You Need" {
		t.Fatalf("expected structured paper_title to win, got %q", query)
	}
}

func TestCuratedRepoFallbackCandidates_AttentionPaper(t *testing.T) {
	candidates := curatedRepoFallbackCandidates("Attention Is All You Need")
	if len(candidates) == 0 {
		t.Fatalf("expected curated fallback candidate")
	}
	if candidates[0].RepoURLs[0] != "https://github.com/harvardnlp/annotated-transformer" {
		t.Fatalf("unexpected curated repo: %+v", candidates[0])
	}

	selected, reason := selectRepo("Attention Is All You Need", candidates)
	if selected != "https://github.com/harvardnlp/annotated-transformer" {
		t.Fatalf("selectRepo=%q reason=%q", selected, reason)
	}
	report := buildRepoDiscoveryReport("Attention Is All You Need", candidates, selected, reason, true, "context deadline exceeded")
	var payload map[string]any
	if err := json.Unmarshal([]byte(report), &payload); err != nil {
		t.Fatalf("repo validation report must be JSON: %v\n%s", err, report)
	}
	if payload["selected"] != selected || !strings.Contains(report, "context deadline exceeded") {
		t.Fatalf("unexpected report: %s", report)
	}
}

func TestSelectRepoRejectsUnrelatedHuggingFaceCandidates(t *testing.T) {
	// Reproduces the deadlock: HF returns unrelated repos, the old code selected
	// the first one and then rejected it, so the GitHub fallback never ran.
	candidates := []repoCandidate{
		{
			PaperID:   "2301.13187",
			Title:     "Weighted Flow Diffusion for Local Graph Clustering",
			RepoURLs:  []string{"https://github.com/s-h-yang/wfd"},
			Source:    "papers_with_code(hf)",
			ScoreHint: 3,
		},
		{
			PaperID:   "1910.09483",
			Title:     "Sampling random graph homomorphisms",
			RepoURLs:  []string{"https://github.com/HanbaekLyu/motif_sampling"},
			Source:    "papers_with_code(hf)",
			ScoreHint: 3,
		},
	}
	if selected, reason := selectRepo("Fast unfolding of communities in large networks", candidates); selected != "" {
		t.Fatalf("unrelated candidates must not be selected: %q (%s)", selected, reason)
	}
	githubCandidates := []repoCandidate{{
		Title:       "Fast unfolding of communities in large networks",
		RepoName:    "Woodstock-z/Louvain",
		Description: "Louvain（Fast unfolding of communities in large networks）",
		RepoURLs:    []string{"https://github.com/Woodstock-z/Louvain"},
		Source:      "github_search",
		Stars:       120,
	}}
	selected, reason := selectRepo("Fast unfolding of communities in large networks", append(candidates, githubCandidates...))
	if selected != "https://github.com/Woodstock-z/Louvain" {
		t.Fatalf("github fallback candidate should win: %q (%s)", selected, reason)
	}
}

func TestPreferredRepositoryInputIsValidGitHubURL(t *testing.T) {
	preferred := normalizeGitHubRepoURL("https://github.com/harvardnlp/annotated-transformer.git")
	if githubURLRe.FindString(preferred) != preferred {
		t.Fatalf("preferred repository was not accepted: %q", preferred)
	}
}

func TestRepoDiscoveryUsesPreferredRepositoryWithoutPaperSearch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	task := &models.Task{Inputs: map[string]any{
		"preferred_repo_url": "https://github.com/example/research-repo",
	}}
	if err := executeRepoDiscovery(ctx, task); err != nil {
		t.Fatal(err)
	}
	artifacts, ok := task.Metadata["artifact_values"].(map[string]any)
	if !ok || artifacts["repo_url"] != "https://github.com/example/research-repo" {
		t.Fatalf("unexpected preferred repository artifacts: %#v", task.Metadata)
	}
	if task.Status != models.StatusCompleted || task.Result != "https://github.com/example/research-repo" {
		t.Fatalf("preferred repository was not selected: %#v", task)
	}
}

func TestRepoDiscoveryWithoutCandidatesIsBlocked(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	task := &models.Task{
		Description: "复现一篇没有公开实现的小众论文",
		Inputs:      map[string]any{"paper_title": "An Untraceable Method Nobody Published"},
	}
	if err := executeRepoDiscovery(ctx, task); err != nil {
		t.Fatal(err)
	}
	if task.Status != models.StatusBlocked {
		t.Fatalf("expected blocked status, got %s (%s)", task.Status, task.Error)
	}
	artifacts := task.Metadata["artifact_values"].(map[string]any)
	if _, hasEmptyURL := artifacts["repo_url"]; hasEmptyURL {
		t.Fatalf("blocked discovery must not write an empty repo_url artifact: %#v", artifacts)
	}
	missingJSON, _ := artifacts["missing_requirements"].(string)
	if !strings.Contains(missingJSON, "repo_url") || !strings.Contains(missingJSON, "GitHub") {
		t.Fatalf("missing_requirements must be actionable: %s", missingJSON)
	}
}

func TestBuildRepoDiscoveryQueryUsesVersionedArxivID(t *testing.T) {
	task := &models.Task{Inputs: map[string]any{
		"parsed_paper": "arXiv:0803.0476v2 [physics.soc-ph] 25 Jul 2008\nFast unfolding of communities in large networks",
	}}
	if query := buildRepoDiscoveryQuery(task); query != "0803.0476" {
		t.Fatalf("versioned arXiv id not normalized: %q", query)
	}
}
