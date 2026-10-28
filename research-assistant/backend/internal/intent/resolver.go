package intent

import (
	"context"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

// Resolver modes. The default is rule so local tests and deployments without
// the optional intent service keep the previous deterministic behavior.
const (
	ModeRule   = "rule"
	ModeLLM    = "llm"
	ModeHybrid = "hybrid"
	ModeShadow = "shadow"
)

// Resolver combines the deterministic rule context with an optional LLM
// classifier. It never lets the model alone trigger the expensive/high-risk
// task classes (AutoResearch, Custom_Benchmark).
type Resolver struct {
	Mode       string
	Threshold  float64
	Classifier Classifier
}

func NewResolverFromEnv() *Resolver {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("INTENT_ROUTER_MODE")))
	if mode == "" {
		mode = ModeRule
	}
	threshold := 0.65
	if raw := strings.TrimSpace(os.Getenv("INTENT_LLM_CONFIDENCE_THRESHOLD")); raw != "" {
		if value, err := strconv.ParseFloat(raw, 64); err == nil && value >= 0 && value <= 1 {
			threshold = value
		}
	}
	return &Resolver{
		Mode:       mode,
		Threshold:  threshold,
		Classifier: NewHTTPClassifierFromEnv(),
	}
}

func (r *Resolver) Resolve(ctx context.Context, raw string, rule models.IntentContext) models.IntentContext {
	if r == nil {
		return postValidate(rule)
	}
	mode := strings.ToLower(strings.TrimSpace(r.Mode))
	if mode == "" || mode == ModeRule {
		return postValidate(rule)
	}
	if mode == ModeShadow {
		r.runShadow(raw, rule)
		return postValidate(rule)
	}
	if mode != ModeLLM && mode != ModeHybrid {
		log.Printf("[intent] unknown INTENT_ROUTER_MODE=%q, using rule context", r.Mode)
		return postValidate(rule)
	}
	if mode == ModeHybrid && r.ruleAuthoritative(raw, rule) {
		rule.Metadata = ensureMetadata(rule.Metadata)
		rule.Metadata["intent_router_mode"] = "rule_fast_path"
		return postValidate(rule)
	}
	if r.Classifier == nil {
		log.Printf("[intent] %s mode requested but INTENT_SERVICE_URL is empty; using rule context", mode)
		return postValidate(rule)
	}

	classified, err := r.Classifier.Classify(ctx, raw)
	if err != nil {
		log.Printf("[intent] LLM classifier failed, falling back to rule: %v", err)
		return postValidate(rule)
	}
	if classified == nil || classified.Confidence < r.Threshold {
		confidence := 0.0
		if classified != nil {
			confidence = classified.Confidence
		}
		log.Printf("[intent] LLM confidence %.2f below threshold %.2f, using rule context", confidence, r.Threshold)
		return postValidate(rule)
	}
	return mergeAndValidate(rule, *classified, mode)
}

func (r *Resolver) ruleAuthoritative(raw string, rule models.IntentContext) bool {
	switch rule.IntentType {
	case "AutoResearch", "Custom_Benchmark":
		return true
	case "Paper_Reproduction":
		return containsStrongPaperSignal(raw)
	default:
		return false
	}
}

func (r *Resolver) runShadow(raw string, rule models.IntentContext) {
	if r.Classifier == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		classified, err := r.Classifier.Classify(ctx, raw)
		if err != nil {
			log.Printf("[intent-shadow] classifier failed: %v", err)
			return
		}
		if classified == nil {
			return
		}
		log.Printf("[intent-shadow] rule=%s llm=%s confidence=%.2f raw=%q", rule.IntentType, classified.IntentType, classified.Confidence, raw)
	}()
}

