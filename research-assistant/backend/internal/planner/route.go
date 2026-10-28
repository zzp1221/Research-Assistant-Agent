package planner

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"research-assistant-backend/internal/models"
)

// ComplexityLevel is the L0-L4 task complexity taxonomy.
type ComplexityLevel string

const (
	ComplexityL0 ComplexityLevel = "L0"
	ComplexityL1 ComplexityLevel = "L1"
	ComplexityL2 ComplexityLevel = "L2"
	ComplexityL3 ComplexityLevel = "L3"
	ComplexityL4 ComplexityLevel = "L4"
)

// RouteDecision is the final, explainable routing decision for one plan.
type RouteDecision struct {
	Complexity   ComplexityLevel
	Capabilities []string
	RouteHint    string // fast_path | standard_template | agent_planner
	Confidence   float64
	Source       string // rule | llm | merged | fallback | disabled
	Reason       string
	Variant      string // simple | standard | full | light
}

// IsZero reports whether the decision is empty (no routing applied).
func (d RouteDecision) IsZero() bool { return d.Complexity == "" }

// ComplexityConfirmer is the optional lightweight LLM confirmation hook for
// ambiguous scores. It is disabled by default: the repository has no separate
// lightweight routing endpoint, so rules stay authoritative.
type ComplexityConfirmer interface {
	Confirm(ctx context.Context, intent models.IntentContext, rule RouteDecision) (RouteDecision, error)
}

// ComplexityRouter turns intent + attachment/research context into a
// RouteDecision. Rules are deterministic; an optional confirmer may only raise
// the complexity, never lower it.
type ComplexityRouter struct {
	Enabled             bool
	Confirmer           ComplexityConfirmer
	ConfidenceThreshold float64
}

// NewComplexityRouterFromEnv builds the router from deployment settings.
func NewComplexityRouterFromEnv() *ComplexityRouter {
	enabled := true
	if raw := strings.TrimSpace(os.Getenv("COMPLEXITY_ROUTER_ENABLED")); raw != "" {
		enabled = !strings.EqualFold(raw, "false") && raw != "0"
	}
	return &ComplexityRouter{Enabled: enabled, ConfidenceThreshold: 0.7}
}

// Decide computes the final route. Attachments and researchContext are the
// post-resolution context the intent classifier cannot see.
func (r *ComplexityRouter) Decide(ctx context.Context, intent models.IntentContext, attachments []map[string]any, researchContext *models.ResearchContext) RouteDecision {
	if r == nil || !r.Enabled {
		return RouteDecision{
			Complexity: ComplexityL2,
			RouteHint:  "standard_template",
			Variant:    "standard",
			Source:     "disabled",
			Confidence: 0.5,
			Reason:     "complexity router disabled",
		}
	}
	score, signals := scoreComplexity(intent, attachments, researchContext)
	level := levelForScore(score, intent)
	variant := variantForLevel(level, intent.IntentType)
	decision := RouteDecision{
		Complexity:   level,
		Capabilities: signals,
		RouteHint:    routeHintForLevel(level),
		Confidence:   confidenceForScore(score),
		Source:       "rule",
		Reason:       strings.Join(signals, ","),
		Variant:      variant,
	}

	// Merge the intent-level complexity (Python LLM or resolver). The model may
	// raise but never lower the rule level.
	if llmLevel := normalizeLevel(intent.Complexity); llmLevel != "" {
		if levelRank(llmLevel) > levelRank(decision.Complexity) {
			decision.Complexity = llmLevel
			decision.Variant = variantForLevel(llmLevel, intent.IntentType)
			decision.RouteHint = routeHintForLevel(llmLevel)
			decision.Source = "merged"
			decision.Reason = strings.TrimSpace(decision.Reason + ",llm_raised")
		} else if levelRank(llmLevel) == levelRank(decision.Complexity) {
			decision.Source = "merged"
		}
	}

	// Optional lightweight confirmation only in the ambiguous band and only
	// when the intent-level confidence is low.
	if r.Confirmer != nil && score >= 5 && score <= 7 && intent.ComplexityConfidence < r.ConfidenceThreshold {
		if confirmed, err := r.Confirmer.Confirm(ctx, intent, decision); err == nil {
			if levelRank(confirmed.Complexity) > levelRank(decision.Complexity) {
				decision.Complexity = confirmed.Complexity
				decision.Variant = variantForLevel(confirmed.Complexity, intent.IntentType)
				decision.RouteHint = routeHintForLevel(confirmed.Complexity)
			}
			decision.Source = "merged"
			if confirmed.Reason != "" {
				decision.Reason = confirmed.Reason
			}
			decision.Confidence = confirmed.Confidence
		}
	}
	return decision
}

