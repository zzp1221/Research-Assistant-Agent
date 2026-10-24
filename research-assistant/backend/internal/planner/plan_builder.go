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

var planBuilderRegistry = map[string]PlanBuilder{
	"Code_Execution": planBuilderAdapter{
		intentType: "Code_Execution",
		build:      buildCodeExecutionNodesV2,
	},
	"Framework_Evaluation": planBuilderAdapter{
		intentType: "Framework_Evaluation",
		build:      buildFrameworkEvaluationNodesV2,
	},
	"Paper_Reproduction": planBuilderAdapter{
		intentType: "Paper_Reproduction",
		requirements: []ContextRequirement{
			{Key: "paper_metadata", Required: true, Kind: "paper_metadata", Role: "paper"},
		},
		build: buildPaperReproductionNodesV2,
	},
	"Custom_Benchmark": planBuilderAdapter{
		intentType: "Custom_Benchmark",
		build:      buildCustomDatasetBenchmarkNodes,
	},
	"AutoResearch": planBuilderAdapter{
		intentType: "AutoResearch",
		build: func(intent models.IntentContext) []*models.TaskNode {
			if isDatasetExperimentIntent(intent) {
				return buildDatasetExperimentNodes(intent)
			}
			return buildAutoResearchNodes(intent)
		},
	},
	"General": planBuilderAdapter{
		intentType: "General",
		requirements: []ContextRequirement{
			{Key: "paper_metadata", Required: false, Kind: "paper_metadata", Role: "paper"},
		},
		build: buildGeneralNodesV2,
	},
}

func buildRegisteredNodes(ctx context.Context, intent models.IntentContext) ([]*models.TaskNode, error) {
	builder, ok := planBuilderRegistry[intent.IntentType]
	if !ok {
		builder = planBuilderRegistry["General"]
	}
	return builder.Build(ctx, intent)
}
