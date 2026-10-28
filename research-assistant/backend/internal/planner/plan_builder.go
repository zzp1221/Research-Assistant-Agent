package planner

import (
	"context"

	"research-assistant-backend/internal/models"
)

// ContextRequirement describes one plan-level context dependency.
type ContextRequirement struct {
	Key      string
	Required bool
	Kind     string // attachment / paper_metadata / document / repo / dataset
	Role     string
	AnyOf    []string
}

// PlanBuilder is the single planning interface used by the registry. The V2
// builder functions remain as adapters while their data-flow contracts are
// migrated incrementally.
type PlanBuilder interface {
	IntentType() string
	ContextRequirements() []ContextRequirement
	Build(ctx context.Context, intent models.IntentContext) ([]*models.TaskNode, error)
}

type planBuilderAdapter struct {
	intentType   string
	requirements []ContextRequirement
	build        func(models.IntentContext) []*models.TaskNode
}

func (b planBuilderAdapter) IntentType() string { return b.intentType }
func (b planBuilderAdapter) ContextRequirements() []ContextRequirement {
	return b.requirements
}
func (b planBuilderAdapter) Build(_ context.Context, intent models.IntentContext) ([]*models.TaskNode, error) {
	return b.build(intent), nil
}

// TemplateKey selects a template variant for an intent type. Variants are
// simple / standard / full (Paper_Reproduction uses light for simple).
type TemplateKey struct {
	IntentType string
	Variant    string
}

var planBuilderRegistry = map[TemplateKey]PlanBuilder{
	{IntentType: "Code_Execution", Variant: "simple"}:   planBuilderAdapter{intentType: "Code_Execution", build: buildCodeExecutionSimpleNodes},
	{IntentType: "Code_Execution", Variant: "standard"}: planBuilderAdapter{intentType: "Code_Execution", build: buildCodeExecutionNodesV2},
	{IntentType: "Code_Execution", Variant: "full"}:     planBuilderAdapter{intentType: "Code_Execution", build: buildCodeExecutionFullNodes},
	{IntentType: "Framework_Evaluation", Variant: "simple"}:   planBuilderAdapter{intentType: "Framework_Evaluation", build: buildFrameworkEvaluationSimpleNodes},
	{IntentType: "Framework_Evaluation", Variant: "standard"}: planBuilderAdapter{intentType: "Framework_Evaluation", build: buildFrameworkEvaluationNodesV2},
	{IntentType: "Framework_Evaluation", Variant: "full"}:     planBuilderAdapter{intentType: "Framework_Evaluation", build: buildFrameworkEvaluationFullNodes},
	{IntentType: "Paper_Reproduction", Variant: "light"}: planBuilderAdapter{
		intentType: "Paper_Reproduction",
		requirements: []ContextRequirement{
			{Key: "paper_metadata", Required: true, Kind: "paper_metadata", Role: "paper"},
		},
		build: buildPaperReproductionLightNodes,
	},
	{IntentType: "Paper_Reproduction", Variant: "standard"}: planBuilderAdapter{
		intentType: "Paper_Reproduction",
		requirements: []ContextRequirement{
			{Key: "paper_metadata", Required: true, Kind: "paper_metadata", Role: "paper"},
		},
		build: buildPaperReproductionNodesV2,
	},
	{IntentType: "Paper_Reproduction", Variant: "full"}: planBuilderAdapter{
		intentType: "Paper_Reproduction",
		requirements: []ContextRequirement{
			{Key: "paper_metadata", Required: true, Kind: "paper_metadata", Role: "paper"},
		},
		build: buildPaperReproductionNodesV2,
	},
	{IntentType: "Custom_Benchmark", Variant: "standard"}: planBuilderAdapter{intentType: "Custom_Benchmark", build: buildCustomDatasetBenchmarkNodes},
	{IntentType: "AutoResearch", Variant: "standard"}: planBuilderAdapter{
		intentType: "AutoResearch",
		build: func(intent models.IntentContext) []*models.TaskNode {
			if isDatasetExperimentIntent(intent) {
				return buildDatasetExperimentNodes(intent)
			}
			return buildAutoResearchNodes(intent)
		},
	},
	{IntentType: "General", Variant: "simple"}: planBuilderAdapter{intentType: "General", build: buildGeneralSimpleNodes},
	{IntentType: "General", Variant: "standard"}: planBuilderAdapter{
		intentType: "General",
		requirements: []ContextRequirement{
			{Key: "paper_metadata", Required: false, Kind: "paper_metadata", Role: "paper"},
		},
		build: buildGeneralNodesV2,
	},
	{IntentType: "General", Variant: "full"}: planBuilderAdapter{intentType: "General", build: buildGeneralFullNodes},
}

// buildRegisteredNodes selects the standard variant (legacy entry point).
func buildRegisteredNodes(ctx context.Context, intent models.IntentContext) ([]*models.TaskNode, error) {
	return buildRegisteredNodesWithVariant(ctx, intent, "standard")
}

// buildRegisteredNodesWithVariant selects a variant with a conservative
// fallback chain: requested variant -> standard -> General standard.
func buildRegisteredNodesWithVariant(ctx context.Context, intent models.IntentContext, variant string) ([]*models.TaskNode, error) {
	if builder, ok := planBuilderRegistry[TemplateKey{IntentType: intent.IntentType, Variant: variant}]; ok {
		return builder.Build(ctx, intent)
	}
	if builder, ok := planBuilderRegistry[TemplateKey{IntentType: intent.IntentType, Variant: "standard"}]; ok {
		return builder.Build(ctx, intent)
	}
	builder := planBuilderRegistry[TemplateKey{IntentType: "General", Variant: "standard"}]
	return builder.Build(ctx, intent)
}
