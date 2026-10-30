package planner

import (
	"encoding/json"
	"fmt"
	"regexp"
	intentpkg "research-assistant-backend/internal/intent"
	"research-assistant-backend/internal/models"
	"strconv"
	"strings"
)

func buildFrameworkEvaluationNodesV2(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	normalized := strings.ToLower(context)
	frameworks := frameworkNamesFromIntent(intent, normalized)
	needsBenchmark := boolEntity(intent.Entities, "needs_benchmark") || intentpkg.ContainsBenchmarkIntent(context) || hasAny(normalized,
		"benchmark",
		"\u6027\u80fd",
		"\u8bc4\u6d4b",
		"latency",
		"\u54cd\u5e94\u65f6\u95f4",
		"\u8017\u65f6",
		"\u65f6\u5ef6",
		"\u541e\u5410",
		"run",
		"\u8fd0\u884c",
		"\u5b9e\u9a8c",
	)
	needsPlot := boolEntity(intent.Entities, "needs_plot") || intentpkg.ContainsPlotIntent(context)

	t1 := newNode("Research Candidate Frameworks", "framework_research", "librarian_agent", nil, nil, []string{"framework_research_report"}, true, context)
	if !needsBenchmark {
		nodes := []*models.TaskNode{t1}
		dependencies := make([]string, 0, len(frameworks))
		requiredArtifacts := make([]string, 0, len(frameworks))
		for idx, framework := range frameworks {
			artifact := fmt.Sprintf("framework_fit_report_%d", idx+1)
			node := newNode("Analyze "+framework+" Fit", fmt.Sprintf("framework_fit_%d", idx+1), "librarian_agent", []string{t1.ID}, []string{"framework_research_report"}, []string{artifact}, true, context)
			nodes = append(nodes, node)
			dependencies = append(dependencies, node.ID)
			requiredArtifacts = append(requiredArtifacts, artifact)
		}
		report := newNode("Generate Selection Recommendation", "framework_recommendation", "data_agent", dependencies, requiredArtifacts, []string{"evaluation_report"}, false, context)
		nodes = append(nodes, report)
		if needsPlot {
			plot := newNode("Render Framework Comparison Chart", "render_plot", "coder_agent", []string{report.ID}, []string{"evaluation_report"}, []string{"plot_image"}, true, context)
			nodes = append(nodes, plot)
		}
		return nodes
	}

	nodes := []*models.TaskNode{t1}
	runDependencies := make([]string, 0, len(frameworks))
	runArtifacts := make([]string, 0, len(frameworks))
	for idx, framework := range frameworks {
		prefix := frameworkArtifactPrefix(framework, idx)
		codeArtifact := fmt.Sprintf("%s_generated_code", prefix)
		dependencyArtifact := fmt.Sprintf("%s_dependency_spec", prefix)
		runtimeArtifact := fmt.Sprintf("%s_runtime_session", prefix)
		preparedArtifact := fmt.Sprintf("%s_prepared_runtime", prefix)
		metricsArtifact := fmt.Sprintf("framework_metrics_%d", idx+1)
		generate := newNode("Generate "+framework+" Benchmark Code", "generate_code", "coder_agent", []string{t1.ID}, []string{"framework_research_report"}, []string{codeArtifact}, true, context)
		resolve := newNode("Resolve "+framework+" Dependencies", "resolve_dependencies", "coder_agent", []string{generate.ID}, []string{codeArtifact}, []string{dependencyArtifact}, true, context)
		prepare := newNode("Prepare "+framework+" Runtime", "prepare_runtime", "sandbox_agent", []string{resolve.ID}, []string{dependencyArtifact}, []string{runtimeArtifact}, true, context)
		install := newNode("Install "+framework+" Dependencies", "install_dependencies", "sandbox_agent", []string{prepare.ID}, []string{runtimeArtifact, dependencyArtifact}, []string{preparedArtifact, prefix + "_dependency_install_report"}, true, context)
		run := newNode("Run "+framework+" Benchmark", "execute_code", "sandbox_agent", []string{install.ID}, []string{codeArtifact, preparedArtifact}, []string{metricsArtifact}, true, context)
		nodes = append(nodes, generate, resolve, prepare, install, run)
		runDependencies = append(runDependencies, run.ID)
		runArtifacts = append(runArtifacts, metricsArtifact)
	}
	report := newNode("Generate Benchmark Report", "framework_report", "data_agent", runDependencies, runArtifacts, []string{"evaluation_report"}, false, context)
	nodes = append(nodes, report)
	if needsPlot {
		plot := newNode("Render Framework Comparison Chart", "render_plot", "coder_agent", []string{report.ID}, []string{"evaluation_report"}, []string{"plot_image"}, true, context)
		nodes = append(nodes, plot)
	}
	return nodes
}

