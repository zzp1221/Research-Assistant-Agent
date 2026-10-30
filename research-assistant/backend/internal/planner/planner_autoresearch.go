package planner

import (
	"regexp"
	"research-assistant-backend/internal/models"
	"strings"
)

func buildAutoResearchNodes(intent models.IntentContext) []*models.TaskNode {
	context := intent.RawIntent
	inputs := buildAutoResearchInputs(intent)
	uploadedFiles := intent.Entities["uploaded_files"]

	discover := newNode(
		"Retrieve AutoResearch Repository",
		"repo_discovery",
		"coder_agent",
		nil,
		nil,
		[]string{"candidate_repositories", "repo_validation_report", "repo_url"},
		false,
		context,
	)
	discover.Description = "任务目标: 定位用户指定的 AutoResearch 目标仓库 / Retrieve the repository to optimize\n具体要求: 优先采用用户明确提供的 GitHub URL，并输出 candidate_repositories、repo_validation_report、repo_url。\n用户原始意图: " + context
	discover.Inputs = buildRepoDiscoveryInputs(intent)

	prepare := newNode(
		"Prepare AutoResearch Workspace",
		"repo_prepare",
		"coder_agent",
		[]string{discover.ID},
		[]string{"repo_url", "candidate_repositories", "repo_validation_report"},
		[]string{"workspace_path", "code_file_path", "generated_code", "repo_manifest", "reproduction_mode_report", "dataset_shape_profile"},
		false,
		context,
	)
	prepare.Inputs = map[string]any{
		"requested_reproduction_mode":    "auto",
		"full_reproduction_requested":    false,
		"skip_reproduction_smoke_runner": true,
	}
	if revision := strings.TrimSpace(stringEntity(intent.Entities, "repository_revision", "")); revision != "" {
		prepare.Inputs["repository_revision"] = revision
	}
	if uploadedFiles != nil {
		prepare.Inputs["uploaded_files"] = workspaceUploadReferences(uploadedFiles)
	}

	freeze := newNode(
		"Freeze AutoResearch Contract",
		"autoresearch_spec",
		"research_coding_agent",
		[]string{prepare.ID},
		[]string{"workspace_path", "repo_manifest"},
		[]string{"research_spec", "research_spec_report", "dependency_spec"},
		false,
		context,
	)
	freeze.Inputs = copyBenchmarkInputs(inputs)

	prepareRuntime := newNode(
		"Prepare AutoResearch Runtime",
		"prepare_runtime",
		"sandbox_agent",
		[]string{freeze.ID},
		[]string{"workspace_path", "dependency_spec"},
		[]string{"runtime_session"},
		false,
		context,
	)
	install := newNode(
		"Install AutoResearch Dependencies",
		"install_dependencies",
		"sandbox_agent",
		[]string{prepareRuntime.ID},
		[]string{"workspace_path", "runtime_session", "dependency_spec"},
		[]string{"prepared_runtime", "dependency_install_report"},
		false,
		context,
	)
	run := newNode(
		"Run Bounded AutoResearch Loop",
		"autoresearch_run",
		"research_coding_agent",
		[]string{install.ID},
		[]string{"workspace_path", "prepared_runtime", "research_spec"},
		[]string{"research_trial_ledger", "research_best_candidate", "research_run_report"},
		false,
		context,
	)
	run.Inputs = copyBenchmarkInputs(inputs)
	if wallSeconds, ok := inputs["autoresearch_max_wall_seconds"].(int); ok {
		run.TimeoutSeconds = wallSeconds + 30
	}
	validate := newNode(
		"Validate AutoResearch Candidate",
		"autoresearch_validate",
		"research_coding_agent",
		[]string{run.ID},
		[]string{"workspace_path", "prepared_runtime", "research_spec", "research_trial_ledger", "research_best_candidate"},
		[]string{"research_validation_report", "research_best_metrics"},
		false,
		context,
	)
	validate.RetryLimit = 0
	report := newNode(
		"Summarize AutoResearch Evidence",
		"verify_result",
		"data_agent",
		[]string{validate.ID},
		[]string{"research_spec_report", "research_run_report", "research_trial_ledger", "research_validation_report", "research_best_metrics"},
		[]string{"evaluation_report"},
		false,
		context,
	)
	return []*models.TaskNode{discover, prepare, freeze, prepareRuntime, install, run, validate, report}
}

func isAutoResearchIntent(intent models.IntentContext) bool {
	if intent.IntentType == "AutoResearch" || boolEntity(intent.Entities, "needs_autoresearch") {
		return true
	}
	normalized := strings.ToLower(intent.RawIntent)
	if hasAny(normalized, "autoresearch", "auto research", "自主研究", "自动研究", "自动实验", "持续实验", "实验循环") {
		return true
	}
	return strings.Contains(normalized, "自动优化") &&
		hasAny(normalized, "实验", "试验", "指标", "评测", "仓库", "模型", "repo", "trial", "metric", "benchmark")
}

func buildAutoResearchInputs(intent models.IntentContext) map[string]any {
	maxTrials := boundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:最多|最大|max(?:imum)?)\s*(\d+)\s*(?:次|轮|个)?\s*(?:试验|实验|trials?|experiments?)`,
		`(?i)(\d+)\s*(?:次|轮)\s*(?:autoresearch|自动实验|实验循环)`,
	}, autoResearchPlannerDefaultTrials, 1, autoResearchPlannerMaxTrials)
	maxWallMinutes := boundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:总耗时|总时长|wall(?:[- ]?time)?|预算)\s*(?:不超过|最多|[:：]?)?\s*(\d+)\s*(?:minutes?|分钟)`,
	}, 15, 1, 60)
	inputs := map[string]any{
		"autoresearch_max_trials":       maxTrials,
		"autoresearch_max_wall_seconds": maxWallMinutes * 60,
	}
	if validationRuns, ok := optionalBoundedIntentNumber(intent.RawIntent, []string{
		`(?i)(?:重复验证|独立复验|重复复验|validation\s+runs?)\s*(?:最多|不超过|[:：=]?)?\s*(\d+)\s*(?:次|轮|runs?)?`,
		`(?i)(\d+)\s*(?:次|轮|runs?)\s*(?:重复验证|独立复验|重复复验|validation)`,
	}, 1, 5); ok {
		inputs["autoresearch_validation_runs"] = validationRuns
	}
	if path := autoResearchSpecPath(intent); path != "" {
		inputs["autoresearch_spec_path"] = path
	}
	return inputs
}

const (
	autoResearchPlannerDefaultTrials = 3
	autoResearchPlannerMaxTrials     = 8
)

func autoResearchSpecPath(intent models.IntentContext) string {
	if path := strings.TrimSpace(stringEntity(intent.Entities, "autoresearch_spec_path", "")); path != "" {
		return path
	}
	pattern := regexp.MustCompile(`(?i)([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+\.json|autoresearch\.json)`)
	if match := pattern.FindStringSubmatch(intent.RawIntent); len(match) > 1 {
		return match[1]
	}
	return ""
}