func levelRank(level ComplexityLevel) int {
	switch level {
	case ComplexityL0:
		return 0
	case ComplexityL1:
		return 1
	case ComplexityL2:
		return 2
	case ComplexityL3:
		return 3
	case ComplexityL4:
		return 4
	default:
		return -1
	}
}

func normalizeLevel(value string) ComplexityLevel {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch value {
	case "L0", "L1", "L2", "L3", "L4":
		return ComplexityLevel(value)
	default:
		return ""
	}
}

func routeHintForLevel(level ComplexityLevel) string {
	switch levelRank(level) {
	case 0, 1:
		return "fast_path"
	case 3, 4:
		return "agent_planner"
	default:
		return "standard_template"
	}
}

// variantForLevel maps complexity to a template variant. Paper_Reproduction
// uses "light" for its simple variant to match the design vocabulary.
func variantForLevel(level ComplexityLevel, intentType string) string {
	switch levelRank(level) {
	case 0, 1:
		if intentType == "Paper_Reproduction" {
			return "light"
		}
		return "simple"
	case 2:
		return "standard"
	default:
		return "full"
	}
}

func confidenceForScore(score int) float64 {
	if score >= 5 && score <= 7 {
		return 0.6
	}
	return 0.9
}

// scoreComplexity implements the rule table from the design doc. Weights are
// initial values and are calibrated by backend/evals/route_eval_dataset.json.
func scoreComplexity(intent models.IntentContext, attachments []map[string]any, researchContext *models.ResearchContext) (int, []string) {
	score := 0
	signals := make([]string, 0, 8)
	add := func(points int, name string) {
		score += points
		signals = append(signals, name)
	}

	hasAttachment := len(attachments) > 0
	if _, ok := intent.Entities["uploaded_files"]; ok {
		hasAttachment = true
	}
	repoURL := ""
	if researchContext != nil && researchContext.Paper != nil {
		repoURL = strings.TrimSpace(researchContext.Paper.RepoURL)
	}
	if repoURL == "" {
		if value, ok := intent.Entities["repo_url"].(string); ok {
			repoURL = strings.TrimSpace(value)
		}
	}
	if hasAttachment {
		add(3, "attachment")
	}
	if intent.Entities["paper_metadata"] != nil {
		add(3, "paper")
	} else if isPaperAnalysisIntent(intent) {
		add(3, "paper_analysis")
	}
	if repoURL != "" {
		add(3, "repo")
	}
	if hasDatasetSignal(intent) {
		add(3, "dataset")
	}

	normalized := strings.ToLower(intent.RawIntent)
	// Only real, whitelisted framework names count. frameworkNamesFromIntent
	// substitutes placeholders for display purposes, which must not inflate the
	// complexity score.
	frameworks := frameworkLabels(normalized)
	switch raw := intent.Entities["frameworks"].(type) {
	case []string:
		if len(raw) > 0 {
			frameworks = raw
		}
	case []any:
		if len(raw) > 0 {
			names := make([]string, 0, len(raw))
			for _, item := range raw {
				names = append(names, strings.TrimSpace(fmt.Sprint(item)))
			}
			frameworks = names
		}
	}
	if len(frameworks) >= 2 {
		add(3, "multi_framework")
	}
	multiStep := hasMultiStepStructure(normalized)
	if multiStep {
		add(2, "multi_stage")
	}
	if hasAdvancedArtifacts(normalized) {
		add(2, "advanced_artifacts")
	}
	if hasMultiMetrics(normalized) {
		add(3, "multi_metric")
	}
	if hasResearchAction(normalized) {
		add(3, "research_action")
	}
	if hasCustomPipeline(normalized) {
		add(3, "custom_pipeline")
	}
	if utf8.RuneCountInString(intent.RawIntent) > 500 || len(intent.Entities) > 12 {
		add(1, "long_intent")
	}
	if isSimpleRequest(normalized) && !hasAttachment && !multiStep {
		score -= 2
		signals = append(signals, "simple")
	}
	return score, signals
}

