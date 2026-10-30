package planner

import (
	"regexp"
	"research-assistant-backend/internal/models"
	"strconv"
	"strings"
)

func buildDatasetExperimentNodes(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	inputs := buildDatasetExperimentInputs(intent)
	prepareData := newNode(
		"Adapt Research Dataset",
		"experiment_dataset_prepare",
		"research_coding_agent",
		nil,
		nil,
		[]string{"workspace_path", "experiment_dataset_manifest", "experiment_dataset_report"},
		false,
		context,
	)
	prepareData.Inputs = copyBenchmarkInputs(inputs)
	prepareData.Inputs["uploaded_files"] = intent.Entities["uploaded_files"]

	freeze := newNode(
		"Freeze Experiment Search Contract",
		"experiment_spec",
		"research_coding_agent",
		[]string{prepareData.ID},
		[]string{"workspace_path", "experiment_dataset_manifest"},
		[]string{"experiment_spec", "experiment_spec_report", "dependency_spec"},
		false,
		context,
	)
	freeze.Inputs = copyBenchmarkInputs(inputs)

	design := newNode(
		"Design Bounded ToT Strategy Tree",
		"ablation_design",
		"data_agent",
		[]string{freeze.ID},
		[]string{"experiment_spec"},
		[]string{"ablation_plan", "selected_ablation_configs", "ablation_selection_report"},
		true,
		context,
	)
	design.Description = "基于冻结的方法、参数、数据与预算契约，使用两层受限 ToT 展开、评分并剪枝实验方向；输出只负责候选设计，真实 Keep/Reject 仍由 evaluator 与 Harness 决定。\n用户原始意图: " + context
	design.Inputs = copyBenchmarkInputs(inputs)
	design.Inputs["ablation_max_experiments"] = min(maxTrialsFromExperimentInputs(inputs), 6)
	design.Inputs["ablation_max_wall_minutes"] = max(5, experimentWallMinutes(inputs))
	design.Inputs["ablation_max_gpu_minutes"] = 0
	design.Inputs["ablation_estimated_minutes_per_experiment"] = max(1, experimentWallMinutes(inputs)/maxTrialsFromExperimentInputs(inputs))

	prepareRuntime := newNode(
		"Prepare Experiment Runtime",
		"prepare_runtime",
		"sandbox_agent",
		[]string{freeze.ID},
		[]string{"workspace_path", "dependency_spec"},
		[]string{"runtime_session"},
		true,
		context,
	)
	install := newNode(
		"Install Experiment Dependencies",
		"install_dependencies",
		"sandbox_agent",
		[]string{prepareRuntime.ID},
		[]string{"workspace_path", "runtime_session", "dependency_spec"},
		[]string{"prepared_runtime", "dependency_install_report"},
		false,
		context,
	)
	run := newNode(
		"Run Bounded Experiment Search",
		"experiment_run",
		"research_coding_agent",
		[]string{install.ID, design.ID},
		[]string{"workspace_path", "prepared_runtime", "experiment_spec", "ablation_plan"},
		[]string{"experiment_trial_ledger", "experiment_best_candidate", "experiment_run_report"},
		false,
		context,
	)
	run.Inputs = copyBenchmarkInputs(inputs)
	if wallSeconds, ok := inputs["experiment_max_wall_seconds"].(int); ok {
		run.TimeoutSeconds = wallSeconds + 30
	}
	validate := newNode(
		"Validate Best Experiment On Holdout",
		"experiment_validate",
		"research_coding_agent",
		[]string{run.ID},
		[]string{"workspace_path", "prepared_runtime", "experiment_spec", "experiment_trial_ledger", "experiment_best_candidate"},
		[]string{"experiment_validation_report", "experiment_best_metrics"},
		false,
		context,
	)
	validate.RetryLimit = 0
	report := newNode(
		"Summarize Scientific Research Evidence",
		"verify_result",
		"data_agent",
		[]string{validate.ID},
		[]string{"experiment_dataset_report", "experiment_spec_report", "experiment_run_report", "experiment_trial_ledger", "experiment_validation_report", "experiment_best_metrics"},
		[]string{"evaluation_report"},
		false,
		context,
	)
	return []*models.TaskNode{prepareData, freeze, design, prepareRuntime, install, run, validate, report}
}

