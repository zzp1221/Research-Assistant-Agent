package llmclient

import (
	"context"
	"os"
	"strings"
	"time"

	"research-assistant-backend/internal/metrics"
	"research-assistant-backend/internal/usage"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// ModelName returns the configured model identifier used for metrics and the
// usage ledger. Several deployments set different names, so check the common
// variables in priority order.
func ModelName() string {
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
	modelName := ModelName()
	if recorder := usage.FromContext(ctx); recorder != nil {
		recorder.RecordLLM(modelName, promptTokens, completionTokens, estimated)
	}
	metrics.RecordLLMCall(modelName, promptTokens, completionTokens, latency, estimated)
}

// GenerateWithUsage calls an Eino chat model and records token usage, latency
// and cost.
func GenerateWithUsage(ctx context.Context, chat *openai.ChatModel, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	started := time.Now()
	message, err := chat.Generate(ctx, input, opts...)
	RecordUsage(ctx, input, message, time.Since(started))
	return message, err
}
