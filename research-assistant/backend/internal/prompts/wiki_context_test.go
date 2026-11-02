package prompts

import (
	"strings"
	"testing"
)

func TestBuildTaskPromptWikiContextOrder(t *testing.T) {
	assembled, err := BuildTaskPrompt(TaskPromptInput{
		System:          "system",
		CoreMemory:      "core memory",
		RetrievedMemory: "retrieved memory",
		WikiContext:     "<wiki_paper id=\"paper-1\" title=\"Attention\"><wiki_summary>tldr</wiki_summary></wiki_paper>",
		TaskName:        "analyze",
		TaskDescription: "analyze the paper",
		Inputs:          map[string]any{"paper_metadata": map[string]any{"title": "Attention"}},
		Budget:          PromptBudget{ContextWindow: 128000, MaxOutput: 8192, Safety: 2000},
	})
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	retrieved := strings.Index(assembled.User, "[section: retrieved_memory]")
	wikiIndex := strings.Index(assembled.User, "[section: wiki_context]")
	inputs := strings.Index(assembled.User, "[section: task_inputs]")
	if retrieved < 0 || wikiIndex < 0 || inputs < 0 {
		t.Fatalf("missing section markers: retrieved=%d wiki=%d inputs=%d\n%s", retrieved, wikiIndex, inputs, assembled.User)
	}
	if !(retrieved < wikiIndex && wikiIndex < inputs) {
		t.Fatalf("section order wrong: retrieved=%d wiki=%d inputs=%d", retrieved, wikiIndex, inputs)
	}
	if assembled.Report.WikiTokens <= 0 {
		t.Fatalf("wiki_tokens not recorded: %+v", assembled.Report)
	}
}

func TestBuildTaskPromptWithoutWikiContext(t *testing.T) {
	assembled, err := BuildTaskPrompt(TaskPromptInput{System: "system", TaskName: "t", TaskDescription: "d", Budget: PromptBudget{ContextWindow: 128000, MaxOutput: 8192, Safety: 2000}})
	if err != nil {
		t.Fatalf("build prompt: %v", err)
	}
	if strings.Contains(assembled.User, "wiki_context") {
		t.Fatalf("wiki_context must not appear when empty")
	}
	if assembled.Report.WikiTokens != 0 {
		t.Fatalf("wiki_tokens=%d", assembled.Report.WikiTokens)
	}
}
