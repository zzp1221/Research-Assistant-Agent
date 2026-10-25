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

	// needs_plot is an output requirement, not a separate intent type.
	if ContainsPlotIntent(ctx.RawIntent) {
		ctx.Entities["needs_plot"] = true
		if _, ok := ctx.Entities["output_mode"]; !ok {
			ctx.Entities["output_mode"] = "plot"
		}
	}

	frameworks := normalizeFrameworks(ctx.Entities["frameworks"])
	if len(frameworks) == 0 {
		frameworks = DetectKnownFrameworks(ctx.RawIntent)
	}
	if len(frameworks) > 0 {
		ctx.Entities["frameworks"] = frameworks
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

func clamp01(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}