func buildPaperReproductionNodesV2(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	normalized := strings.ToLower(context)
	needsAblation := boolEntity(intent.Entities, "needs_ablation") || hasAny(normalized,
		"ablation", "消融", "参数敏感性", "模块移除", "随机种子", "seed stability", "运行成本对照",
	)
	needsPlot := boolEntity(intent.Entities, "needs_plot") || intentpkg.ContainsPlotIntent(context)
	needsFix := boolEntity(intent.Entities, "needs_fix") || hasAny(normalized,
		"debug",
		"fix",
		"\u4fee\u590d",
		"\u6392\u67e5",
		"\u4e0d\u4e00\u81f4",
		"\u91cd\u8dd1",
	)
	paperTitle := stringEntity(intent.Entities, "paper_title", "Paper")

	t1 := newNode("Parse "+paperTitle+" & Extract Method", "paper_parse", "librarian_agent", nil, nil, []string{"parsed_paper", "paper_metadata", "paper_summary"}, true, context)
	t1.InputBindings = []models.InputBinding{
		{Target: "document_id", Source: models.BindingSource{Kind: "context_document"}, Required: false},
		{Target: "paper_metadata", Source: models.BindingSource{Kind: "context_paper"}, Required: false},
	}
	if uploadedFiles, ok := intent.Entities["uploaded_files"]; ok {
		t1.Inputs["uploaded_files"] = uploadedFiles
	}
	claimRubric := newNode(
		"Freeze Hierarchical Claim Rubric",
		"claim_rubric_extract",
		"librarian_agent",
		[]string{t1.ID},
		[]string{"parsed_paper"},
		[]string{"claim_rubric", "claim_rubric_report"},
		true,
		context,
	)
	t2 := newRepoDiscoveryNode([]string{t1.ID}, context, intent)
	t2.RequiredArtifacts = append(t2.RequiredArtifacts, "paper_metadata")
	repoPrepareDependencies := []string{t2.ID}
	repoPrepareArtifacts := []string{"repo_url", "candidate_repositories", "repo_validation_report"}
	var ablationDesign *models.TaskNode
	if needsAblation {
		ablationDesign = newNode(
			"Design Bounded Ablation Study",
			"ablation_design",
			"data_agent",
			[]string{t1.ID},
			[]string{"parsed_paper"},
			[]string{"ablation_plan", "selected_ablation_configs", "ablation_selection_report"},
			true,
			context,
		)
		ablationDesign.Inputs = buildAblationDesignInputs(intent)
		repoPrepareDependencies = append(repoPrepareDependencies, ablationDesign.ID)
		repoPrepareArtifacts = append(repoPrepareArtifacts, "ablation_plan", "selected_ablation_configs")
	}
	t3 := newNode("Prepare Workspace", "repo_prepare", "coder_agent", repoPrepareDependencies, repoPrepareArtifacts, []string{"workspace_path", "code_file_path", "generated_code", "repo_manifest", "reproduction_mode_report", "dataset_shape_profile"}, false, context)
	t3.Inputs = buildPaperReproductionInputs(intent)
	t4 := newNode("Resolve "+paperTitle+" Dependencies", "resolve_dependencies", "coder_agent", []string{t3.ID}, []string{"workspace_path", "code_file_path", "generated_code", "repo_manifest"}, []string{"dependency_spec"}, false, context)
	t4b := newNode("Derive Reproduction Runtime Spec", "reproduction_spec", "research_coding_agent", []string{t4.ID}, []string{"paper_metadata", "repo_manifest", "dependency_spec", "parsed_paper"}, []string{"reproduction_runtime_spec"}, false, context)
	t4c := newNode("Probe Sandbox Runtime Capabilities", "runtime_probe", "sandbox_agent", []string{t4b.ID}, []string{"reproduction_runtime_spec"}, []string{"runtime_capabilities", "missing_requirements"}, false, context)
	t4d := newNode("Prepare Runtime Image", "image_prepare", "sandbox_agent", []string{t4c.ID}, []string{"reproduction_runtime_spec"}, []string{"runtime_image", "image_digest"}, false, context)
	t5 := newNode("Setup Runtime Environment", "prepare_runtime", "sandbox_agent", []string{t4d.ID}, []string{"workspace_path", "reproduction_runtime_spec", "runtime_image"}, []string{"runtime_session"}, false, context)
	t6 := newNode("Install "+paperTitle+" Dependencies", "install_dependencies", "sandbox_agent", []string{t5.ID}, []string{"runtime_session", "dependency_spec", "reproduction_runtime_spec"}, []string{"prepared_runtime", "dependency_install_report"}, false, context)
	t6b := newNode("Validate Reproduction Runtime", "runtime_validate", "sandbox_agent", []string{t6.ID}, []string{"prepared_runtime", "reproduction_runtime_spec", "runtime_image", "image_digest"}, []string{"environment_manifest", "runtime_validation_report"}, false, context)
	t7 := newNode("Execute And Debug Baseline", "paper_code_execute", "research_coding_agent", []string{t6b.ID}, []string{"workspace_path", "code_file_path", "generated_code", "prepared_runtime", "repo_manifest", "reproduction_runtime_spec", "environment_manifest"}, []string{"run_metrics", "paper_debug_report", "paper_patch_manifest"}, false, context)
	t8 := newNode("Compare With Paper Claims", "paper_compare", "data_agent", []string{t7.ID}, []string{"run_metrics", "paper_debug_report", "parsed_paper", "repo_manifest", "reproduction_mode_report", "environment_manifest"}, []string{"comparison_report"}, false, context)

	nodes := []*models.TaskNode{t1, claimRubric, t2}
	if ablationDesign != nil {
		nodes = append(nodes, ablationDesign)
	}
	nodes = append(nodes, t3, t4, t4b, t4c, t4d, t5, t6, t6b, t7, t8)
	lastID := t8.ID
	lastArtifact := "comparison_report"

	if needsFix {
		fix := newNode(
			"Debug Result Gap And Rerun",
			"fix_and_rerun",
			"research_coding_agent",
			[]string{t7.ID, t8.ID},
			[]string{"workspace_path", "code_file_path", "generated_code", "prepared_runtime", "repo_manifest", "run_metrics", "paper_debug_report", "comparison_report"},
			[]string{"rerun_metrics", "rerun_report", "gap_debug_report", "gap_patch_manifest"},
			false,
			context,
		)
		nodes = append(nodes, fix)
		lastID = fix.ID
		lastArtifact = "rerun_metrics"
	}
	if needsPlot {
		visualize := newNode("Visualize Reproduction Results", "result_visualization", "coder_agent", []string{lastID}, []string{lastArtifact}, []string{"result_plot"}, true, context)
		nodes = append(nodes, visualize)
		lastID = visualize.ID
	}
	evidenceArtifacts := []string{
		"claim_rubric",
		"parsed_paper",
		"repo_manifest",
		"reproduction_mode_report",
		"reproduction_runtime_spec",
		"environment_manifest",
		"runtime_validation_report",
		"dependency_install_report",
		"run_metrics",
		"paper_debug_report",
		"paper_patch_manifest",
		"comparison_report",
	}
	if needsFix {
		evidenceArtifacts = append(evidenceArtifacts, "rerun_metrics", "rerun_report", "gap_debug_report", "gap_patch_manifest")
	}
	if needsPlot {
		evidenceArtifacts = append(evidenceArtifacts, "result_plot")
	}
	claimEvidence := newNode(
		"Build Claim-to-Evidence Graph",
		"claim_evidence_build",
		"data_agent",
		[]string{claimRubric.ID, lastID},
		evidenceArtifacts,
		[]string{"claim_evidence_graph", "claim_verification_report"},
		false,
		context,
	)
	nodes = append(nodes, claimEvidence)
	return nodes
}

