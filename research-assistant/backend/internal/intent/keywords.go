package intent

import "strings"

var plotKeywords = []string{
	"画图", "绘图", "画一个", "画个", "对比图", "柱状图", "折线图", "散点图",
	"曲线", "可视化", "plot", "matplotlib", "chart", "figure",
}

// frameworkNames are the only strong signals that can trigger
// Framework_Evaluation on their own. A generic word such as "对比" or
// "性能" must never be enough.
var frameworkNames = []string{
	"langchain", "llamaindex", "llama_index", "haystack", "dspy", "autogen", "crewai", "langgraph",
}

// frameworkContextWords require a framework name to be present; they only
// describe that the user wants a framework-level comparison.
var frameworkContextWords = []string{
	"框架", "framework", "选型", "多框架对比",
}

// comparisonWords only express "compare these things"; they do not decide the
// intent by themselves.
var comparisonWords = []string{
	"对比", "比较", "区别", "异同", "vs", "pk", "比一比",
}

// benchmarkWords express "measure a metric"; they can drive needs_benchmark
// but are not framework-specific.
var benchmarkWords = []string{
	"benchmark", "性能", "评测", "latency", "响应时间", "耗时", "时延",
	"吞吐", "吞吐量", "run", "运行", "实验",
}

var codeKeywords = []string{
	"计算", "代码", "运行", "执行", "画图", "写一个", "生成代码", "跑一下",
	"plot", "matplotlib", "numpy", "python", "bisect", "排序", "算法",
}

var knownFrameworkNames = frameworkNames

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
// explicitly talks about framework selection. Benchmark/performance words are
// deliberately not included: "对比二分法和摩尔排序的性能" is a code task.
func ContainsFrameworkContext(text string) bool {
	if len(DetectKnownFrameworks(text)) > 0 {
		return true
	}
	return containsAnyNormalized(text, frameworkContextWords)
}

// ContainsCodeIntent reports whether the request is about writing/running code.
func ContainsCodeIntent(text string) bool {
	return containsAnyNormalized(text, codeKeywords)
}

// ContainsComparisonIntent reports whether the request asks for a comparison.
func ContainsComparisonIntent(text string) bool {
	return containsAnyNormalized(text, comparisonWords)
}

// ContainsBenchmarkIntent reports whether the request asks for measured
// benchmark/performance work rather than a qualitative comparison.
func ContainsBenchmarkIntent(text string) bool {
	return containsAnyNormalized(text, benchmarkWords)
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