func mergeAndValidate(rule models.IntentContext, classified Classification, mode string) models.IntentContext {
	llmType := normalizeIntentType(classified.IntentType)
	if llmType == "" {
		log.Printf("[intent] unsupported LLM intent_type=%q, using rule context", classified.IntentType)
		return postValidate(rule)
	}
	// High-risk intents must be supported by a rule signal; the model cannot
	// upgrade a generic request into an expensive AutoResearch/Benchmark plan.
	// The rule context is post-validated first so its own misclassification is
	// repaired before the comparison.
	if (llmType == "AutoResearch" || llmType == "Custom_Benchmark") && rule.IntentType != llmType {
		validatedRule := postValidate(rule)
		log.Printf("[intent] model proposed high-risk intent %s without rule support, keeping rule intent %s", llmType, validatedRule.IntentType)
		return validatedRule
	}

	merged := rule
	merged.Entities = ensureEntities(merged.Entities)
	merged.Metadata = ensureMetadata(merged.Metadata)
	merged.IntentType = llmType
	merged.Confidence = clamp01(classified.Confidence)
	merged.Source = "llm"
	merged.Reasoning = strings.TrimSpace(classified.Reasoning)
	if merged.Reasoning == "" {
		merged.Reasoning = "llm intent classification"
	}
	merged.Metadata["intent_router_mode"] = mode
	merged.Metadata["intent_model"] = classified.Model
	merged.Metadata["intent_latency_ms"] = classified.LatencyMS
	merged.Metadata["intent_attempts"] = classified.Attempts
	// Complexity is merged conservatively: the model may raise the level but
	// never lower a rule-derived level. Unknown enum values fall back to L2.
	merged.Complexity = mergeComplexityLevels(merged.Complexity, classified.Complexity)
	merged.Capabilities = normalizeCapabilities(append(merged.Capabilities, classified.Capabilities...))
	merged.RouteHint = normalizeRouteHint(classified.RouteHint)
	merged.ComplexityConfidence = clamp01(classified.ComplexityConfidence)
	merged.ComplexityReason = strings.TrimSpace(classified.ComplexityReason)
	if merged.RouteHint == "" {
		merged.RouteHint = RouteHintForComplexity(merged.Complexity)
	}
	for key, value := range classified.Entities {
		merged.Entities[key] = value
	}
	merged = postValidate(merged)
	// A custom benchmark without uploaded data is just a code-execution task;
	// do not let the model route it into the expensive benchmark pipeline.
	if merged.IntentType == "Custom_Benchmark" && !hasUploadedFiles(merged.Entities) {
		merged.IntentType = "Code_Execution"
		appendReasoning(&merged, "custom benchmark without uploaded data downgraded to Code_Execution")
	}
	return merged
}

func postValidate(ctx models.IntentContext) models.IntentContext {
	ctx.Entities = ensureEntities(ctx.Entities)
	ctx.Metadata = ensureMetadata(ctx.Metadata)
	ctx.Complexity = normalizeComplexity(ctx.Complexity)
	ctx.RouteHint = normalizeRouteHint(ctx.RouteHint)
	ctx.Capabilities = normalizeCapabilities(ctx.Capabilities)

	// needs_plot is an output requirement, not a separate intent type.
	if ContainsPlotIntent(ctx.RawIntent) {
		ctx.Entities["needs_plot"] = true
		if _, ok := ctx.Entities["output_mode"]; !ok {
			ctx.Entities["output_mode"] = "plot"
		}
	}

	frameworks := normalizeFrameworks(ctx.Entities["frameworks"])
	// Never trust arbitrary LLM entities: only real framework names from the
	// whitelist may drive Framework_Evaluation. This prevents "bisect/sorted"
	// or any other library/function name from being treated as a framework.
	frameworks = filterKnownFrameworks(frameworks)
	if len(frameworks) == 0 {
		frameworks = DetectKnownFrameworks(ctx.RawIntent)
	}
	if len(frameworks) > 0 {
		ctx.Entities["frameworks"] = frameworks
	} else {
		delete(ctx.Entities, "frameworks")
	}

	switch ctx.IntentType {
	case "Framework_Evaluation":
		if len(frameworks) == 0 && ContainsPlotIntent(ctx.RawIntent) {
			ctx.IntentType = "Code_Execution"
			appendReasoning(&ctx, "no framework entity for a plotting request; downgraded to Code_Execution")
		} else if len(frameworks) == 0 && !ContainsFrameworkContext(ctx.RawIntent) {
			// A generic "compare performance" request over code is a code task,
			// never a framework benchmark (D3).
			if ContainsBenchmarkIntent(ctx.RawIntent) && ContainsCodeIntent(ctx.RawIntent) {
				ctx.IntentType = "Code_Execution"
				appendReasoning(&ctx, "generic performance comparison over code; downgraded to Code_Execution")
			} else {
				ctx.IntentType = "General"
				appendReasoning(&ctx, "generic comparison without framework context; downgraded to General")
			}
		}
	case "Code_Execution":
		if len(frameworks) > 0 && ContainsBenchmarkIntent(ctx.RawIntent) {
			ctx.IntentType = "Framework_Evaluation"
			appendReasoning(&ctx, "framework entities plus benchmark signal; upgraded to Framework_Evaluation")
		}
	case "General":
		if ContainsPlotIntent(ctx.RawIntent) {
			ctx.IntentType = "Code_Execution"
			appendReasoning(&ctx, "plot request without a more specific intent; upgraded to Code_Execution")
		} else if len(frameworks) > 0 && ContainsComparisonIntent(ctx.RawIntent) {
			ctx.IntentType = "Framework_Evaluation"
			appendReasoning(&ctx, "framework entities plus comparison signal; upgraded to Framework_Evaluation")
		} else if ContainsBenchmarkIntent(ctx.RawIntent) && ContainsCodeIntent(ctx.RawIntent) {
			ctx.IntentType = "Code_Execution"
			appendReasoning(&ctx, "performance comparison over code; upgraded to Code_Execution")
		}
	}
	return ctx
}