func buildAblationDesignInputs(intent models.IntentContext) map[string]any {
	maxExperiments := boundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:最多|max(?:imum)?)\s*(\d+)\s*(?:组|个|项|次)?\s*(?:轻量|小型|bounded)?\s*(?:消融|实验|ablations?|experiments?)`,
	}, 3, 1, 6)
	maxGPUMinutes := boundedIntentNumber(intent.RawIntent, []string{
		`(?i)gpu(?:\s*时间|\s*time)?\s*(?:不超过|最多|[:：]?)?\s*(\d+)\s*(?:minutes?|分钟)`,
		`(?i)(\d+)\s*(?:gpu[- ]?minutes?|gpu\s*分钟|分钟\s*gpu)`,
	}, 30, 0, 1440)
	maxWallMinutes := boundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:总耗时|总时长|wall(?:[- ]?time)?)\s*(?:不超过|最多|[:：]?)?\s*(\d+)\s*(?:minutes?|分钟)`,
	}, 60, 5, 1440)
	inputs := map[string]any{
		"ablation_max_experiments":  maxExperiments,
		"ablation_max_gpu_minutes":  maxGPUMinutes,
		"ablation_max_wall_minutes": maxWallMinutes,
		"ablation_strategy":         "bounded_tree_of_thoughts",
	}
	return inputs
}

