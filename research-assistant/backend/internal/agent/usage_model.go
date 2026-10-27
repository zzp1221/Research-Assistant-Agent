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

func (m *usageChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	started := time.Now()
	message, err := m.inner.Generate(ctx, input, opts...)
	llmclient.RecordUsage(ctx, input, message, time.Since(started))
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
		llmclient.RecordUsage(ctx, input, recorded, time.Since(started))
	}()
	return copies[0], nil
}