func levelForScore(score int, intent models.IntentContext) ComplexityLevel {
	level := ComplexityL0
	switch {
	case score >= 9:
		level = ComplexityL4
	case score >= 6:
		level = ComplexityL3
	case score >= 3:
		level = ComplexityL2
	default:
		if hasToolAction(strings.ToLower(intent.RawIntent)) {
			level = ComplexityL1
		}
	}
	// Paper reproduction is never "light" by default: a wrong downgrade loses
	// the claim/evidence pipeline. Only an explicit simple/light signal would
	// justify L1, and that signal is not currently trusted.
	if intent.IntentType == "Paper_Reproduction" && levelRank(level) < 2 {
		return ComplexityL2
	}
	// Code execution with a plot needs the standard runtime preparation chain
	// (prepare_runtime/install_dependencies) that the simple template does not
	// include; the repository's plot contract wins over the design's generic
	// "single plot = L1" example.
	if intent.IntentType == "Code_Execution" && levelRank(level) < 2 {
		lower := strings.ToLower(intent.RawIntent)
		if boolEntity(intent.Entities, "needs_plot") || strings.Contains(lower, "画") || strings.Contains(lower, "plot") || strings.Contains(lower, "chart") {
			return ComplexityL2
		}
	}
	return level
}

func hasDatasetSignal(intent models.IntentContext) bool {
	if _, ok := intent.Entities["dataset_path"]; ok {
		return true
	}
	if _, ok := intent.Entities["dataset"]; ok {
		return true
	}
	lower := strings.ToLower(intent.RawIntent)
	return hasAny(lower, "数据集", "dataset", "csv", "tsv", "jsonl")
}

func hasMultiStepStructure(lower string) bool {
	return hasAny(lower, "先", "再", "然后", "最后", "分别", "step 1", "step1", "first,", "then,", "finally")
}

func hasAdvancedArtifacts(lower string) bool {
	return hasAny(lower, "claim_evidence", "claim evidence", "benchmark_contract", "benchmark contract", "hidden holdout", "custom evaluator", "证据图谱", "自定义评估")
}

func hasResearchAction(lower string) bool {
	return hasAny(lower, "总结", "综述", "调研", "报告", "梳理", "summary", "survey", "report", "research")
}

func hasMultiMetrics(lower string) bool {
	return hasAny(lower, "多指标", "多个指标", "多数据集", "多个数据集", "multiple metrics", "multiple datasets", "多种子", "多个 seed", "3 个 seed", "三个 seed")
}

func hasCustomPipeline(lower string) bool {
	return hasAny(lower, "自定义流程", "自定义评估", "自定义实验", "special pipeline", "custom pipeline", "custom evaluator")
}

func isSimpleRequest(lower string) bool {
	return hasAny(lower, "解释", "翻译", "计算", "画图", "画一个", "绘图", "explain", "translate", "calculate", "plot", "chart")
}

func hasToolAction(lower string) bool {
	return hasAny(lower, "画图", "画一个", "绘图", "计算", "运行", "执行", "写代码", "生成代码", "写一段", "生成一个", "打印", "用 python", "plot", "chart", "calculate", "run code")
}