func boundedIntentNumber(raw string, patterns []string, fallback, minimum, maximum int) int {
	if value, ok := optionalBoundedIntentNumber(raw, patterns, minimum, maximum); ok {
		return value
	}
	return fallback
}

func optionalBoundedIntentNumber(raw string, patterns []string, minimum, maximum int) (int, bool) {
	for _, pattern := range patterns {
		matches := regexp.MustCompile(pattern).FindStringSubmatch(raw)
		if len(matches) < 2 {
			continue
		}
		value, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		if value < minimum {
			return minimum, true
		}
		if value > maximum {
			return maximum, true
		}
		return value, true
	}
	return 0, false
}

func buildPaperReproductionInputs(intent models.IntentContext) map[string]any {
	raw := strings.ToLower(strings.Join([]string{intent.RawIntent, intent.RewrittenIntent}, " "))
	requestedMode := "auto"
	smokeRequested := boolEntity(intent.Entities, "smoke_reproduction") ||
		boolEntity(intent.Constraints, "smoke_reproduction") ||
		hasAny(raw, "smoke", "最小实验", "最小验证", "快速验证")
	explicitFullRequested := hasAny(raw,
		"full reproduction", "full run", "enable full", "run wmt",
		"完整复现", "全量复现", "完整实验", "全量实验", "开启全量", "采用 full",
	)
	fullRequested := boolEntity(intent.Entities, "full_reproduction") ||
		boolEntity(intent.Constraints, "full_reproduction") ||
		hasAny(raw, "full reproduction", "full run", "bleu", "wmt14", "完整复现", "全量复现", "完整实验", "全量实验")
	if smokeRequested && !explicitFullRequested {
		fullRequested = false
	}
	if smokeRequested {
		requestedMode = "smoke"
	}
	if fullRequested {
		requestedMode = "full"
	}
	if rawMode := strings.TrimSpace(stringEntity(intent.Constraints, "reproduction_mode", "")); rawMode != "" {
		requestedMode = rawMode
	}
	inputs := map[string]any{
		"requested_reproduction_mode": requestedMode,
		"full_reproduction_requested": fullRequested,
	}
	if uploadedFiles, ok := intent.Entities["uploaded_files"]; ok {
		inputs["uploaded_files"] = workspaceUploadReferences(uploadedFiles)
	}
	return inputs
}

func workspaceUploadReferences(value any) any {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var files []map[string]any
	if err := json.Unmarshal(raw, &files); err != nil {
		return value
	}
	for _, file := range files {
		delete(file, "text_excerpt")
	}
	return files
}

// ensureVerificationNode appends a verify_result close-out when a plan produces
// images but no verification node exists. The LLM planner is free-form, so this
// guard is what guarantees every image-producing plan gets the generic
// structural + visual verification (design 4.5), regardless of planner source.
func ensureVerificationNode(nodes []*models.TaskNode) []*models.TaskNode {
	if len(nodes) == 0 {
		return nodes
	}
	hasVerify := false
	imageProducers := make([]*models.TaskNode, 0, 2)
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if node.Type == "verify_result" {
			hasVerify = true
		}
		for _, artifact := range node.OutputArtifacts {
			lower := strings.ToLower(artifact)
			if strings.Contains(lower, "plot") || strings.Contains(lower, "image") {
				imageProducers = append(imageProducers, node)
				break
			}
		}
	}
	if hasVerify || len(imageProducers) == 0 {
		return nodes
	}
	// Attach the verifier to the deepest image producer so it runs after every
	// figure is on disk.
	producer := imageProducers[len(imageProducers)-1]
	for _, candidate := range imageProducers {
		if candidate.ID != producer.ID && len(candidate.Dependencies) > len(producer.Dependencies) {
			producer = candidate
		}
	}
	verify := newNode(
		"Verify And Summarize Result",
		"verify_result",
		"data_agent",
		[]string{producer.ID},
		append([]string(nil), producer.OutputArtifacts...),
		[]string{"verification_report"},
		true,
		"",
	)
	nodes = append(nodes, verify)
	return nodes
}

