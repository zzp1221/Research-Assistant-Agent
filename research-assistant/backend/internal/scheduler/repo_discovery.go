package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"research-assistant-backend/internal/catalog"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/paperid"
	"sort"
	"strings"
	"time"
)

// repo_discovery 节点的真实执行逻辑（不走 LLM），用于：
// 1) 根据论文标题/arXiv ID 在 Papers with Code（当前由 HuggingFace Papers API 承载）中检索论文；
// 2) 抽取其关联的 GitHub 仓库链接作为候选；
// 3) 输出结构化 artifact：candidate_repositories / repo_validation_report / repo_url。
//
// 说明：
// - 目前 paperswithcode.com 的接口在网络侧会 302 跳转到 HuggingFace（/papers/*），所以这里直接调用 HF 的 papers API。
// - 该 API 并非所有论文都带 repo 信息：如果找不到 github 链接，会返回空 repo_url，并在 report 里说明原因。

var (
	arxivIDRe             = paperid.ArxivIDPattern
	githubURLRe           = regexp.MustCompile(`https?://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(?:\.git)?`)
	yearTailRe            = regexp.MustCompile(`\b(19|20)\d{2}\b`)
	minRepoSelectionScore = 50
)

const maxPaperSearchQueryLen = 240

type hfPaperSearchItem struct {
	Paper struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"paper"`
}

type repoCandidate struct {
	PaperID      string   `json:"paper_id,omitempty"`
	Title        string   `json:"title"`
	RepoName     string   `json:"repo_name,omitempty"`
	Description  string   `json:"description,omitempty"`
	RepoURLs     []string `json:"repo_urls"`
	Source       string   `json:"source"`
	ScoreHint    int      `json:"score_hint"`
	Stars        int      `json:"stars,omitempty"`
	FallbackUsed bool     `json:"fallback_used,omitempty"`
}

type githubSearchResponse struct {
	Items []githubRepoItem `json:"items"`
}

type githubRepoItem struct {
	FullName        string `json:"full_name"`
	HTMLURL         string `json:"html_url"`
	Description     string `json:"description"`
	StargazersCount int    `json:"stargazers_count"`
	Archived        bool   `json:"archived"`
	Fork            bool   `json:"fork"`
}

func executeRepoDiscovery(ctx context.Context, runtimeTask *models.Task) error {
	if runtimeTask == nil {
		return fmt.Errorf("runtime task is nil")
	}

	query := buildRepoDiscoveryQuery(runtimeTask)
	preferredRepoURL := normalizeGitHubRepoURL(taskInputValue(runtimeTask, "preferred_repo_url"))
	if preferredRepoURL != "" && githubURLRe.FindString(preferredRepoURL) == preferredRepoURL {
		candidate := repoCandidate{
			Title:       "User-preferred repository",
			RepoName:    strings.TrimPrefix(preferredRepoURL, "https://github.com/"),
			Description: "Repository explicitly requested by the user; cloneability is verified by repo_prepare.",
			RepoURLs:    []string{preferredRepoURL},
			Source:      "user_preference",
			ScoreHint:   1_000_000,
		}
		completeRepoDiscoveryTask(runtimeTask, query, []repoCandidate{candidate}, preferredRepoURL, "source=user_preference", false, "")
		return nil
	}
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("repo_discovery: empty query")
	}

	base := strings.TrimSpace(os.Getenv("PWC_API_BASE_URL"))
	if base == "" {
		// HuggingFace Papers API: /api/papers/search 和 /api/papers/{id}
		base = "https://huggingface.co"
	}

	limit := envInt("PWC_SEARCH_LIMIT", 5, 1, 20)
	timeout := envDuration("PWC_HTTP_TIMEOUT", 8*time.Second)

	httpClient := &http.Client{
		Timeout: timeout,
	}

	items, err := hfPaperSearch(ctx, httpClient, base, query, limit)
	hfSearchError := ""
	if err != nil {
		hfSearchError = err.Error()
		items = nil
	}

	candidates := make([]repoCandidate, 0, len(items))
	for _, item := range items {
		pid := strings.TrimSpace(item.Paper.ID)
		title := strings.TrimSpace(item.Paper.Title)
		if pid == "" {
			continue
		}
		repos, _ := hfPaperRepos(ctx, httpClient, base, pid)
		candidates = append(candidates, repoCandidate{
			PaperID:   pid,
			Title:     title,
			RepoURLs:  repos,
			Source:    "papers_with_code(hf)",
			ScoreHint: repoScoreHint(query, title, repos),
		})
	}

	// An arXiv id is a precise lookup key but a poor repository-matching query;
	// fall back to the paper title for scoring and GitHub search.
	selectionQuery := query
	if arxivIDRe.MatchString(query) {
		if title := strings.TrimSpace(taskInputValue(runtimeTask, "paper_title")); title != "" {
			selectionQuery = cleanPaperTitle(title)
		} else {
			for _, candidate := range candidates {
				if strings.TrimSpace(candidate.Title) != "" {
					selectionQuery = cleanPaperTitle(candidate.Title)
					break
				}
			}
		}
	}

	// Curated entries are a labelled trusted fallback. Append them regardless
	// of whether Papers/GitHub search returned candidates: search results often
	// contain unrelated repositories that fail trusted selection.
	fallbackUsed := false
	curated := curatedRepoFallbackCandidates(query)
	if len(curated) > 0 {
		fallbackUsed = true
		candidates = append(candidates, curated...)
	}

	// A single scored selection replaces the old "firstSelectedRepo then
	// firstTrustedRepo" pair, which could pick an unrelated repository and then
	// reject it, blocking the GitHub fallback forever.
	selected, selectionReason := selectRepo(selectionQuery, candidates)
	if selected == "" {
		githubCandidates, ghErr := githubRepoSearch(ctx, httpClient, buildGitHubFallbackQueries(selectionQuery), limit)
		if ghErr != nil {
			hfSearchError = joinNonEmpty(hfSearchError, "github search: "+ghErr.Error())
		} else if len(githubCandidates) > 0 {
			fallbackUsed = true
			candidates = append(candidates, githubCandidates...)
			selected, selectionReason = selectRepo(selectionQuery, candidates)
		}
	}
	completeRepoDiscoveryTask(runtimeTask, query, candidates, selected, selectionReason, fallbackUsed, hfSearchError)
	return nil
}

func joinNonEmpty(values ...string) string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return strings.Join(out, "; ")
}

func completeRepoDiscoveryTask(runtimeTask *models.Task, query string, candidates []repoCandidate, selected, selectionReason string, fallbackUsed bool, searchError string) {
	candidateJSON, _ := json.Marshal(candidates)
	report := buildRepoDiscoveryReport(query, candidates, selected, selectionReason, fallbackUsed, searchError)
	if runtimeTask.Metadata == nil {
		runtimeTask.Metadata = map[string]any{}
	}
	artifacts := map[string]any{
		"candidate_repositories": string(candidateJSON),
		"repo_validation_report": report,
	}
	if strings.TrimSpace(selected) != "" {
		artifacts["repo_url"] = selected
		runtimeTask.Result = selected
		runtimeTask.Status = models.StatusCompleted
	} else {
		// Fail closed: "no trusted repository" is a user-actionable blocked state,
		// never an empty artifact finished as completed.
		missing := []models.MissingRequirement{{
			Kind:        "repo_url",
			Name:        "repository",
			Detail:      fmt.Sprintf("未找到可信仓库（query=%q）", query),
			Remediation: "请提供论文的 GitHub 仓库 URL，或补充 arXiv ID 后重试。",
		}}
		missingJSON, _ := json.Marshal(missing)
		artifacts["missing_requirements"] = string(missingJSON)
		runtimeTask.Metadata["missing_requirements"] = missing
		runtimeTask.Result = report
		runtimeTask.Status = models.StatusBlocked
		runtimeTask.Error = "repo_discovery: no trusted repository found for query " + query
	}
	runtimeTask.Metadata["artifact_values"] = artifacts
	runtimeTask.Metadata["repo_selection_reason"] = selectionReason
}

func hfPaperSearch(ctx context.Context, client *http.Client, base, query string, limit int) ([]hfPaperSearchItem, error) {
	endpoint := strings.TrimRight(base, "/") + "/api/papers/search?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "research-assistant-agent/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("papers api search failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var items []hfPaperSearchItem
	dec := json.NewDecoder(resp.Body)
	if err := dec.Decode(&items); err != nil {
		return nil, fmt.Errorf("decode papers search response: %w", err)
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func hfPaperRepos(ctx context.Context, client *http.Client, base, paperID string) ([]string, error) {
	endpoint := strings.TrimRight(base, "/") + "/api/papers/" + url.PathEscape(paperID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "research-assistant-agent/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("papers api info failed: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// 这里不强依赖具体字段名，直接从 JSON 中抽取 github 链接，适配 API 字段变化。
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	matches := githubURLRe.FindAllString(string(raw), -1)
	if len(matches) == 0 {
		return nil, nil
	}

	seen := map[string]struct{}{}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		u := strings.TrimSuffix(m, ".git")
		if _, ok := seen[u]; ok {
			continue
		}
		seen[u] = struct{}{}
		out = append(out, u)
	}
	return out, nil
}

func buildRepoDiscoveryQuery(runtimeTask *models.Task) string {
	// 1) 优先使用路由阶段已经提取好的结构化检索字段，避免再次从长文本里猜。
	if runtimeTask != nil && runtimeTask.Inputs != nil {
		if id := strings.TrimSpace(taskInputValue(runtimeTask, "paper_arxiv_id")); id != "" {
			return paperid.StripVersion(id)
		}
		if title := normalizeSearchQuery(taskInputValue(runtimeTask, "paper_title"), maxPaperSearchQueryLen); title != "" {
			return title
		}
		if query := normalizeSearchQuery(taskInputValue(runtimeTask, "paper_search_query"), maxPaperSearchQueryLen); query != "" {
			return query
		}
		if method := normalizeSearchQuery(taskInputValue(runtimeTask, "paper_method_name"), maxPaperSearchQueryLen); method != "" {
			return method
		}
	}

	// 2) 优先使用结构化的 paper_metadata（paper_parse 产物）
	if runtimeTask != nil && runtimeTask.Inputs != nil {
		if v, ok := runtimeTask.Inputs["paper_metadata"]; ok {
			var metadata models.PaperMetadata
			if err := json.Unmarshal([]byte(fmt.Sprint(v)), &metadata); err == nil {
				if title := normalizeSearchQuery(metadata.Title, maxPaperSearchQueryLen); title != "" {
					return title
				}
				if id := strings.TrimSpace(metadata.ArxivID); id != "" {
					return id
				}
			}
		}
	}

	// 3) 再从 parsed_paper 里提取 arXiv ID 或 Title
	if runtimeTask != nil && runtimeTask.Inputs != nil {
		if v, ok := runtimeTask.Inputs["parsed_paper"]; ok {
			s := fmt.Sprint(v)
			if id := paperid.Find(s); id != "" {
				return id
			}
			if title := extractTitleHeuristic(s); title != "" {
				return normalizeSearchQuery(cleanPaperTitle(title), maxPaperSearchQueryLen)
			}
		}
	}

	// 3) 再从任务描述里提取
	desc := ""
	if runtimeTask != nil {
		desc = runtimeTask.Description
	}
	if id := paperid.Find(desc); id != "" {
		return id
	}
	if title := extractTitleHeuristic(desc); title != "" {
		return normalizeSearchQuery(cleanPaperTitle(title), maxPaperSearchQueryLen)
	}

	// 4) 最后退回到描述本身（做长度限制避免把整段 prompt 扔去检索）
	return normalizeSearchQuery(desc, maxPaperSearchQueryLen)
}

func extractTitleHeuristic(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		// 先做一层 Markdown 装饰清洗，兼容：
		// **论文标题**: *Attention Is All You Need*
		// ### Title: Attention Is All You Need
		cleaned := stripMarkdownDecorators(l)
		// HuggingFace paper markdown 常见格式：Title: xxx
		if strings.HasPrefix(strings.ToLower(cleaned), "title:") {
			return collapseSingleLine(strings.TrimSpace(cleaned[len("title:"):]))
		}
		// 中文报告里另一种常见格式：标题：xxx
		if strings.HasPrefix(cleaned, "标题") {
			if title := extractAfterTitleSeparator(cleaned); title != "" {
				return title
			}
		}
		// 中文报告里常见格式：论文标题：xxx
		if strings.HasPrefix(cleaned, "论文标题") {
			if title := extractAfterTitleSeparator(cleaned); title != "" {
				return title
			}
		}
	}
	return ""
}

func extractAfterTitleSeparator(text string) string {
	if idx := strings.Index(text, "："); idx >= 0 && idx+len("：") < len(text) {
		return collapseSingleLine(strings.TrimSpace(text[idx+len("："):]))
	}
	if idx := strings.Index(text, ":"); idx >= 0 && idx+len(":") < len(text) {
		return collapseSingleLine(strings.TrimSpace(text[idx+len(":"):]))
	}
	return ""
}

func normalizeSearchQuery(text string, maxLen int) string {
	query := cleanPaperTitle(collapseSingleLine(text))
	if query == "" {
		return ""
	}

	// 避免把整段任务描述或长提示词直接传给 Papers API。
	if idx := strings.IndexAny(query, "。！？.!?;；"); idx > 0 {
		query = strings.TrimSpace(query[:idx])
	}
	if len(query) > maxLen {
		query = strings.TrimSpace(query[:maxLen])
	}
	return query
}

func collapseSingleLine(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	return strings.Join(fields, " ")
}

func stripMarkdownDecorators(text string) string {
	replacer := strings.NewReplacer(
		"**", "",
		"*", "",
		"__", "",
		"_", "",
		"`", "",
		"#", "",
	)
	return collapseSingleLine(replacer.Replace(text))
}