func normalizeIntentType(value string) string {
	switch strings.TrimSpace(value) {
	case "General", "Code_Execution", "Paper_Reproduction", "Framework_Evaluation", "AutoResearch", "Custom_Benchmark":
		return strings.TrimSpace(value)
	case "Literature_Review", "Data_Analysis":
		return "General"
	default:
		return ""
	}
}

// filterKnownFrameworks keeps only whitelisted framework names, canonicalising
// common aliases. Unknown names are dropped instead of being passed through.
func filterKnownFrameworks(values []string) []string {
	known := DetectKnownFrameworks(strings.Join(values, " "))
	out := make([]string, 0, len(values))
	for _, value := range values {
		canonical := canonicalFrameworkName(value)
		if canonical == "" {
			continue
		}
		if !containsString(out, canonical) {
			out = append(out, canonical)
		}
	}
	for _, item := range known {
		if !containsString(out, item) {
			out = append(out, item)
		}
	}
	return out
}

func canonicalFrameworkName(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, " ", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, "-", "")
	switch normalized {
	case "langchain":
		return "langchain"
	case "llamaindex", "llama_index", "llamaindexai":
		return "llamaindex"
	case "haystack", "haystackai":
		return "haystack"
	case "dspy":
		return "dspy"
	case "autogen", "autogenai":
		return "autogen"
	case "crewai":
		return "crewai"
	case "langgraph":
		return "langgraph"
	default:
		return ""
	}
}

func normalizeFrameworks(value any) []string {
	var raw []string
	switch typed := value.(type) {
	case []string:
		raw = typed
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok {
				raw = append(raw, text)
			}
		}
	case string:
		if strings.TrimSpace(typed) != "" {
			raw = []string{typed}
		}
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !containsString(out, item) {
			out = append(out, item)
		}
	}
	return out
}

func containsStrongPaperSignal(text string) bool {
	lower := strings.ToLower(text)
	for _, keyword := range []string{"复现", "reproduce", "replicate", "论文复现", "paper reproduction", "papers with code", "arxiv", "实现算法", "复现代码"} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func hasUploadedFiles(entities map[string]any) bool {
	if entities == nil {
		return false
	}
	switch value := entities["uploaded_files"].(type) {
	case []map[string]any:
		return len(value) > 0
	case []any:
		return len(value) > 0
	}
	return false
}

func ensureEntities(entities map[string]any) map[string]any {
	if entities == nil {
		return map[string]any{}
	}
	return entities
}

func ensureMetadata(metadata map[string]any) map[string]any {
	if metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func appendReasoning(ctx *models.IntentContext, detail string) {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return
	}
	if strings.TrimSpace(ctx.Reasoning) == "" {
		ctx.Reasoning = detail
		return
	}
	ctx.Reasoning = strings.TrimSpace(ctx.Reasoning) + "; " + detail
}

// complexityRank orders L0..L4. Unknown values rank -1.
func complexityRank(value string) int {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case "L0":
		return 0
	case "L1":
		return 1
	case "L2":
		return 2
	case "L3":
		return 3
	case "L4":
		return 4
	default:
		return -1
	}
}

// normalizeComplexity validates an enum value. Empty stays empty (the rule
// router computes it); unknown non-empty values conservatively become L2.
func normalizeComplexity(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if complexityRank(value) < 0 {
		return "L2"
	}
	return value
}

// mergeComplexityLevels keeps the higher of the rule and model levels so the
// model can never downgrade a task.
func mergeComplexityLevels(ruleLevel, llmLevel string) string {
	ruleLevel = normalizeComplexity(ruleLevel)
	llmLevel = normalizeComplexity(llmLevel)
	if ruleLevel == "" {
		return llmLevel
	}
	if llmLevel == "" {
		return ruleLevel
	}
	if complexityRank(llmLevel) > complexityRank(ruleLevel) {
		return llmLevel
	}
	return ruleLevel
}

// normalizeRouteHint validates the route hint enum.
func normalizeRouteHint(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fast_path", "standard_template", "agent_planner":
		return strings.ToLower(strings.TrimSpace(value))
	case "":
		return ""
	default:
		return "standard_template"
	}
}

// RouteHintForComplexity maps a complexity level to the default route hint.
func RouteHintForComplexity(level string) string {
	switch complexityRank(level) {
	case 0, 1:
		return "fast_path"
	case 3, 4:
		return "agent_planner"
	default:
		return "standard_template"
	}
}

// normalizeCapabilities trims, dedupes and bounds the capability list.
func normalizeCapabilities(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
		if len(out) >= 16 {
			break
		}
	}
	return out
}

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