func buildCodeExecutionNodesV2(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	normalized := strings.ToLower(context)
	needsPlot := boolEntity(intent.Entities, "needs_plot") || intentpkg.ContainsPlotIntent(context)
	_ = normalized

	t1 := newNode("Generate Code", "generate_code", "coder_agent", nil, nil, []string{"generated_code"}, true, context)
	t2 := newNode("Resolve Dependencies", "resolve_dependencies", "coder_agent", []string{t1.ID}, []string{"generated_code"}, []string{"dependency_spec"}, true, context)
	t3 := newNode("Prepare Runtime", "prepare_runtime", "sandbox_agent", []string{t2.ID}, []string{"dependency_spec"}, []string{"runtime_session"}, false, context)
	t4 := newNode("Install Dependencies", "install_dependencies", "sandbox_agent", []string{t3.ID}, []string{"runtime_session", "dependency_spec"}, []string{"prepared_runtime", "dependency_install_report"}, false, context)
	t5 := newNode("Execute Code", "execute_code", "sandbox_agent", []string{t4.ID}, []string{"generated_code", "prepared_runtime"}, []string{"execution_result"}, false, context)

	nodes := []*models.TaskNode{t1, t2, t3, t4, t5}
	lastID := t5.ID
	lastArtifact := "execution_result"

	if needsPlot {
		t6 := newNode("Render Output Plot", "render_plot", "coder_agent", []string{lastID}, []string{lastArtifact}, []string{"plot_image"}, true, context)
		nodes = append(nodes, t6)
		lastID = t6.ID
		lastArtifact = "plot_image"
	}
	// verify_result is the close-out node for every executable plan: it runs the
	// generic structural + visual verification of the produced artifacts and
	// writes the advisory verdict. Plot plans must not skip it, otherwise a
	// figure could ship without any verification record (design 4.5).
	t7 := newNode("Verify And Summarize Result", "verify_result", "data_agent", []string{lastID}, []string{lastArtifact}, []string{"verification_report"}, true, context)
	nodes = append(nodes, t7)
	return nodes
}

func buildGeneralNodesV2(intent models.IntentContext) []*models.TaskNode {
	if !generalPlanFastPathEnabled() {
		return buildGeneralNodesLegacy(intent)
	}
	context := intent.RawIntent
	switch classifyGeneralPlanShape(intent) {
	case generalPlanLiterature:
		t1 := newNode("Search Literature", "literature_search", "librarian_agent", nil, nil, []string{"literature_results"}, true, context)
		t1.Inputs = map[string]any{
			"query":   literatureSearchQuery(intent),
			"sources": literatureSearchSources(intent),
			"limit":   5,
		}
		t2 := newNode("Collect Background Context", "general_research", "librarian_agent", []string{t1.ID}, []string{"literature_results"}, []string{"background_context"}, true, context)
		t3 := newNode("Synthesize Response", "general_synthesis", "answer_agent", []string{t2.ID}, []string{"background_context"}, []string{"general_response"}, false, context)
		return []*models.TaskNode{t1, t2, t3}
	case generalPlanPaper:
		// D10: fixed four-node shape for General + paper analysis. The claim
		// rubric stays exclusive to Paper_Reproduction, so general analysis uses
		// research -> verify -> answer without claim extraction.
		t1 := newNode("Parse Paper & Extract Method", "paper_parse", "librarian_agent", nil, nil, []string{"parsed_paper", "paper_metadata", "paper_summary"}, true, context)
		t1.InputBindings = []models.InputBinding{
			{Target: "document_id", Source: models.BindingSource{Kind: "context_document"}, Required: false},
			{Target: "paper_metadata", Source: models.BindingSource{Kind: "context_paper"}, Required: false},
		}
		t2 := newNode("Collect Paper Background", "general_research", "librarian_agent", []string{t1.ID}, []string{"paper_summary", "paper_metadata"}, []string{"background_context"}, true, context)
		t3 := newNode("Verify And Summarize Result", "verify_result", "data_agent", []string{t2.ID}, []string{"background_context", "paper_summary"}, []string{"verification_report"}, true, context)
		t4 := newNode("Synthesize Response", "general_synthesis", "answer_agent", []string{t3.ID}, []string{"verification_report", "paper_summary", "paper_metadata"}, []string{"general_response"}, false, context)
		return []*models.TaskNode{t1, t2, t3, t4}
	case generalPlanResearch:
		t1 := newNode("Collect Background Context", "general_research", "librarian_agent", nil, nil, []string{"background_context"}, true, context)
		t1.InputBindings = documentBindingsIfPresent(intent)
		t2 := newNode("Synthesize Response", "general_synthesis", "answer_agent", []string{t1.ID}, []string{"background_context"}, []string{"general_response"}, false, context)
		return []*models.TaskNode{t1, t2}
	default:
		t1 := newNode("Answer Request", "general_synthesis", "answer_agent", nil, nil, []string{"general_response"}, true, context)
		t1.InputBindings = documentBindingsIfPresent(intent)
		return []*models.TaskNode{t1}
	}
}