func cleanPaperTitle(text string) string {
	title := collapseSingleLine(stripMarkdownDecorators(text))
	if title == "" {
		return ""
	}

	// 裁掉常见的作者/年份尾巴，例如：
	// Attention Is All You Need (Ashish Vaswani et al., 2017)
	// Attention Is All You Need - Vaswani et al.
	if idx := strings.Index(title, " ("); idx > 0 {
		tail := strings.ToLower(title[idx+2:])
		if strings.Contains(tail, "et al") || yearTailRe.MatchString(tail) || strings.Contains(tail, "vaswani") {
			title = strings.TrimSpace(title[:idx])
		}
	}
	if idx := strings.Index(title, " - "); idx > 0 {
		tail := strings.ToLower(title[idx+3:])
		if strings.Contains(tail, "et al") || yearTailRe.MatchString(tail) || strings.Contains(tail, "vaswani") {
			title = strings.TrimSpace(title[:idx])
		}
	}
	return strings.Trim(title, "\"' ")
}

func repoScoreHint(query, title string, repos []string) int {
	score := 0
	q := strings.ToLower(strings.TrimSpace(query))
	t := strings.ToLower(strings.TrimSpace(title))
	if q != "" && t != "" {
		if strings.Contains(t, q) || strings.Contains(q, t) {
			score += 5
		}
	}
	if len(repos) > 0 {
		score += 3
	}
	return score
}

