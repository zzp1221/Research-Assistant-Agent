package planner

import (
	"strings"

	"research-assistant-backend/internal/models"
)

// buildCodeExecutionSimpleNodes is the L0/L1 code template: generate, execute,
// optional plot, verify. Plot requests keep a coder-owned render_plot node so
// the existing plot artifact contract still holds.
func buildCodeExecutionSimpleNodes(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	needsPlot := boolEntity(intent.Entities, "needs_plot") || strings.Contains(strings.ToLower(context), "画") || strings.Contains(strings.ToLower(context), "plot")
	t1 := newNode("Generate Code", "generate_code", "coder_agent", nil, nil, []string{"generated_code"}, true, context)
	t2 := newNode("Execute Code", "execute_code", "sandbox_agent", []string{t1.ID}, []string{"generated_code"}, []string{"execution_result"}, false, context)
	lastID := t2.ID
	lastArtifact := "execution_result"
	nodes := []*models.TaskNode{t1, t2}
	if needsPlot {
		t3 := newNode("Render Output Plot", "render_plot", "coder_agent", []string{lastID}, []string{lastArtifact}, []string{"plot_image"}, true, context)
		nodes = append(nodes, t3)
		lastID = t3.ID
		lastArtifact = "plot_image"
	}
	verify := newNode("Verify And Summarize Result", "verify_result", "data_agent", []string{lastID}, []string{lastArtifact}, []string{"verification_report"}, true, context)
	nodes = append(nodes, verify)
	return nodes
}

// buildCodeExecutionFullNodes is the standard code template plus a final
// user-facing synthesis node.
func buildCodeExecutionFullNodes(intent models.IntentContext) []*models.TaskNode {
	nodes := buildCodeExecutionNodesV2(intent)
	if len(nodes) == 0 {
		return nodes
	}
	last := nodes[len(nodes)-1]
	synthesis := newNode("Synthesize Response", "general_synthesis", "answer_agent",
		[]string{last.ID}, append([]string(nil), last.OutputArtifacts...), []string{"general_response"}, false, intent.RawIntent)
	nodes = append(nodes, synthesis)
	return nodes
}

// buildFrameworkEvaluationSimpleNodes is the single-framework comparison
// template: research, generate, execute, report (optional plot).
func buildFrameworkEvaluationSimpleNodes(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	normalized := strings.ToLower(context)
	frameworks := frameworkNamesFromIntent(intent, normalized)
	name := "Candidate Framework"
	if len(frameworks) > 0 {
		name = frameworks[0]
	}
	needsPlot := boolEntity(intent.Entities, "needs_plot") || strings.Contains(normalized, "画") || strings.Contains(normalized, "plot")
	t1 := newNode("Research Candidate Frameworks", "framework_research", "librarian_agent", nil, nil, []string{"framework_research_report"}, true, context)
	t2 := newNode("Generate "+name+" Benchmark Code", "generate_code", "coder_agent", []string{t1.ID}, []string{"framework_research_report"}, []string{"framework_generated_code"}, true, context)
	t3 := newNode("Run "+name+" Benchmark", "execute_code", "sandbox_agent", []string{t2.ID}, []string{"framework_generated_code"}, []string{"framework_metrics"}, true, context)
	t4 := newNode("Generate Benchmark Report", "framework_report", "data_agent", []string{t3.ID}, []string{"framework_metrics"}, []string{"evaluation_report"}, false, context)
	nodes := []*models.TaskNode{t1, t2, t3, t4}
	if needsPlot {
		plot := newNode("Render Framework Comparison Chart", "render_plot", "coder_agent", []string{t4.ID}, []string{"evaluation_report"}, []string{"plot_image"}, true, context)
		nodes = append(nodes, plot)
	}
	return nodes
}

// buildFrameworkEvaluationFullNodes is the standard multi-framework template
// plus an explicit verification close-out.
func buildFrameworkEvaluationFullNodes(intent models.IntentContext) []*models.TaskNode {
	nodes := buildFrameworkEvaluationNodesV2(intent)
	if len(nodes) == 0 {
		return nodes
	}
	last := nodes[len(nodes)-1]
	verify := newNode("Verify And Summarize Result", "verify_result", "data_agent",
		[]string{last.ID}, append([]string(nil), last.OutputArtifacts...), []string{"verification_report"}, true, intent.RawIntent)
	nodes = append(nodes, verify)
	return nodes
}

// buildPaperReproductionLightNodes is the lightweight paper reproduction
// template: parse, prepare repository, run baseline, compare.
func buildPaperReproductionLightNodes(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	paperTitle := stringEntity(intent.Entities, "paper_title", "Paper")
	t1 := newNode("Parse "+paperTitle+" & Extract Method", "paper_parse", "librarian_agent", nil, nil, []string{"parsed_paper", "paper_metadata", "paper_summary"}, true, context)
	t1.InputBindings = []models.InputBinding{
		{Target: "document_id", Source: models.BindingSource{Kind: "context_document"}, Required: false},
		{Target: "paper_metadata", Source: models.BindingSource{Kind: "context_paper"}, Required: false},
	}
	t2 := newNode("Prepare Workspace", "repo_prepare", "coder_agent", []string{t1.ID}, []string{"paper_metadata"}, []string{"workspace_path", "code_file_path", "generated_code", "repo_manifest", "dataset_shape_profile"}, false, context)
	t2.Inputs = buildPaperReproductionInputs(intent)
	t3 := newNode("Execute And Debug Baseline", "paper_code_execute", "research_coding_agent", []string{t2.ID}, []string{"workspace_path", "code_file_path", "generated_code", "repo_manifest"}, []string{"run_metrics", "paper_debug_report", "paper_patch_manifest"}, false, context)
	t4 := newNode("Compare With Paper Claims", "paper_compare", "data_agent", []string{t3.ID}, []string{"run_metrics", "paper_debug_report", "parsed_paper"}, []string{"comparison_report"}, false, context)
	return []*models.TaskNode{t1, t2, t3, t4}
}

// buildGeneralSimpleNodes is the L0/L1 General answer template.
func buildGeneralSimpleNodes(intent models.IntentContext) []*models.TaskNode {
	t1 := newNode("Answer Request", "general_synthesis", "answer_agent", nil, nil, []string{"general_response"}, true, intent.RawIntent)
	t1.InputBindings = documentBindingsIfPresent(intent)
	return []*models.TaskNode{t1}
}

// buildGeneralFullNodes adds an explicit verification stage to the research
// shape. Paper/literature requests keep their established shapes.
func buildGeneralFullNodes(intent models.IntentContext) []*models.TaskNode {
	if isPaperAnalysisIntent(intent) || isLiteratureDiscoveryIntent(intent) {
		return buildGeneralNodesV2(intent)
	}
	context := intent.RawIntent
	t1 := newNode("Collect Background Context", "general_research", "librarian_agent", nil, nil, []string{"background_context"}, true, context)
	t1.InputBindings = documentBindingsIfPresent(intent)
	t2 := newNode("Verify And Summarize Result", "verify_result", "data_agent", []string{t1.ID}, []string{"background_context"}, []string{"verification_report"}, true, context)
	t3 := newNode("Synthesize Response", "general_synthesis", "answer_agent", []string{t2.ID}, []string{"verification_report", "background_context"}, []string{"general_response"}, false, context)
	return []*models.TaskNode{t1, t2, t3}
}