// buildGeneralNodesLegacy is the pre-fast-path General shape. It is kept behind
// GENERAL_PLAN_FAST_PATH=false for rollback and A/B comparison.
func buildGeneralNodesLegacy(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	normalized := strings.ToLower(context)
	if isLiteratureDiscoveryIntent(intent) {
		t1 := newNode("Search Literature", "literature_search", "librarian_agent", nil, nil, []string{"literature_results"}, true, context)
		t1.Inputs = map[string]any{
			"query":   literatureSearchQuery(intent),
			"sources": literatureSearchSources(intent),
			"limit":   5,
		}
		t2 := newNode("Collect Background Context", "general_research", "librarian_agent", []string{t1.ID}, []string{"literature_results"}, []string{"background_context"}, true, context)
		t3 := newNode("Synthesize Response", "general_synthesis", "answer_agent", []string{t2.ID}, []string{"background_context"}, []string{"general_response"}, false, context)
		return []*models.TaskNode{t1, t2, t3}
	}
	paperAnalysis := isPaperAnalysisIntent(intent)
	if paperAnalysis {
		// D10: fixed four-node shape for General + paper analysis. The claim
		// rubric stays exclusive to Paper_Reproduction, so general analysis uses
		// research -> verify -> answer without claim extraction.
		t1 := newNode("Parse Paper & Extract Method", "paper_parse", "librarian_agent", nil, nil, []string{"parsed_paper", "paper_metadata", "paper_summary"}, true, context)
		t1.InputBindings = []models.InputBinding{
			{Target: "document_id", Source: models.BindingSource{Kind: "context_document"}, Required: false},
			{Target: "paper_metadata", Source: models.BindingSource{Kind: "context_paper"}, Required: false},
		}
		t2 := newNode("Collect Paper Background", "general_research", "librarian_agent", []string{t1.ID}, []string{"paper_summary", "paper_metadata"}, []string{"background_context"}, true, context)
		t3 := newNode("Verify And Summarize Result", "verify_result", "data_agent", []string{t2.ID}, []string{"background_context", "paper_summary"}, []string{"verification_report"}, true, context)
		t4 := newNode("Synthesize Response", "general_synthesis", "answer_agent", []string{t3.ID}, []string{"verification_report", "paper_summary", "paper_metadata"}, []string{"general_response"}, false, context)
		return []*models.TaskNode{t1, t2, t3, t4}
	}
	if hasAny(normalized, "\u603b\u7ed3", "summary", "\u7efc\u8ff0", "report", "\u62a5\u544a", "rag", "query rewrite") {
		t1 := newNode("Collect Background Context", "general_research", "librarian_agent", nil, nil, []string{"background_context"}, true, context)
		t2 := newNode("Synthesize Response", "general_synthesis", "answer_agent", []string{t1.ID}, []string{"background_context"}, []string{"general_response"}, false, context)
		return []*models.TaskNode{t1, t2}
	}

	t1 := newNode("Scope Request", "general_process", "general_agent", nil, nil, []string{"intent_scope", "assumption_log"}, true, context)
	t2 := newNode("Collect Background Context", "general_research", "librarian_agent", []string{t1.ID}, []string{"intent_scope", "assumption_log"}, []string{"research_notes"}, true, context)
	t3 := newNode("Process Request", "general_process", "general_agent", []string{t1.ID, t2.ID}, []string{"intent_scope", "research_notes"}, []string{"final_answer"}, false, context)
	return []*models.TaskNode{t1, t2, t3}
}

// isPaperAnalysisIntent reports whether a General request is a paper analysis
// (as opposed to a generic chat). It is intentionally independent of the
// planner model so the plan shape is deterministic.