func buildRepoDiscoveryReport(query string, candidates []repoCandidate, selected, selectionReason string, fallbackUsed bool, searchError string) string {
	type reportCandidate struct {
		PaperID   string   `json:"paper_id,omitempty"`
		Title     string   `json:"title"`
		RepoName  string   `json:"repo_name,omitempty"`
		RepoURLs  []string `json:"repo_urls"`
		Source    string   `json:"source"`
		ScoreHint int      `json:"score_hint"`
	}
	report := struct {
		Version      string            `json:"version"`
		Query        string            `json:"query"`
		Candidates   []reportCandidate `json:"candidates"`
		Selected     string            `json:"selected"`
		Reason       string            `json:"reason"`
		FallbackUsed bool              `json:"fallback_used"`
		SearchError  string            `json:"search_error,omitempty"`
	}{
		Version:      "repo.discovery/v2",
		Query:        query,
		Selected:     selected,
		Reason:       selectionReason,
		FallbackUsed: fallbackUsed,
		SearchError:  searchError,
		Candidates:   make([]reportCandidate, 0, len(candidates)),
	}
	if strings.TrimSpace(report.Reason) == "" {
		if selected == "" {
			report.Reason = "no candidate passed the trusted repository scoring threshold"
		} else {
			report.Reason = "selected by scored repository matching"
		}
	}
	for _, candidate := range candidates {
		report.Candidates = append(report.Candidates, reportCandidate{
			PaperID:   candidate.PaperID,
			Title:     candidate.Title,
			RepoName:  candidate.RepoName,
			RepoURLs:  candidate.RepoURLs,
			Source:    candidate.Source,
			ScoreHint: candidate.ScoreHint,
		})
	}
	payload, _ := json.MarshalIndent(report, "", "  ")
	return string(payload)
}

