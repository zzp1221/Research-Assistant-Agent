// Package catalog loads the data-driven repository and smoke-recipe catalogs.
// Keeping this data outside Go source means adding a new paper no longer
// requires editing the scheduler or rebuilding logic branches.
package catalog

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
)

//go:embed repositories.json recipes.json templates/*.py
var files embed.FS

const (
	RepositoriesVersion = "catalog/repositories/v1"
	RecipesVersion      = "catalog/smoke-recipes/v1"
)

// Repository is one curated implementation entry. Curated repositories are a
// labelled fallback: the discovery report tells the user when one is used.
type Repository struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	MatchTerms      []string `json:"match_terms"`
	MethodTerms     []string `json:"method_terms,omitempty"`
	RepoName        string   `json:"repo_name"`
	RepoURLs        []string `json:"repo_urls"`
	Description     string   `json:"description,omitempty"`
	ScoreHint       int      `json:"score_hint,omitempty"`
	FallbackQueries []string `json:"fallback_queries,omitempty"`
}

type repositoryFile struct {
	Version string       `json:"version"`
	Entries []Repository `json:"entries"`
}

// SmokeRecipe describes how to build a bounded smoke runner for a repository
// shape. Templates are Go text/template files rendered with the paper title.
type SmokeRecipe struct {
	ID           string      `json:"id"`
	Kind         string      `json:"kind"`
	Template     string      `json:"template"`
	RequireFiles []string    `json:"require_files,omitempty"`
	TaskGroups   [][]string  `json:"task_groups,omitempty"`
	TextGroups   [][]string  `json:"text_groups,omitempty"`
	Variants     *VariantSet `json:"variants,omitempty"`
}

type VariantSet struct {
	Baseline          Variant              `json:"baseline"`
	DefaultCategories []string             `json:"default_categories"`
	Categories        map[string][]Variant `json:"categories"`
}

// Variant is one bounded ablation configuration.
type Variant struct {
	Name        string `json:"name"`
	Heads       int    `json:"heads"`
	UseScaling  bool   `json:"use_scaling"`
	UseResidual bool   `json:"use_residual"`
	BatchSize   int    `json:"batch_size"`
	SequenceLen int    `json:"sequence_length"`
	Seed        int    `json:"seed"`
	Category    string `json:"category"`
}

type recipeFile struct {
	Version string        `json:"version"`
	Recipes []SmokeRecipe `json:"recipes"`
}

var (
	once       sync.Once
	loadErr    error
	repository []Repository
	recipes    []SmokeRecipe
)

func load() {
	once.Do(func() {
		raw, err := files.ReadFile("repositories.json")
		if err != nil {
			loadErr = fmt.Errorf("read repository catalog: %w", err)
			return
		}
		var parsed repositoryFile
		if err := json.Unmarshal(raw, &parsed); err != nil {
			loadErr = fmt.Errorf("decode repository catalog: %w", err)
			return
		}
		if parsed.Version != RepositoriesVersion {
			loadErr = fmt.Errorf("unsupported repository catalog version %q", parsed.Version)
			return
		}
		repository = parsed.Entries

		raw, err = files.ReadFile("recipes.json")
		if err != nil {
			loadErr = fmt.Errorf("read recipe catalog: %w", err)
			return
		}
		var parsedRecipes recipeFile
		if err := json.Unmarshal(raw, &parsedRecipes); err != nil {
			loadErr = fmt.Errorf("decode recipe catalog: %w", err)
			return
		}
		if parsedRecipes.Version != RecipesVersion {
			loadErr = fmt.Errorf("unsupported recipe catalog version %q", parsedRecipes.Version)
			return
		}
		recipes = parsedRecipes.Recipes
	})
}

// Repositories returns every curated repository entry.
func Repositories() []Repository {
	load()
	return repository
}

// Recipes returns every smoke recipe in catalog order.
func Recipes() []SmokeRecipe {
	load()
	return recipes
}

// Error reports a catalog load failure, if any.
func Error() error {
	load()
	return loadErr
}

// MatchRepositories returns curated entries matching the query. A match on a
// title term is stronger than a method term, and longer terms win ties.
func MatchRepositories(query string) []Repository {
	load()
	lower := strings.ToLower(strings.TrimSpace(query))
	if lower == "" {
		return nil
	}
	type scored struct {
		repo  Repository
		score int
	}
	matches := make([]scored, 0, len(repository))
	for _, entry := range repository {
		score := 0
		for _, term := range entry.MatchTerms {
			if term != "" && strings.Contains(lower, strings.ToLower(term)) {
				score += len(term) * 2
			}
		}
		if score == 0 {
			for _, term := range entry.MethodTerms {
				if term != "" && strings.Contains(lower, strings.ToLower(term)) {
					score += len(term)
				}
			}
		}
		if score > 0 {
			matches = append(matches, scored{repo: entry, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		return matches[i].repo.ID < matches[j].repo.ID
	})
	out := make([]Repository, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.repo)
	}
	return out
}

// Template returns the raw smoke runner template for a recipe.
func Template(recipe SmokeRecipe) (string, error) {
	load()
	raw, err := files.ReadFile(recipe.Template)
	if err != nil {
		return "", fmt.Errorf("read smoke template %q: %w", recipe.Template, err)
	}
	return string(raw), nil
}

// RecipeByID looks up one recipe.
func RecipeByID(id string) (SmokeRecipe, bool) {
	for _, recipe := range Recipes() {
		if recipe.ID == id {
			return recipe, true
		}
	}
	return SmokeRecipe{}, false
}

// TemplateIDs returns the embedded template names, used by catalog tests.
func TemplateIDs() []string {
	entries, err := files.ReadDir("templates")
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}
