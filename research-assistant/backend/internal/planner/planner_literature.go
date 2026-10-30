package planner

import (
	"fmt"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/paperid"
	"research-assistant-backend/internal/prompts"
	"strings"
)

// isPaperAnalysisIntent reports whether a General request is a paper analysis
// (as opposed to a generic chat). It is intentionally independent of the
// planner model so the plan shape is deterministic.
func isPaperAnalysisIntent(intent models.IntentContext) bool {
	if intent.IntentType != "General" {
		return false
	}
	if intent.Entities["paper_metadata"] != nil {
		return true
	}
	normalized := strings.ToLower(intent.RawIntent)
	return hasAny(normalized, "\u8bba\u6587", "paper", "\u6587\u732e", "\u8d21\u732e", "\u5c40\u9650")
}

// isLiteratureDiscoveryIntent reports whether a General request asks to find
// or survey papers rather than to analyze one uploaded paper.
func isLiteratureDiscoveryIntent(intent models.IntentContext) bool {
	if intent.IntentType != "General" {
		return false
	}
	if intent.Entities["paper_metadata"] != nil {
		return false
	}
	if _, ok := intent.Entities["uploaded_files"]; ok {
		return false
	}
	lower := strings.ToLower(intent.RawIntent)
	for _, phrase := range []string{
		"分析这篇", "总结这篇", "这篇论文的", "该论文的", "创新点", "局限性",
		"analyze this paper", "summarize this paper",
	} {
		if strings.Contains(lower, phrase) {
			return false
		}
	}
	discovery := hasAny(lower,
		"找", "搜索", "搜一下", "查找", "调研", "综述", "文献检索", "论文检索",
		"相关论文", "literature review", "survey", "find papers", "search papers",
		"search literature", "related papers", "paper search",
	)
	topic := hasAny(lower, "论文", "文献", "paper", "literature", "survey", "综述", "research")
	return discovery && topic
}

// literatureSearchQuery returns the most specific search string available.
func literatureSearchQuery(intent models.IntentContext) string {
	for _, key := range []string{"paper_search_query", "paper_title", "research_topic", "topic"} {
		if value, ok := intent.Entities[key]; ok {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return strings.TrimSpace(intent.RawIntent)
}

// literatureSearchSources reads an optional source restriction from entities.
// The default is the academic provider set; GitHub repositories are useful in
// other plans but are not papers for a literature review.
func literatureSearchSources(intent models.IntentContext) []string {
	raw, ok := intent.Entities["literature_sources"]
	if !ok || raw == nil {
		return []string{"arxiv", "openalex", "crossref", "semanticscholar"}
	}
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...)
	case []any:
		items := make([]string, 0, len(value))
		for _, entry := range value {
			if text := strings.TrimSpace(fmt.Sprint(entry)); text != "" {
				items = append(items, text)
			}
		}
		return items
	case string:
		items := make([]string, 0, 4)
		for _, entry := range strings.Split(value, ",") {
			if text := strings.TrimSpace(entry); text != "" {
				items = append(items, text)
			}
		}
		return items
	}
	return nil
}

func hasAny(s string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(s, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

func frameworkLabels(normalized string) []string {
	candidates := []string{"langchain", "llamaindex", "haystack", "autogen", "crewai", "langgraph"}
	labels := make([]string, 0, 2)
	for _, candidate := range candidates {
		if strings.Contains(normalized, candidate) {
			labels = append(labels, strings.ToUpper(candidate[:1])+candidate[1:])
		}
	}
	return labels
}

func frameworkNamesFromIntent(intent models.IntentContext, normalized string) []string {
	if raw, ok := intent.Entities["frameworks"].([]string); ok && len(raw) > 0 {
		names := make([]string, 0, len(raw))
		for _, item := range raw {
			names = append(names, prettifyFrameworkName(item))
		}
		return names
	}
	return fallbackFrameworkNames(frameworkLabels(normalized))
}

func fallbackFrameworkNames(frameworks []string) []string {
	if len(frameworks) == 0 {
		return []string{"Framework A", "Framework B"}
	}
	return frameworks
}

func prettifyFrameworkName(name string) string {
	switch strings.ToLower(name) {
	case "langchain":
		return "LangChain"
	case "llamaindex":
		return "LlamaIndex"
	case "langgraph":
		return "LangGraph"
	default:
		return name
	}
}

func boolEntity(entities map[string]any, key string) bool {
	value, ok := entities[key].(bool)
	return ok && value
}

func stringEntity(entities map[string]any, key string, fallback string) string {
	value, ok := entities[key].(string)
	if !ok || value == "" {
		return fallback
	}
	return value
}

func frameworkArtifactPrefix(framework string, index int) string {
	normalized := strings.ToLower(strings.TrimSpace(framework))
	replacer := strings.NewReplacer(" ", "_", "-", "_", "/", "_")
	normalized = replacer.Replace(normalized)
	if normalized == "" {
		return fmt.Sprintf("framework_%d", index+1)
	}
	return normalized
}

func newRepoDiscoveryNode(deps []string, context string, intent models.IntentContext) *models.TaskNode {
	node := newNode(
		"Retrieve Paper Repositories",
		"repo_discovery",
		"coder_agent",
		deps,
		[]string{"parsed_paper"},
		[]string{"candidate_repositories", "repo_validation_report", "repo_url"},
		true,
		context,
	)
	// 这里用固定流程覆盖默认描述，确保仓库检索节点不再只是“让 LLM 猜一个链接”。
	node.Description = buildRepoDiscoveryDescription(context)
	node.Inputs = buildRepoDiscoveryInputs(intent)
	return node
}

func buildRepoDiscoveryInputs(intent models.IntentContext) map[string]any {
	inputs := map[string]any{}
	if repoURL := stringEntity(intent.Entities, "preferred_repo_url", ""); repoURL != "" {
		inputs["preferred_repo_url"] = repoURL
	}
	if arxivID := stringEntity(intent.Entities, "paper_arxiv_id", ""); arxivID != "" {
		inputs["paper_arxiv_id"] = paperid.StripVersion(arxivID)
	}
	if title := stringEntity(intent.Entities, "paper_title", ""); title != "" {
		inputs["paper_title"] = title
	}
	if metadata, ok := intent.Entities["paper_metadata"].(map[string]any); ok {
		if _, exists := inputs["paper_title"]; !exists {
			if title := strings.TrimSpace(fmt.Sprint(metadata["title"])); title != "" {
				inputs["paper_title"] = title
			}
		}
		if _, exists := inputs["paper_arxiv_id"]; !exists {
			if arxivID := strings.TrimSpace(fmt.Sprint(metadata["arxiv_id"])); arxivID != "" && arxivID != "<nil>" {
				inputs["paper_arxiv_id"] = paperid.StripVersion(arxivID)
			}
		}
	}
	if query := stringEntity(intent.Entities, "paper_search_query", ""); query != "" {
		inputs["paper_search_query"] = query
	}
	if method := stringEntity(intent.Entities, "paper_method_name", ""); method != "" {
		inputs["paper_method_name"] = method
	}
	return inputs
}

func buildRepoDiscoveryDescription(rawIntent string) string {
	return prompts.RepoDiscoveryDescription(rawIntent)
}