// selectRepo is the single repository-selection entry point. It scores every
// candidate once and returns the best trusted URL plus an explainable reason.
func selectRepo(query string, candidates []repoCandidate) (string, string) {
	type scoredCandidate struct {
		url    string
		score  int
		reason string
	}
	best := scoredCandidate{}
	tokens := significantTokens(cleanPaperTitle(query))
	curatedPaths := curatedRepoPaths(query)

	for _, candidate := range candidates {
		if len(candidate.RepoURLs) == 0 {
			continue
		}
		for _, rawURL := range candidate.RepoURLs {
			repoURL := normalizeGitHubRepoURL(rawURL)
			if repoURL == "" || !githubURLRe.MatchString(repoURL) || githubURLRe.FindString(repoURL) != repoURL {
				continue
			}
			score, reason := scoreRepoCandidate(query, tokens, curatedPaths, candidate, repoURL)
			if score > best.score {
				best = scoredCandidate{url: repoURL, score: score, reason: reason}
			}
		}
	}
	if best.url == "" || best.score < minRepoSelectionScore {
		return "", ""
	}
	return best.url, best.reason
}

func scoreRepoCandidate(query string, tokens []string, curatedPaths map[string]struct{}, candidate repoCandidate, repoURL string) (int, string) {
	lowerText := strings.ToLower(candidateSearchText(candidate))
	repoPath := strings.ToLower(strings.TrimPrefix(repoURL, "https://github.com/"))
	score := 0
	reasons := make([]string, 0, 5)

	if strings.EqualFold(strings.TrimSpace(candidate.Source), "user_preference") {
		return 1000, "source=user_preference (explicit user choice)"
	}
	if _, ok := curatedPaths[repoPath]; ok {
		// Curated entries bypass the threshold: they are labelled data, not guesses.
		return 100, "source=curated_catalog; repo=" + repoPath
	}

	matched := 0
	for _, token := range tokens {
		if strings.Contains(lowerText, token) {
			matched++
		}
	}
	if len(tokens) > 0 {
		ratio := float64(matched) / float64(len(tokens))
		score += int(ratio * 50)
		reasons = append(reasons, fmt.Sprintf("matched_tokens=%d/%d", matched, len(tokens)))
	}
	for _, token := range tokens {
		if strings.Contains(repoPath, token) {
			score += 20
			reasons = append(reasons, "repo_name_contains="+token)
			break
		}
	}
	switch {
	case candidate.Stars >= 10000:
		score += 10
	case candidate.Stars >= 3000:
		score += 8
	case candidate.Stars >= 500:
		score += 5
	case candidate.Stars >= 50:
		score += 2
	}
	switch strings.ToLower(strings.TrimSpace(candidate.Source)) {
	case "user_preference":
		score += 1000
	case "curated_fallback":
		score += 100
	case "papers_with_code(hf)":
		score += 5
	case "github_search":
		score += 3
	}
	reasons = append(reasons, "source="+candidate.Source, fmt.Sprintf("score=%d", score))
	return score, strings.Join(reasons, "; ")
}

