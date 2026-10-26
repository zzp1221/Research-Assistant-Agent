package api

import "testing"

func TestDetectIntentTypeSplitsComparisonFromFrameworkEvaluation(t *testing.T) {
	cases := []struct {
		intent string
		want   string
	}{
		{"对比一下python中二分法和摩尔排序的性能", "Code_Execution"},
		{"对比 LangChain 和 LlamaIndex 的 RAG 性能", "Framework_Evaluation"},
		{"帮我画一个正弦和余弦的对比图", "Code_Execution"},
		{"对比一下 RAG 和 GraphRAG 的检索质量", "General"},
	}
	for _, tc := range cases {
		if got := DetectIntentType(tc.intent); got != tc.want {
			t.Fatalf("DetectIntentType(%q)=%s, want %s", tc.intent, got, tc.want)
		}
	}
}
