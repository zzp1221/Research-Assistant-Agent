package planner

import (
	"fmt"
	intentpkg "research-assistant-backend/internal/intent"
	"research-assistant-backend/internal/models"
	"strings"
)

func bilingualTaskName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return name
	}
	if strings.Contains(trimmed, " / ") {
		return trimmed
	}

	if zh, ok := exactTaskNameTranslations()[trimmed]; ok {
		return zh + " / " + trimmed
	}

	switch {
	case strings.HasPrefix(trimmed, "Analyze ") && strings.HasSuffix(trimmed, " Fit"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Analyze "), " Fit")
		return fmt.Sprintf("分析 %s 适配性 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Prepare ") && strings.HasSuffix(trimmed, " Environment"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Prepare "), " Environment")
		return fmt.Sprintf("准备 %s 环境 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Prepare ") && strings.HasSuffix(trimmed, " Runtime"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Prepare "), " Runtime")
		return fmt.Sprintf("准备 %s 运行环境 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Generate ") && strings.HasSuffix(trimmed, " Benchmark Code"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Generate "), " Benchmark Code")
		return fmt.Sprintf("生成 %s 基准测试代码 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Resolve ") && strings.HasSuffix(trimmed, " Dependencies"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Resolve "), " Dependencies")
		return fmt.Sprintf("解析 %s 依赖 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Install ") && strings.HasSuffix(trimmed, " Dependencies"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Install "), " Dependencies")
		return fmt.Sprintf("安装 %s 依赖 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Run ") && strings.HasSuffix(trimmed, " Benchmark"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Run "), " Benchmark")
		return fmt.Sprintf("运行 %s 基准测试 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Run ") && strings.HasSuffix(trimmed, " Benchmark in Sandbox"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Run "), " Benchmark in Sandbox")
		return fmt.Sprintf("在沙箱中运行 %s 基准测试 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Parse ") && strings.HasSuffix(trimmed, " & Extract Method"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Parse "), " & Extract Method")
		return fmt.Sprintf("解析 %s 并提取方法 / %s", target, trimmed)
	case strings.HasPrefix(trimmed, "Read ") && strings.HasSuffix(trimmed, " Context"):
		target := strings.TrimSuffix(strings.TrimPrefix(trimmed, "Read "), " Context")
		return fmt.Sprintf("阅读 %s 背景 / %s", target, trimmed)
	}

	return trimmed
}

func exactTaskNameTranslations() map[string]string {
	return map[string]string{
		"Adapt Research Dataset":                                        "适配研究数据集",
		"Freeze Experiment Search Contract":                             "冻结实验搜索契约",
		"Prepare Experiment Runtime":                                    "准备实验运行环境",
		"Install Experiment Dependencies":                               "安装实验依赖",
		"Run Bounded Experiment Search":                                 "运行受限实验搜索",
		"Validate Best Experiment On Holdout":                           "在留出集验收最佳实验",
		"Summarize Scientific Research Evidence":                        "汇总自动研究证据",
		"Profile Uploaded Dataset":                                      "分析上传数据集",
		"Retrieve Benchmark Repository":                                 "获取评测目标仓库",
		"Prepare Benchmark Workspace":                                   "准备评测工作区",
		"Generate Repository Benchmark Adapter":                         "生成仓库评测适配器",
		"Resolve Benchmark Dependencies":                                "解析评测依赖",
		"Prepare Benchmark Runtime":                                     "准备评测运行环境",
		"Install Benchmark Dependencies":                                "安装评测依赖",
		"Preflight And Repair Benchmark Adapter":                        "预检并修复评测适配器",
		"Execute Custom Dataset Benchmark":                              "执行自定义数据评测",
		"Validate Benchmark Evidence":                                   "校验评测证据",
		"Generate Trusted Benchmark Report":                             "生成可信评测报告",
		"Retrieve AutoResearch Repository":                              "获取 AutoResearch 目标仓库",
		"Prepare AutoResearch Workspace":                                "准备 AutoResearch 工作区",
		"Freeze AutoResearch Contract":                                  "冻结 AutoResearch 研究契约",
		"Prepare AutoResearch Runtime":                                  "准备 AutoResearch 运行环境",
		"Install AutoResearch Dependencies":                             "安装 AutoResearch 依赖",
		"Run Bounded AutoResearch Loop":                                 "运行受限 AutoResearch 循环",
		"Validate AutoResearch Candidate":                               "验收 AutoResearch 候选",
		"Summarize AutoResearch Evidence":                               "汇总 AutoResearch 证据",
		"Research Candidate Frameworks":                                 "调研候选框架",
		"Render Framework Comparison Chart":                             "渲染框架对比图",
		"Generate Selection Recommendation":                             "生成选型建议",
		"Generate Benchmark Report":                                     "生成基准测试报告",
		"Design Shared RAG Benchmark Protocol":                          "设计共享 RAG 评测协议",
		"Compare RAG Benchmark Results":                                 "对比 RAG 基准测试结果",
		"Locate Reference Repository":                                   "定位参考仓库",
		"Prepare Workspace":                                             "准备工作区",
		"Setup Runtime Environment":                                     "搭建运行环境",
		"Design Bounded Ablation Study":                                 "设计受限消融实验",
		"Execute Baseline":                                              "执行基线实验",
		"Execute And Debug Baseline":                                    "执行并调试基线实验",
		"Compare With Paper Claims":                                     "对比论文声明结果",
		"Freeze Hierarchical Claim Rubric":                              "冻结分层主张验收标准",
		"Build Claim-to-Evidence Graph":                                 "构建主张证据图",
		"Visualize Reproduction Results":                                "可视化复现实验结果",
		"Fix Gaps And Rerun":                                            "修复问题并重新运行",
		"Debug Result Gap And Rerun":                                    "调试结果差异并重新运行",
		"Generate Code":                                                 "生成代码",
		"Resolve Dependencies":                                          "解析依赖",
		"Prepare Runtime":                                               "准备运行环境",
		"Install Dependencies":                                          "安装依赖",
		"Execute Code":                                                  "执行代码",
		"Render Output Plot":                                            "渲染输出图表",
		"Verify And Summarize Result":                                   "校验并总结结果",
		"Collect Background Context":                                    "收集背景信息",
		"Synthesize Response":                                           "综合生成回答",
		"Process Request":                                               "处理请求",
		"Retrieve Documentation & Best Practices for Target Frameworks": "获取目标框架文档与最佳实践",
		"Generate Environment Setup & Integration Code for Framework A": "为框架 A 生成环境配置与集成代码",
		"Generate Environment Setup & Integration Code for Framework B": "为框架 B 生成环境配置与集成代码",
		"Execute A/B Tests with User Data in Sandbox":                   "在沙箱中执行 A/B 测试",
		"Analyze Metrics & Generate Evaluation Report":                  "分析指标并生成评估报告",
		"Parse Paper & Extract Algorithm Details":                       "解析论文并提取算法细节",
		"Find/Clone Open Source Repository":                             "查找或克隆开源仓库",
		"Setup Sandbox Environment (Install Dependencies)":              "搭建沙箱环境并安装依赖",
		"Compare Results with Paper":                                    "将结果与论文进行对比",
		"Debug/Refine Code if Results Mismatch":                         "结果不一致时调试并优化代码",
		"Summarize Contributions":                                       "总结核心贡献",
		"List Limitations And Risks":                                    "列出局限与风险",
		"Compose Paper Summary":                                         "整理论文总结",
	}
}

func validateAgentPlannedNodes(intent models.IntentContext, nodes []*models.TaskNode) error {
	if len(nodes) == 0 {
		return fmt.Errorf("planner agent returned no executable nodes")
	}
	if err := validateCriticalNodeContracts(intent, nodes); err != nil {
		return err
	}
	if intent.IntentType == "General" {
		for _, node := range nodes {
			if node == nil {
				continue
			}
			if strings.HasPrefix(node.Type, "claim_") {
				return fmt.Errorf("General plans must not contain claim nodes (%s); claim rubric belongs to Paper_Reproduction", node.Type)
			}
		}
	}
	if intent.Entities != nil && intent.Entities["paper_metadata"] != nil &&
		(intent.IntentType == "General" || intent.IntentType == "Paper_Reproduction") {
		hasPaperParse := false
		for _, node := range nodes {
			if node != nil && node.Type == "paper_parse" {
				hasPaperParse = true
				break
			}
		}
		if !hasPaperParse {
			return fmt.Errorf("paper context requires a paper_parse node")
		}
	}
	if intent.IntentType != "Framework_Evaluation" {
		return nil
	}
	if boolEntity(intent.Entities, "needs_plot") && !hasPlotNode(nodes) {
		return fmt.Errorf("framework evaluation plan requested plotting but has no plot node")
	}

	normalized := strings.ToLower(intent.RawIntent)
	needsBenchmark := boolEntity(intent.Entities, "needs_benchmark") || intentpkg.ContainsBenchmarkIntent(intent.RawIntent) || hasAny(normalized,
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
	if !needsBenchmark {
		return nil
	}

	expectedFrameworks := frameworkNamesFromIntent(intent, normalized)
	executePrefixes := map[string]struct{}{}
	prepareCount := 0

	for _, node := range nodes {
		if node == nil || node.AssignedTo != "sandbox_agent" {
			continue
		}
		switch node.Type {
		case "prepare_runtime":
			prepareCount++
		case "execute_code":
			codePrefix := ""
			runtimePrefix := ""
			for _, artifact := range node.RequiredArtifacts {
				switch {
				case strings.HasSuffix(artifact, "_generated_code"):
					codePrefix = strings.TrimSuffix(artifact, "_generated_code")
				case strings.HasSuffix(artifact, "_prepared_runtime"):
					runtimePrefix = strings.TrimSuffix(artifact, "_prepared_runtime")
				}
			}
			if codePrefix == "" || runtimePrefix == "" || codePrefix != runtimePrefix {
				return fmt.Errorf("framework execute node %q is missing isolated code/runtime artifacts", node.Name)
			}
			executePrefixes[codePrefix] = struct{}{}
		}
	}

	if len(executePrefixes) < len(expectedFrameworks) {
		return fmt.Errorf("framework benchmark plan does not contain one isolated execute branch per framework")
	}
	if prepareCount < len(expectedFrameworks) {
		return fmt.Errorf("framework benchmark plan does not contain one runtime branch per framework")
	}
	return nil
}

func validateCriticalNodeContracts(intent models.IntentContext, nodes []*models.TaskNode) error {
	if err := validateTaskRegistry(nodes); err != nil {
		return err
	}
	if intent.IntentType != "Code_Execution" && intent.IntentType != "Framework_Evaluation" && intent.IntentType != "Paper_Reproduction" && intent.IntentType != "AutoResearch" && intent.IntentType != "Custom_Benchmark" && intent.IntentType != "General" {
		return nil
	}

	hasGeneratedCode := false
	hasResolveDependencies := false
	hasPrepareRuntime := false
	hasInstallDependencies := false
	hasExecuteCode := false
	hasPaperParse := false
	hasClaimRubric := false
	hasClaimEvidence := false
	hasRepoDiscovery := false
	hasRepoPrepare := false
	hasReproductionSpec := false
	hasImagePrepare := false
	hasRuntimeValidate := false
	hasAutoResearchSpec := false
	hasAutoResearchRun := false
	hasAutoResearchValidation := false
	hasExperimentDataset := false
	hasExperimentSpec := false
	hasExperimentRun := false
	hasExperimentValidation := false
	hasBenchmarkAudit := false
	hasBenchmarkSplit := false
	hasBenchmarkContract := false
	hasBenchmarkExecute := false
	hasBenchmarkValidation := false

	for _, node := range nodes {
		if node == nil {
			continue
		}

		switch node.Type {
		case "benchmark_dataset_audit":
			hasBenchmarkAudit = true
			if node.AssignedTo != "benchmark_agent" || !containsArtifact(node.OutputArtifacts, "dataset_manifest") || !containsArtifact(node.OutputArtifacts, "benchmark_dataset_audit") {
				return fmt.Errorf("benchmark_dataset_audit node %q has an incomplete audit contract", node.Name)
			}
		case "benchmark_split_materialize":
			hasBenchmarkSplit = true
			if node.AssignedTo != "benchmark_agent" || !containsArtifact(node.RequiredArtifacts, "benchmark_dataset_audit") || !containsArtifact(node.OutputArtifacts, "benchmark_split_manifest") || !containsArtifact(node.OutputArtifacts, "benchmark_public_test_manifest") || !containsArtifact(node.OutputArtifacts, "benchmark_input_only_preflight_manifest") {
				return fmt.Errorf("benchmark_split_materialize node %q has an incomplete split contract", node.Name)
			}
		case "benchmark_contract_freeze":
			hasBenchmarkContract = true
			if node.AssignedTo != "benchmark_agent" || !containsArtifact(node.RequiredArtifacts, "benchmark_split_manifest") || !containsArtifact(node.OutputArtifacts, "benchmark_metric_contract") || !containsArtifact(node.OutputArtifacts, "benchmark_reward_contract") {
				return fmt.Errorf("benchmark_contract_freeze node %q has an incomplete metric/reward contract", node.Name)
			}
		case "benchmark_execute":
			hasBenchmarkExecute = true
			if node.AssignedTo != "research_coding_agent" || !containsArtifact(node.RequiredArtifacts, "benchmark_contract") || !containsArtifact(node.OutputArtifacts, "benchmark_hidden_predictions_path") {
				return fmt.Errorf("benchmark_execute node %q has an incomplete hidden execution contract", node.Name)
			}
		case "benchmark_validate":
			hasBenchmarkValidation = true
			if intent.IntentType == "Custom_Benchmark" && (node.AssignedTo != "benchmark_agent" || !containsArtifact(node.RequiredArtifacts, "benchmark_hidden_predictions_path") || !containsArtifact(node.OutputArtifacts, "benchmark_validation_report")) {
				return fmt.Errorf("benchmark_validate node %q has an incomplete hidden validation contract", node.Name)
			}
		case "experiment_dataset_prepare":
			hasExperimentDataset = true
			if node.AssignedTo != "research_coding_agent" || !containsArtifact(node.OutputArtifacts, "workspace_path") || !containsArtifact(node.OutputArtifacts, "experiment_dataset_manifest") {
				return fmt.Errorf("experiment_dataset_prepare node %q must output a workspace and dataset manifest", node.Name)
			}
		case "experiment_spec":
			hasExperimentSpec = true
			if node.AssignedTo != "research_coding_agent" || !containsArtifact(node.RequiredArtifacts, "experiment_dataset_manifest") || !containsArtifact(node.OutputArtifacts, "experiment_spec") || !containsArtifact(node.OutputArtifacts, "dependency_spec") {
				return fmt.Errorf("experiment_spec node %q has an incomplete frozen contract", node.Name)
			}
		case "experiment_run":
			hasExperimentRun = true
			if node.AssignedTo != "research_coding_agent" || !containsArtifact(node.RequiredArtifacts, "prepared_runtime") || !containsArtifact(node.RequiredArtifacts, "experiment_spec") || !containsArtifact(node.RequiredArtifacts, "ablation_plan") || !containsArtifact(node.OutputArtifacts, "experiment_trial_ledger") {
				return fmt.Errorf("experiment_run node %q has an incomplete execution contract", node.Name)
			}
		case "experiment_validate":
			hasExperimentValidation = true
			if node.AssignedTo != "research_coding_agent" || !containsArtifact(node.RequiredArtifacts, "experiment_trial_ledger") || !containsArtifact(node.RequiredArtifacts, "experiment_best_candidate") || !containsArtifact(node.OutputArtifacts, "experiment_validation_report") {
				return fmt.Errorf("experiment_validate node %q has an incomplete validation contract", node.Name)
			}
		case "reproduction_spec":
			hasReproductionSpec = true
			if node.AssignedTo != "research_coding_agent" || !containsArtifact(node.OutputArtifacts, "reproduction_runtime_spec") {
				return fmt.Errorf("reproduction_spec node %q must output reproduction_runtime_spec", node.Name)
			}
		case "image_prepare":
			hasImagePrepare = true
			if node.AssignedTo != "sandbox_agent" || !containsArtifact(node.OutputArtifacts, "runtime_image") || !containsArtifact(node.OutputArtifacts, "image_digest") {
				return fmt.Errorf("image_prepare node %q must output runtime_image and image_digest", node.Name)
			}
		case "runtime_validate":
			hasRuntimeValidate = true
			if node.AssignedTo != "sandbox_agent" || !containsArtifact(node.OutputArtifacts, "environment_manifest") || !containsArtifact(node.OutputArtifacts, "runtime_validation_report") {
				return fmt.Errorf("runtime_validate node %q must output environment_manifest and runtime_validation_report", node.Name)
			}
		case "generate_code":
			hasGeneratedCode = true
			if node.AssignedTo != "coder_agent" {
				return fmt.Errorf("generate_code node %q must be assigned to coder_agent", node.Name)
			}
		case "paper_parse":
			hasPaperParse = true
			if node.AssignedTo != "librarian_agent" {
				return fmt.Errorf("paper_parse node %q must be assigned to librarian_agent", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "parsed_paper") {
				return fmt.Errorf("paper_parse node %q must output parsed_paper", node.Name)
			}
		case "claim_rubric_extract":
			hasClaimRubric = true
			if node.AssignedTo != "librarian_agent" {
				return fmt.Errorf("claim_rubric_extract node %q must be assigned to librarian_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "parsed_paper") || !containsArtifact(node.OutputArtifacts, "claim_rubric") {
				return fmt.Errorf("claim_rubric_extract node %q must consume parsed_paper and output claim_rubric", node.Name)
			}
		case "claim_evidence_build":
			hasClaimEvidence = true
			if node.AssignedTo != "data_agent" {
				return fmt.Errorf("claim_evidence_build node %q must be assigned to data_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "claim_rubric") || !containsArtifact(node.RequiredArtifacts, "run_metrics") || !containsArtifact(node.RequiredArtifacts, "comparison_report") {
				return fmt.Errorf("claim_evidence_build node %q must consume claim_rubric, run_metrics, and comparison_report", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "claim_evidence_graph") || !containsArtifact(node.OutputArtifacts, "claim_verification_report") {
				return fmt.Errorf("claim_evidence_build node %q must output claim_evidence_graph and claim_verification_report", node.Name)
			}
		case "ablation_design":
			if node.AssignedTo != "data_agent" {
				return fmt.Errorf("ablation_design node %q must be assigned to data_agent", node.Name)
			}
			if (!containsArtifact(node.RequiredArtifacts, "parsed_paper") && !containsArtifact(node.RequiredArtifacts, "experiment_spec")) || !containsArtifact(node.OutputArtifacts, "ablation_plan") {
				return fmt.Errorf("ablation_design node %q must consume parsed_paper or experiment_spec and output ablation_plan", node.Name)
			}
		case "repo_discovery":
			hasRepoDiscovery = true
			if node.AssignedTo != "coder_agent" {
				return fmt.Errorf("repo_discovery node %q must be assigned to coder_agent", node.Name)
			}
			if intent.IntentType != "AutoResearch" && intent.IntentType != "Custom_Benchmark" && !containsArtifact(node.RequiredArtifacts, "parsed_paper") {
				return fmt.Errorf("repo_discovery node %q must require parsed_paper", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "candidate_repositories") {
				return fmt.Errorf("repo_discovery node %q must output candidate_repositories", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "repo_validation_report") {
				return fmt.Errorf("repo_discovery node %q must output repo_validation_report", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "repo_url") {
				return fmt.Errorf("repo_discovery node %q must output repo_url", node.Name)
			}
		case "repo_prepare":
			hasRepoPrepare = true
			if node.AssignedTo != "coder_agent" {
				return fmt.Errorf("repo_prepare node %q must be assigned to coder_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "repo_url") {
				return fmt.Errorf("repo_prepare node %q must require repo_url", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "generated_code") && !containsArtifact(node.OutputArtifacts, "workspace_path") {
				return fmt.Errorf("repo_prepare node %q must output generated_code or workspace_path", node.Name)
			}
		case "resolve_dependencies":
			hasResolveDependencies = true
			if node.AssignedTo != "coder_agent" {
				return fmt.Errorf("resolve_dependencies node %q must be assigned to coder_agent", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "dependency_spec") {
				return fmt.Errorf("resolve_dependencies node %q must output dependency_spec", node.Name)
			}
		case "prepare_runtime":
			hasPrepareRuntime = true
			if node.AssignedTo != "sandbox_agent" {
				return fmt.Errorf("prepare_runtime node %q must be assigned to sandbox_agent", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "runtime_session") && !containsArtifact(node.OutputArtifacts, "runtime_env") {
				return fmt.Errorf("prepare_runtime node %q must output runtime_session or runtime_env", node.Name)
			}
		case "install_dependencies":
			hasInstallDependencies = true
			if node.AssignedTo != "sandbox_agent" {
				return fmt.Errorf("install_dependencies node %q must be assigned to sandbox_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "dependency_spec") {
				return fmt.Errorf("install_dependencies node %q must require dependency_spec", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "prepared_runtime") {
				return fmt.Errorf("install_dependencies node %q must output prepared_runtime", node.Name)
			}
		case "execute_code", "baseline_run":
			hasExecuteCode = true
			if node.AssignedTo != "sandbox_agent" {
				return fmt.Errorf("execute_code node %q must be assigned to sandbox_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "generated_code") && !containsArtifact(node.RequiredArtifacts, "code_file_path") {
				return fmt.Errorf("execute_code node %q must require generated code input", node.Name)
			}
		case "paper_code_execute":
			hasExecuteCode = true
			if node.AssignedTo != "research_coding_agent" {
				return fmt.Errorf("paper_code_execute node %q must be assigned to research_coding_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "code_file_path") || !containsArtifact(node.RequiredArtifacts, "prepared_runtime") {
				return fmt.Errorf("paper_code_execute node %q must require code_file_path and prepared_runtime", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "run_metrics") || !containsArtifact(node.OutputArtifacts, "paper_debug_report") {
				return fmt.Errorf("paper_code_execute node %q must output run_metrics and paper_debug_report", node.Name)
			}
		case "autoresearch_spec":
			hasAutoResearchSpec = true
			if node.AssignedTo != "research_coding_agent" {
				return fmt.Errorf("autoresearch_spec node %q must be assigned to research_coding_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "workspace_path") || !containsArtifact(node.OutputArtifacts, "research_spec") || !containsArtifact(node.OutputArtifacts, "dependency_spec") {
				return fmt.Errorf("autoresearch_spec node %q must consume workspace_path and output research_spec plus dependency_spec", node.Name)
			}
		case "autoresearch_run":
			hasAutoResearchRun = true
			if node.AssignedTo != "research_coding_agent" {
				return fmt.Errorf("autoresearch_run node %q must be assigned to research_coding_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "workspace_path") || !containsArtifact(node.RequiredArtifacts, "prepared_runtime") || !containsArtifact(node.RequiredArtifacts, "research_spec") {
				return fmt.Errorf("autoresearch_run node %q is missing frozen workspace/runtime/spec inputs", node.Name)
			}
			if !containsArtifact(node.OutputArtifacts, "research_trial_ledger") || !containsArtifact(node.OutputArtifacts, "research_best_candidate") {
				return fmt.Errorf("autoresearch_run node %q must output a trial ledger and best candidate", node.Name)
			}
		case "autoresearch_validate":
			hasAutoResearchValidation = true
			if node.AssignedTo != "research_coding_agent" {
				return fmt.Errorf("autoresearch_validate node %q must be assigned to research_coding_agent", node.Name)
			}
			if !containsArtifact(node.RequiredArtifacts, "research_spec") || !containsArtifact(node.RequiredArtifacts, "research_trial_ledger") || !containsArtifact(node.OutputArtifacts, "research_validation_report") {
				return fmt.Errorf("autoresearch_validate node %q must consume frozen evidence and output validation", node.Name)
			}
		}
	}

	if intent.IntentType == "Code_Execution" {
		if !hasGeneratedCode || !hasResolveDependencies || !hasPrepareRuntime || !hasInstallDependencies || !hasExecuteCode {
			return fmt.Errorf("code execution plan is missing one or more required canonical nodes")
		}
	}
	if intent.IntentType == "Paper_Reproduction" {
		if !hasPaperParse || !hasClaimRubric || !hasClaimEvidence || !hasRepoDiscovery || !hasRepoPrepare || !hasResolveDependencies || !hasPrepareRuntime || !hasInstallDependencies || !hasExecuteCode {
			return fmt.Errorf("paper reproduction plan is missing one or more required canonical nodes")
		}
		// Preserve the audited reproduction runtime chain: without these nodes
		// the plan cannot emit an EnvironmentManifest and would silently degrade
		// reproducibility. The deterministic template always provides them.
		if !hasReproductionSpec || !hasImagePrepare || !hasRuntimeValidate {
			return fmt.Errorf("paper reproduction plan is missing the reproducibility runtime chain (reproduction_spec/image_prepare/runtime_validate)")
		}
	}
	if intent.IntentType == "AutoResearch" {
		if isDatasetExperimentIntent(intent) {
			if !hasExperimentDataset || !hasExperimentSpec || !hasPrepareRuntime || !hasInstallDependencies || !hasExperimentRun || !hasExperimentValidation {
				return fmt.Errorf("dataset AutoResearch plan is missing one or more required canonical nodes")
			}
		} else if !hasRepoDiscovery || !hasRepoPrepare || !hasAutoResearchSpec || !hasPrepareRuntime || !hasInstallDependencies || !hasAutoResearchRun || !hasAutoResearchValidation {
			return fmt.Errorf("repository AutoResearch plan is missing one or more required canonical nodes")
		}
	}
	if intent.IntentType == "Custom_Benchmark" {
		if !hasBenchmarkAudit || !hasBenchmarkSplit || !hasBenchmarkContract || !hasRepoDiscovery || !hasRepoPrepare || !hasPrepareRuntime || !hasInstallDependencies || !hasBenchmarkExecute || !hasBenchmarkValidation {
			return fmt.Errorf("custom benchmark plan is missing one or more trusted benchmark nodes")
		}
	}
	if intent.IntentType == "General" {
		hasFinalAnswer := false
		for _, node := range nodes {
			if node == nil {
				continue
			}
			switch node.Type {
			case "general_process":
				if containsArtifact(node.OutputArtifacts, "final_answer") {
					hasFinalAnswer = true
				}
			case "general_synthesis":
				if containsArtifact(node.OutputArtifacts, "general_response") {
					hasFinalAnswer = true
				}
			}
		}
		if !hasFinalAnswer {
			return fmt.Errorf("general plan must contain a final answer-producing node")
		}
	}
	if boolEntity(intent.Entities, "needs_plot") && !hasPlotNode(nodes) {
		return fmt.Errorf("%s plan requested plotting but has no plot node", intent.IntentType)
	}

	return nil
}

func hasPlotNode(nodes []*models.TaskNode) bool {
	for _, node := range nodes {
		if node == nil || node.AssignedTo != "coder_agent" {
			continue
		}
		switch node.Type {
		case "render_plot":
			if containsArtifact(node.OutputArtifacts, "plot_image") {
				return true
			}
		case "result_visualization":
			if containsArtifact(node.OutputArtifacts, "result_plot") {
				return true
			}
		}
	}
	return false
}

func containsArtifact(values []string, artifact string) bool {
	for _, value := range values {
		if value == artifact || strings.HasSuffix(value, "_"+artifact) {
			return true
		}
	}
	return false
}
