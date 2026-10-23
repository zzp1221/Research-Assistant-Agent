package catalog

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

func TestCatalogLoads(t *testing.T) {
	if err := Error(); err != nil {
		t.Fatalf("catalog load failed: %v", err)
	}
	if len(Repositories()) == 0 {
		t.Fatal("repository catalog is empty")
	}
	if len(Recipes()) == 0 {
		t.Fatal("recipe catalog is empty")
	}
}

func TestMatchRepositories(t *testing.T) {
	matches := MatchRepositories("Attention Is All You Need")
	if len(matches) == 0 {
		t.Fatal("expected a curated match for Attention Is All You Need")
	}
	if matches[0].RepoURLs[0] != "https://github.com/harvardnlp/annotated-transformer" {
		t.Fatalf("unexpected repository: %+v", matches[0])
	}
	if got := MatchRepositories("a totally unrelated paper about geology"); len(got) != 0 {
		t.Fatalf("expected no curated match, got %+v", got)
	}
	// A generic method name must not silently map to a specific paper's repo.
	if got := MatchRepositories("a generic transformer architecture paper"); len(got) != 0 {
		t.Fatalf("expected no curated match for a generic transformer query, got %+v", got)
	}
}

func TestTemplatesRender(t *testing.T) {
	for _, recipe := range Recipes() {
		raw, err := Template(recipe)
		if err != nil {
			t.Fatalf("recipe %s: %v", recipe.ID, err)
		}
		parsed, err := template.New(recipe.ID).Option("missingkey=error").Parse(raw)
		if err != nil {
			t.Fatalf("recipe %s template parse: %v", recipe.ID, err)
		}
		var rendered bytes.Buffer
		data := map[string]any{"PaperTitleJSON": "\"Example Paper\"", "VariantsJSON": "[]"}
		if err := parsed.Execute(&rendered, data); err != nil {
			t.Fatalf("recipe %s template execute: %v", recipe.ID, err)
		}
		if strings.Contains(rendered.String(), "{{") {
			t.Fatalf("recipe %s left template markers: %s", recipe.ID, rendered.String())
		}
	}
}