func candidateSearchText(candidate repoCandidate) string {
	parts := make([]string, 0, 4)
	if name := strings.TrimSpace(candidate.RepoName); name != "" {
		parts = append(parts, name)
	}
	if desc := strings.TrimSpace(candidate.Description); desc != "" {
		parts = append(parts, desc)
	}
	for _, repoURL := range candidate.RepoURLs {
		repoURL = strings.TrimSpace(repoURL)
		if repoURL == "" {
			continue
		}
		parts = append(parts, repoURL)
		if parsed, err := url.Parse(repoURL); err == nil {
			parts = append(parts, strings.Trim(parsed.Path, "/"))
		}
	}
	return strings.Join(parts, " ")
}

func curatedRepoPaths(query string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, entry := range catalog.MatchRepositories(query) {
		for _, repoURL := range entry.RepoURLs {
			path := strings.ToLower(strings.TrimPrefix(strings.TrimSuffix(repoURL, ".git"), "https://github.com/"))
			if path != "" {
				out[path] = struct{}{}
			}
		}
	}
	return out
}

func buildGitHubFallbackQueries(title string) []string {
	title = cleanPaperTitle(title)
	if title == "" {
		return nil
	}

	queries := []string{
		title,
		title + " implementation",
	}
	for _, entry := range catalog.MatchRepositories(title) {
		queries = append(queries, entry.FallbackQueries...)
	}
	return uniqueNonEmptyStrings(queries)
}

