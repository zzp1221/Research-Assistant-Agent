package intent

import "strings"

var plotKeywords = []string{
	"画图", "绘图", "画一个", "画个", "对比图", "柱状图", "折线图", "散点图",
	"曲线", "可视化", "plot", "matplotlib", "chart", "figure",
}

var frameworkKeywords = []string{
	"框架", "framework", "langchain", "llamaindex", "llama_index", "haystack",
	"dspy", "autogen", "crewai", "langgraph", "选型", "benchmark",
}

var comparisonKeywords = []string{
	"对比", "比较", "评估", "选型", "benchmark", "性能", "评测", "区别", "异同", "vs", "pk",
}

var benchmarkKeywords = []string{
	"benchmark", "性能", "评测", "latency", "响应时间", "耗时", "时延",
	"吞吐", "吞吐量", "run", "运行", "实验",
}

var knownFrameworkNames = []string{
	"langchain", "llamaindex", "llama_index", "haystack", "dspy", "autogen", "crewai", "langgraph",
}

func containsAnyNormalized(text string, keywords []string) bool {
	lower := strings.ToLower(text)
	for _, keyword := range keywords {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// ContainsPlotIntent reports whether the request asks for a chart/plot output.
func ContainsPlotIntent(text string) bool {
	return containsAnyNormalized(text, plotKeywords)
}

// ContainsFrameworkContext reports whether the request names a framework or
// otherwise clearly refers to framework comparison/evaluation.
func ContainsFrameworkContext(text string) bool {
	return containsAnyNormalized(text, frameworkKeywords)
}

// ContainsComparisonIntent reports whether the request asks for a comparison.
func ContainsComparisonIntent(text string) bool {
	return containsAnyNormalized(text, comparisonKeywords)
}

// ContainsBenchmarkIntent reports whether the request asks for measured
// benchmark/performance work rather than a qualitative comparison.
func ContainsBenchmarkIntent(text string) bool {
	return containsAnyNormalized(text, benchmarkKeywords)
}

// DetectKnownFrameworks returns canonical lowercase framework names found in
// the text. Unknown framework names are left to the LLM classifier.
func DetectKnownFrameworks(text string) []string {
	lower := strings.ToLower(text)
	found := make([]string, 0, 2)
	for _, name := range knownFrameworkNames {
		if strings.Contains(lower, name) {
			canonical := name
			if canonical == "llama_index" {
				canonical = "llamaindex"
			}
			if !containsString(found, canonical) {
				found = append(found, canonical)
			}
		}
	}
	return found
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