func maxTrialsFromExperimentInputs(inputs map[string]any) int {
	if value, ok := inputs["experiment_max_trials"].(int); ok && value > 0 {
		return value
	}
	return 12
}

func experimentWallMinutes(inputs map[string]any) int {
	if value, ok := inputs["experiment_max_wall_seconds"].(int); ok && value > 0 {
		return (value + 59) / 60
	}
	return 15
}

func isDatasetExperimentIntent(intent models.IntentContext) bool {
	if intent.Entities == nil || intent.Entities["uploaded_files"] == nil {
		return false
	}
	if boolEntity(intent.Entities, "autoresearch_config_only") {
		return false
	}
	if boolEntity(intent.Entities, "needs_dataset_research") {
		return true
	}
	if !isAutoResearchIntent(intent) {
		return false
	}
	normalized := strings.ToLower(intent.RawIntent)
	return hasAny(normalized,
		"策略", "超参数", "参数搜索", "方法选择", "最优方法", "最优配置", "自动调优",
		"strategy", "hyperparameter", "parameter search", "method selection", "best config",
		"rag", "retrieval", "bm25", "graphrag", "检索",
	)
}

func buildDatasetExperimentInputs(intent models.IntentContext) map[string]any {
	maxTrials := boundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:最多|最大|max(?:imum)?)\s*(\d+)\s*(?:次|轮|个)?\s*(?:试验|实验|trials?|experiments?|候选)`,
	}, 12, 1, 40)
	maxWallMinutes := boundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:总耗时|总时长|wall(?:[- ]?time)?|时间预算|预算)\s*(?:不超过|最多|[:：]?)?\s*(\d+)\s*(?:minutes?|分钟)`,
	}, 15, 1, 60)
	validationRuns := boundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:重复验证|独立复验|重复复验|validation\s+runs?)\s*(?:最多|不超过|[:：=]?)?\s*(\d+)\s*(?:次|轮|runs?)?`,
	}, 3, 1, 5)
	parallelTrials := boundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:并发|并行|parallel(?:ism)?|workers?)\s*(?:为|是|[:：=])?\s*(\d+)\s*(?:个|路|组|workers?|trials?)?`,
	}, 4, 1, 4)
	inputs := map[string]any{
		"experiment_max_trials":          maxTrials,
		"experiment_max_parallel_trials": parallelTrials,
		"experiment_max_wall_seconds":    maxWallMinutes * 60,
		"experiment_validation_runs":     validationRuns,
	}
	if domain := strings.TrimSpace(stringEntity(intent.Entities, "research_domain", "")); domain != "" {
		inputs["research_domain"] = domain
	}
	if adapter := strings.TrimSpace(stringEntity(intent.Entities, "experiment_adapter", "")); adapter != "" {
		inputs["experiment_adapter"] = adapter
	}
	if value, ok := optionalIntentFloat(intent.RawIntent, []string{
		`(?i)(?:目标(?:分数|指标)?|target(?:\s+score)?|bench(?:mark)?(?:\s+score)?)\s*(?:达到|为|是|[:：=]|>=)?\s*([-+]?(?:\d+(?:\.\d+)?|\.\d+))`,
	}); ok {
		inputs["experiment_target_score"] = value
	}
	if value, ok := optionalIntentFloat(intent.RawIntent, []string{
		`(?i)(?:holdout|隐藏集|留出集)(?:\s*目标|\s*target)?\s*(?:达到|为|是|[:：=]|>=)?\s*([-+]?(?:\d+(?:\.\d+)?|\.\d+))`,
	}); ok {
		inputs["experiment_holdout_target_score"] = value
	}
	if value, ok := optionalBoundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:top[_ -]?k|cutoff|召回深度|检索条数)\s*(?:为|是|[:：=])?\s*(\d+)`,
	}, 1, 100); ok {
		inputs["experiment_cutoff"] = value
	}
	normalized := strings.ToLower(intent.RawIntent)
	if matches := regexp.MustCompile(`(?i)\b(ndcg|recall|mrr)\s*@\s*(\d+)`).FindStringSubmatch(intent.RawIntent); len(matches) == 3 {
		metric := strings.ToLower(matches[1])
		if metric == "ndcg" || metric == "recall" {
			metric += "_at_k"
		}
		inputs["experiment_metric"] = metric
		if _, exists := inputs["experiment_cutoff"]; !exists {
			if cutoff, err := strconv.Atoi(matches[2]); err == nil && cutoff >= 1 && cutoff <= 100 {
				inputs["experiment_cutoff"] = cutoff
			}
		}
	}
	for _, metric := range []string{"ndcg_at_k", "recall_at_k", "mrr"} {
		if _, exists := inputs["experiment_metric"]; !exists && strings.Contains(normalized, metric) {
			inputs["experiment_metric"] = metric
			break
		}
	}
	return inputs
}

func optionalIntentFloat(value string, patterns []string) (float64, bool) {
	for _, pattern := range patterns {
		matches := regexp.MustCompile(pattern).FindStringSubmatch(value)
		if len(matches) < 2 {
			continue
		}
		parsed, err := strconv.ParseFloat(matches[1], 64)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}

func buildCustomDatasetBenchmarkNodes(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	uploadedFiles := intent.Entities["uploaded_files"]
	benchmarkInputs := buildCustomBenchmarkInputs(intent)

	audit := newNode(
		"Audit Benchmark Dataset",
		"benchmark_dataset_audit",
		"benchmark_agent",
		nil,
		nil,
		[]string{"dataset_manifest", "benchmark_dataset_audit"},
		true,
		context,
	)
	audit.Inputs = copyBenchmarkInputs(benchmarkInputs)
	audit.Inputs["uploaded_files"] = uploadedFiles

	discover := newNode(
		"Retrieve Benchmark Repository",
		"repo_discovery",
		"coder_agent",
		nil,
		nil,
		[]string{"candidate_repositories", "repo_validation_report", "repo_url"},
		true,
		context,
	)
	discover.Description = buildRepoDiscoveryDescription(context)
	discover.Inputs = buildRepoDiscoveryInputs(intent)

	prepare := newNode(
		"Prepare Benchmark Workspace",
		"repo_prepare",
		"coder_agent",
		[]string{discover.ID},
		[]string{"repo_url", "candidate_repositories", "repo_validation_report"},
		[]string{"workspace_path", "code_file_path", "generated_code", "repo_manifest", "reproduction_mode_report", "dataset_shape_profile"},
		false,
		context,
	)
	prepare.Inputs = map[string]any{
		"requested_reproduction_mode": "smoke",
		"full_reproduction_requested": false,
		"uploaded_files":              workspaceUploadReferences(uploadedFiles),
	}
	split := newNode(
		"Materialize Leakage-Safe Splits",
		"benchmark_split_materialize",
		"benchmark_agent",
		[]string{audit.ID, prepare.ID},
		[]string{"dataset_manifest", "benchmark_dataset_audit", "workspace_path"},
		[]string{
			"benchmark_split_manifest", "benchmark_leakage_report", "benchmark_validation_dataset_manifest",
			"benchmark_public_test_manifest", "benchmark_input_only_preflight_manifest",
			"benchmark_train_path", "benchmark_validation_path", "benchmark_test_features_path",
		},
		false,
		context,
	)
	split.Inputs = copyBenchmarkInputs(benchmarkInputs)
	split.Inputs["uploaded_files"] = uploadedFiles

	freeze := newNode(
		"Freeze Metric And Reward Contract",
		"benchmark_contract_freeze",
		"benchmark_agent",
		[]string{split.ID},
		[]string{"workspace_path", "benchmark_dataset_audit", "benchmark_split_manifest"},
		[]string{
			"benchmark_contract", "benchmark_metric_contract", "benchmark_reward_contract",
			"benchmark_public_evaluator_path", "benchmark_evaluator_manifest", "benchmark_contract_report",
		},
		false,
		context,
	)
	freeze.Inputs = copyBenchmarkInputs(benchmarkInputs)

	generate := newNode(
		"Generate Repository Benchmark Adapter",
		"benchmark_adapter_generate",
		"research_coding_agent",
		[]string{freeze.ID},
		[]string{"benchmark_validation_dataset_manifest", "workspace_path", "repo_manifest", "benchmark_contract"},
		[]string{"benchmark_adapter_plan", "benchmark_adapter_spec", "benchmark_generated_code", "benchmark_code_file_path", "benchmark_adapter_report"},
		false,
		context,
	)
	generate.Inputs = copyBenchmarkInputs(benchmarkInputs)

	resolve := newNode(
		"Resolve Benchmark Dependencies",
		"resolve_dependencies",
		"coder_agent",
		[]string{generate.ID},
		[]string{"workspace_path", "repo_manifest", "benchmark_generated_code", "benchmark_code_file_path"},
		[]string{"dependency_spec"},
		false,
		context,
	)
	prepareRuntime := newNode(
		"Prepare Benchmark Runtime",
		"prepare_runtime",
		"sandbox_agent",
		[]string{resolve.ID},
		[]string{"workspace_path", "dependency_spec"},
		[]string{"runtime_session"},
		false,
		context,
	)
	install := newNode(
		"Install Benchmark Dependencies",
		"install_dependencies",
		"sandbox_agent",
		[]string{prepareRuntime.ID},
		[]string{"workspace_path", "runtime_session", "dependency_spec"},
		[]string{"prepared_runtime", "dependency_install_report"},
		false,
		context,
	)
	preflight := newNode(
		"Preflight And Repair Benchmark Adapter",
		"benchmark_adapter_preflight",
		"research_coding_agent",
		[]string{install.ID},
		[]string{"workspace_path", "benchmark_validation_dataset_manifest", "benchmark_input_only_preflight_manifest", "benchmark_contract", "benchmark_adapter_spec", "benchmark_generated_code", "benchmark_code_file_path", "prepared_runtime"},
		[]string{"validated_benchmark_adapter_spec", "validated_benchmark_generated_code", "validated_benchmark_code_file_path", "benchmark_preflight_report"},
		false,
		context,
	)
	preflight.Inputs = copyBenchmarkInputs(benchmarkInputs)
	execute := newNode(
		"Execute Custom Dataset Benchmark",
		"benchmark_execute",
		"research_coding_agent",
		[]string{preflight.ID},
		[]string{
			"workspace_path", "benchmark_validation_dataset_manifest", "benchmark_public_test_manifest", "benchmark_contract",
			"validated_benchmark_adapter_spec", "validated_benchmark_generated_code", "validated_benchmark_code_file_path", "prepared_runtime",
		},
		[]string{
			"benchmark_run_metrics", "benchmark_run_manifest", "benchmark_predictions_path",
			"benchmark_hidden_predictions_path", "benchmark_hidden_run_manifest", "benchmark_execution_report",
		},
		false,
		context,
	)
	execute.Inputs = copyBenchmarkInputs(benchmarkInputs)
	validate := newNode(
		"Validate Hidden Benchmark Evidence",
		"benchmark_validate",
		"benchmark_agent",
		[]string{execute.ID},
		[]string{
			"workspace_path", "benchmark_contract", "benchmark_split_manifest", "benchmark_run_metrics",
			"benchmark_hidden_predictions_path", "benchmark_hidden_run_manifest",
		},
		[]string{"benchmark_metrics", "benchmark_validation_report"},
		false,
		context,
	)
	report := newNode(
		"Generate Trusted Benchmark Report",
		"framework_report",
		"data_agent",
		[]string{validate.ID},
		[]string{"benchmark_metrics", "benchmark_validation_report", "benchmark_contract", "benchmark_leakage_report", "validated_benchmark_adapter_spec"},
		[]string{"evaluation_report"},
		false,
		context,
	)
	return []*models.TaskNode{audit, discover, prepare, split, freeze, generate, resolve, prepareRuntime, install, preflight, execute, validate, report}
}

func isCustomDatasetBenchmarkIntent(intent models.IntentContext) bool {
	if intent.IntentType == "Custom_Benchmark" || boolEntity(intent.Entities, "needs_custom_benchmark") {
		return true
	}
	if intent.Entities == nil || intent.Entities["uploaded_files"] == nil {
		return false
	}
	normalized := strings.ToLower(intent.RawIntent)
	return hasAny(normalized, "benchmark", "基准测试", "评测", "测评", "跑分")
}

func buildCustomBenchmarkInputs(intent models.IntentContext) map[string]any {
	inputs := map[string]any{
		"benchmark_max_preflight_attempts": 3,
		"benchmark_max_samples": boundedIntentNumber(intent.RawIntent, []string{
			`(?i)(?:最多|最大|max(?:imum)?)\s*(\d+)\s*(?:条|个|rows?|samples?|样本)`,
		}, 1000, 1, 100000),
	}
	for key, patterns := range map[string][]string{
		"benchmark_input_column": {
			`(?i)(?:输入列|input(?:\s+column)?)\s*(?:是|为|[:：=])?\s*["']?([A-Za-z_][A-Za-z0-9_.-]*)`,
		},
		"benchmark_target_column": {
			`(?i)(?:标签列|目标列|label(?:\s+column)?|target(?:\s+column)?)\s*(?:是|为|[:：=])?\s*["']?([A-Za-z_][A-Za-z0-9_.-]*)`,
		},
		"benchmark_group_column": {
			`(?i)(?:分组列|group(?:\s+column)?)\s*(?:是|为|[:：=])?\s*["']?([A-Za-z_][A-Za-z0-9_.-]*)`,
		},
		"benchmark_time_column": {
			`(?i)(?:时间列|time(?:\s+column)?|timestamp(?:\s+column)?)\s*(?:是|为|[:：=])?\s*["']?([A-Za-z_][A-Za-z0-9_.-]*)`,
		},
		"benchmark_primary_metric": {
			`(?i)(?:主指标|primary\s+metric|指标)\s*(?:是|为|[:：=])?\s*["']?([A-Za-z_][A-Za-z0-9_@.-]*)`,
		},
	} {
		for _, pattern := range patterns {
			matches := regexp.MustCompile(pattern).FindStringSubmatch(intent.RawIntent)
			if len(matches) > 1 {
				inputs[key] = matches[1]
				break
			}
		}
	}
	normalized := strings.ToLower(intent.RawIntent)
	for taskType, keywords := range map[string][]string{
		"classification": {"classification", "分类任务", "文本分类"},
		"regression":     {"regression", "回归任务"},
		"generation":     {"generation", "生成任务", "文本生成"},
		"retrieval":      {"retrieval", "检索任务", "rag评测", "rag 评测"},
	} {
		if hasAny(normalized, keywords...) {
			inputs["benchmark_task_type"] = taskType
			break
		}
	}
	if target, ok := optionalIntentFloat(intent.RawIntent, []string{
		`(?i)(?:目标分数|target(?:\s+score)?)\s*(?:达到|不低于|不高于|[:：=])?\s*(-?\d+(?:\.\d+)?)`,
	}); ok && target >= -1e12 && target <= 1e12 {
		inputs["benchmark_target_score"] = target
	}
	if hasAny(strings.ToLower(intent.RawIntent), "latency", "throughput", "推理", "延迟", "吞吐") {
		inputs["benchmark_mode"] = "inference_performance"
		inputs["benchmark_task_type"] = "inference"
	}
	return inputs
}

func copyBenchmarkInputs(source map[string]any) map[string]any {
	copyValues := make(map[string]any, len(source))
	for key, value := range source {
		copyValues[key] = value
	}
	return copyValues
}