// curatedRepoFallbackCandidates reads the curated repository catalog. It is a
// labelled fallback used only when Papers/GitHub search produced no usable
// repository; the discovery report states that a curated entry was selected.
func curatedRepoFallbackCandidates(query string) []repoCandidate {
	matches := catalog.MatchRepositories(query)
	candidates := make([]repoCandidate, 0, len(matches))
	for _, entry := range matches {
		candidates = append(candidates, repoCandidate{
			Title:        entry.Title,
			RepoName:     entry.RepoName,
			Description:  entry.Description,
			RepoURLs:     append([]string(nil), entry.RepoURLs...),
			Source:       "curated_fallback",
			ScoreHint:    entry.ScoreHint,
			FallbackUsed: true,
		})
	}
	return candidates
}

func githubRepoSearch(ctx context.Context, client *http.Client, queries []string, limit int) ([]repoCandidate, error) {
	if len(queries) == 0 {
		return nil, nil
	}

	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	seen := map[string]struct{}{}
	out := make([]repoCandidate, 0, limit)

	for _, query := range queries {
		endpoint := "https://api.github.com/search/repositories?q=" + url.QueryEscape(query) + "&per_page=" + fmt.Sprintf("%d", limit)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("User-Agent", "research-assistant-agent/1.0")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				return
			}
			var payload githubSearchResponse
			if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
				return
			}
			for _, item := range payload.Items {
				if item.HTMLURL == "" || item.Archived || item.Fork {
					continue
				}
				if _, ok := seen[item.HTMLURL]; ok {
					continue
				}
				seen[item.HTMLURL] = struct{}{}
				out = append(out, repoCandidate{
					Title:        query,
					RepoName:     item.FullName,
					Description:  item.Description,
					RepoURLs:     []string{item.HTMLURL},
					Source:       "github_search",
					ScoreHint:    githubRepoScore(query, item),
					Stars:        item.StargazersCount,
					FallbackUsed: true,
				})
			}
		}()
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].ScoreHint > out[j].ScoreHint })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func githubRepoScore(query string, item githubRepoItem) int {
	score := 0
	lowerQuery := strings.ToLower(cleanPaperTitle(query))
	fullName := strings.ToLower(item.FullName)
	desc := strings.ToLower(item.Description)

	// Curated entries carry an explicit score hint. Matching by repo name keeps
	// the ranking generic: new papers are added to repositories.json instead of
	// this function.
	for _, entry := range catalog.MatchRepositories(query) {
		for _, repoURL := range entry.RepoURLs {
			repoPath := strings.ToLower(strings.TrimPrefix(repoURL, "https://github.com/"))
			if repoPath != "" && strings.HasSuffix(fullName, repoPath) {
				score += entry.ScoreHint
			}
		}
	}

	for _, token := range significantTokens(lowerQuery) {
		if strings.Contains(fullName, token) {
			score += 6
		}
		if strings.Contains(desc, token) {
			score += 3
		}
	}

	switch {
	case item.StargazersCount >= 10000:
		score += 12
	case item.StargazersCount >= 3000:
		score += 8
	case item.StargazersCount >= 500:
		score += 4
	}
	return score
}

func significantTokens(text string) []string {
	stop := map[string]struct{}{
		"is": {}, "all": {}, "you": {}, "the": {}, "a": {}, "an": {}, "of": {}, "and": {}, "for": {}, "with": {}, "paper": {}, "implementation": {},
	}
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	out := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, f := range fields {
		if len(f) < 3 {
			continue
		}
		if _, ok := stop[f]; ok {
			continue
		}
		if _, ok := seen[f]; ok {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, f)
	}
	return out
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func envInt(key string, fallback, min, max int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int(r-'0')
	}
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
