package llmclient

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/usage"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type modelNameKey struct{}

// WithModelName pins the model identifier recorded for one call. Runtime
// profiles resolve different models per plan, so the env-only name is not
// enough for the usage ledger.
func WithModelName(ctx context.Context, name string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, modelNameKey{}, name)
}

// ModelName returns the configured model identifier used for metrics and the
// usage ledger. A per-call override wins; otherwise check the common env
// variables in priority order.
func ModelName(ctx ...context.Context) string {
	if len(ctx) > 0 && ctx[0] != nil {
		if name, ok := ctx[0].Value(modelNameKey{}).(string); ok && strings.TrimSpace(name) != "" {
			return strings.TrimSpace(name)
		}
	}
	for _, key := range []string{"OPENAI_MODEL_NAME", "LLM_MODEL", "OPENAI_MODEL", "MODEL_NAME"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "unknown"
}

// RecordUsage records one model invocation into the context recorder, metrics
// and cost ledger. The provider usage block is authoritative; when it is missing
// tokens are estimated from text length and marked estimated.
func RecordUsage(ctx context.Context, input []*schema.Message, message *schema.Message, latency time.Duration) {
	promptTokens := 0
	if message == nil || message.ResponseMeta == nil || message.ResponseMeta.Usage == nil {
		for _, item := range input {
			if item != nil {
				promptTokens += usage.EstimateTokens(item.Content)
			}
		}
	}
	completionTokens := 0
	estimated := true
	if message != nil {
		if message.ResponseMeta != nil && message.ResponseMeta.Usage != nil {
			promptTokens = message.ResponseMeta.Usage.PromptTokens
			completionTokens = message.ResponseMeta.Usage.CompletionTokens
			estimated = false
		} else {
			completionTokens = usage.EstimateTokens(message.Content)
		}
	}
	modelName := ModelName(ctx)
	if recorder := usage.FromContext(ctx); recorder != nil {
		recorder.RecordLLM(modelName, promptTokens, completionTokens, estimated)
	}
	metrics.RecordLLMCall(modelName, promptTokens, completionTokens, latency, estimated)
}

// GenerateWithUsage calls an Eino chat model and records token usage, latency
// and cost. The model may be a runtime-resolved profile proxy.
func GenerateWithUsage(ctx context.Context, chat model.BaseChatModel, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if chat == nil {
		return nil, fmt.Errorf("chat model is not configured")
	}
	started := time.Now()
	message, err := chat.Generate(ctx, input, opts...)
	RecordUsage(ctx, input, message, time.Since(started))
	return message, err
}
