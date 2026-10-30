// Package usage accumulates per-request resource usage (LLM tokens, cost and
// sandbox seconds) so a plan can report what it actually consumed.
package usage

import (
	"context"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

type contextKey struct{}

// ModelUsage is the per-model breakdown of one recorder.
type ModelUsage struct {
	Calls            int     `json:"calls"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	Estimated        bool    `json:"estimated,omitempty"`
	CostSource       string  `json:"cost_source,omitempty"`
}

// Snapshot is an immutable view of the recorder.
type Snapshot struct {
	LLMCalls         int                   `json:"llm_calls"`
	PromptTokens     int                   `json:"prompt_tokens"`
	CompletionTokens int                   `json:"completion_tokens"`
	CostUSD          float64               `json:"cost_usd"`
	Estimated        bool                  `json:"estimated,omitempty"`
	CostSource       string                `json:"cost_source,omitempty"`
	SandboxSeconds   float64               `json:"sandbox_seconds,omitempty"`
	ByModel          map[string]ModelUsage `json:"by_model,omitempty"`
}

// Empty reports whether the recorder observed nothing.
func (s Snapshot) Empty() bool {
	return s.LLMCalls == 0 && s.SandboxSeconds == 0
}

// Recorder accumulates usage for one task or request.
type Recorder struct {
	mu             sync.Mutex
	llmCalls       int
	promptTokens   int
	completion     int
	costUSD        float64
	estimated      bool
	costSource     string
	sandboxSeconds float64
	byModel        map[string]ModelUsage
}

func NewRecorder() *Recorder {
	return &Recorder{byModel: map[string]ModelUsage{}}
}

// WithRecorder attaches a recorder to a context.
func WithRecorder(ctx context.Context, recorder *Recorder) context.Context {
	if recorder == nil {
		return ctx
	}
	return context.WithValue(ctx, contextKey{}, recorder)
}

// FromContext returns the attached recorder, or nil.
func FromContext(ctx context.Context) *Recorder {
	if ctx == nil {
		return nil
	}
	recorder, _ := ctx.Value(contextKey{}).(*Recorder)
	return recorder
}

// RecordLLM records one model invocation using the deployment-wide env price.
// When the provider returns no usage the caller passes estimated=true and token
// counts derived from text length.
func (r *Recorder) RecordLLM(model string, promptTokens, completionTokens int, estimated bool) {
	r.RecordLLMWithPrice(model, promptTokens, completionTokens, estimated, Price{})
}

// Price is the model-specific 1K-token price pair. Zero values fall back to
// the deployment env price.
type Price struct {
	PromptPer1K     float64
	CompletionPer1K float64
}

// RecordLLMWithPrice records one model invocation, preferring the runtime
// profile price over the env fallback. CostSource reports which was used.
func (r *Recorder) RecordLLMWithPrice(model string, promptTokens, completionTokens int, estimated bool, price Price) {
	if r == nil {
		return
	}
	if promptTokens < 0 {
		promptTokens = 0
	}
	if completionTokens < 0 {
		completionTokens = 0
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = "unknown"
	}
	cost, source := EstimateCostWithPrice(promptTokens, completionTokens, price)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.llmCalls++
	r.promptTokens += promptTokens
	r.completion += completionTokens
	r.costUSD += cost
	r.estimated = r.estimated || estimated
	r.costSource = weakerCostSource(r.costSource, source)
	entry := r.byModel[model]
	entry.Calls++
	entry.PromptTokens += promptTokens
	entry.CompletionTokens += completionTokens
	entry.CostUSD += cost
	entry.Estimated = entry.Estimated || estimated
	entry.CostSource = weakerCostSource(entry.CostSource, source)
	r.byModel[model] = entry
}

func weakerCostSource(left, right string) string {
	rank := func(value string) int {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "profile":
			return 3
		case "env":
			return 2
		case "estimated":
			return 1
		case "unknown":
			return 0
		default:
			return -1
		}
	}
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" {
		return right
	}
	if right == "" {
		return left
	}
	if rank(left) <= rank(right) {
		return left
	}
	return right
}

// RecordSandbox adds sandbox execution seconds.
func (r *Recorder) RecordSandbox(seconds float64) {
	if r == nil || seconds <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sandboxSeconds += seconds
}

// Snapshot returns an immutable copy.
func (r *Recorder) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	byModel := make(map[string]ModelUsage, len(r.byModel))
	for key, value := range r.byModel {
		byModel[key] = value
	}
	return Snapshot{
		LLMCalls:         r.llmCalls,
		PromptTokens:     r.promptTokens,
		CompletionTokens: r.completion,
		CostUSD:          r.costUSD,
		Estimated:        r.estimated,
		CostSource:       r.costSource,
		SandboxSeconds:   r.sandboxSeconds,
		ByModel:          byModel,
	}
}

// Merge folds another snapshot into this recorder.
func (r *Recorder) Merge(snapshot Snapshot) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.llmCalls += snapshot.LLMCalls
	r.promptTokens += snapshot.PromptTokens
	r.completion += snapshot.CompletionTokens
	r.costUSD += snapshot.CostUSD
	r.estimated = r.estimated || snapshot.Estimated
	r.costSource = weakerCostSource(r.costSource, snapshot.CostSource)
	r.sandboxSeconds += snapshot.SandboxSeconds
	for key, value := range snapshot.ByModel {
		entry := r.byModel[key]
		entry.Calls += value.Calls
		entry.PromptTokens += value.PromptTokens
		entry.CompletionTokens += value.CompletionTokens
		entry.CostUSD += value.CostUSD
		entry.Estimated = entry.Estimated || value.Estimated
		entry.CostSource = weakerCostSource(entry.CostSource, value.CostSource)
		r.byModel[key] = entry
	}
}

// EstimateTokens approximates a token count for mixed English/CJK text. It is
// only used when the provider response omits a usage block, and every consumer
// marks such values as estimated.
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	ascii := 0
	cjk := 0
	other := 0
	for _, r := range text {
		switch {
		case r <= unicode.MaxASCII:
			ascii++
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			cjk++
		default:
			other++
		}
	}
	tokens := float64(ascii)/4.0 + float64(cjk)/1.5 + float64(other)/3.0
	if tokens < 1 && utf8.RuneCountInString(text) > 0 {
		return 1
	}
	return int(math.Ceil(tokens))
}

// EstimateCostUSD converts tokens to USD using LLM_PRICE_PROMPT_PER_1K_USD and
// LLM_PRICE_COMPLETION_PER_1K_USD. Unset prices yield zero cost so local
// deployments do not report fake spend.
func EstimateCostUSD(promptTokens, completionTokens int) float64 {
	cost, _ := EstimateCostWithPrice(promptTokens, completionTokens, Price{})
	return cost
}

// EstimateCostWithPrice converts tokens to USD using an explicit model price
// first, then the env fallback. It returns the amount and a cost source:
// profile, env, or unknown.
func EstimateCostWithPrice(promptTokens, completionTokens int, price Price) (float64, string) {
	promptPrice := price.PromptPer1K
	completionPrice := price.CompletionPer1K
	source := "profile"
	if promptPrice == 0 && completionPrice == 0 {
		promptPrice = envFloat("LLM_PRICE_PROMPT_PER_1K_USD", 0)
		completionPrice = envFloat("LLM_PRICE_COMPLETION_PER_1K_USD", 0)
		source = "env"
	}
	if promptPrice == 0 && completionPrice == 0 {
		return 0, "unknown"
	}
	cost := float64(promptTokens)/1000.0*promptPrice + float64(completionTokens)/1000.0*completionPrice
	return math.Round(cost*1e8) / 1e8, source
}

func envFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
