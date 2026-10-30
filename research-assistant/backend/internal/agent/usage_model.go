package agent

import (
	"context"
	"strings"
	"time"

	"research-assistant-backend/internal/llmclient"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// usageChatModel wraps an Eino chat model so chat-model graph nodes (which call
// Generate/Stream internally) also feed the plan usage ledger. Without it,
// primary-generation paths such as the coder code chain would report zero
// tokens even though they call the provider.
type usageChatModel struct {
	inner model.BaseChatModel
}

func wrapUsageModel(inner model.BaseChatModel) model.BaseChatModel {
	if inner == nil {
		return nil
	}
	return &usageChatModel{inner: inner}
}

// modelNameFor prefers the runtime proxy's resolved model name. The dynamic
// proxy pins the name on its local context inside Generate, but that context is
// not visible to the outer usage wrapper, which used to record the env model.
func modelNameFor(ctx context.Context, inner model.BaseChatModel) string {
	if provider, ok := inner.(interface{ ResolvedModelName(context.Context) string }); ok {
		if name := provider.ResolvedModelName(ctx); name != "" {
			return name
		}
	}
	return llmclient.ModelName(ctx)
}

// modelPriceFor mirrors modelNameFor for the runtime profile price.
func modelPriceFor(ctx context.Context, inner model.BaseChatModel) (float64, float64) {
	if provider, ok := inner.(interface {
		ResolvedModelPrice(context.Context) (float64, float64)
	}); ok {
		return provider.ResolvedModelPrice(ctx)
	}
	return 0, 0
}

func (m *usageChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	started := time.Now()
	message, err := m.inner.Generate(ctx, input, opts...)
	recordCtx := llmclient.WithModelName(ctx, modelNameFor(ctx, m.inner))
	if promptPrice, completionPrice := modelPriceFor(ctx, m.inner); promptPrice > 0 || completionPrice > 0 {
		recordCtx = llmclient.WithModelPrice(recordCtx, promptPrice, completionPrice)
	}
	llmclient.RecordUsage(recordCtx, input, message, time.Since(started))
	return message, err
}

func (m *usageChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	started := time.Now()
	reader, err := m.inner.Stream(ctx, input, opts...)
	if err != nil {
		llmclient.RecordUsage(ctx, input, nil, time.Since(started))
		return nil, err
	}
	copies := reader.Copy(2)
	observer := copies[1]
	go func() {
		defer observer.Close()
		var content strings.Builder
		var tokenUsage *schema.TokenUsage
		for {
			message, recvErr := observer.Recv()
			if recvErr != nil {
				break
			}
			if message == nil {
				continue
			}
			content.WriteString(message.Content)
			if message.ResponseMeta != nil && message.ResponseMeta.Usage != nil {
				tokenUsage = message.ResponseMeta.Usage
			}
		}
		recorded := &schema.Message{Content: content.String()}
		if tokenUsage != nil {
			recorded.ResponseMeta = &schema.ResponseMeta{Usage: tokenUsage}
		}
		recordCtx := llmclient.WithModelName(ctx, modelNameFor(ctx, m.inner))
		if promptPrice, completionPrice := modelPriceFor(ctx, m.inner); promptPrice > 0 || completionPrice > 0 {
			recordCtx = llmclient.WithModelPrice(recordCtx, promptPrice, completionPrice)
		}
		llmclient.RecordUsage(recordCtx, input, recorded, time.Since(started))
	}()
	return copies[0], nil
}
