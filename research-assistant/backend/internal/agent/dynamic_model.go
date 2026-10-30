package agent

import (
	"context"
	"fmt"
	"sync"

	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/modelresolver"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// dynamicChatModel implements Eino's BaseChatModel but resolves the concrete
// provider model from the plan owner's runtime profile on every call. The Eino
// graph is compiled once; a profile edit changes the resolved profile revision
// and therefore the cached client, without rebuilding the chain (design 4.3).
type dynamicChatModel struct {
	fallback *openai.ChatModel

	mu    sync.Mutex
	cache map[string]*openai.ChatModel
}

func newDynamicChatModel(fallback *openai.ChatModel) *dynamicChatModel {
	return &dynamicChatModel{fallback: fallback, cache: map[string]*openai.ChatModel{}}
}

// resolvedModel returns the provider model plus the model identifier recorded
// in the usage ledger.
func (m *dynamicChatModel) resolvedModel(ctx context.Context) (model.BaseChatModel, string, error) {
	resolver := modelresolver.Default()
	if resolver == nil {
		if m.fallback == nil {
			return nil, "", fmt.Errorf("no chat model is configured")
		}
		return m.fallback, "", nil
	}
	owner := modelresolver.OwnerFromContext(ctx)
	resolved, err := resolver.ResolveText(owner)
	if err != nil {
		if m.fallback != nil {
			return m.fallback, "", nil
		}
		return nil, "", err
	}
	if resolved.Profile.ID == "env-bootstrap" && m.fallback != nil {
		return m.fallback, resolved.Profile.Model, nil
	}
	key := fmt.Sprintf("%s@%d", resolved.Profile.ID, resolved.Profile.Revision)
	m.mu.Lock()
	cached := m.cache[key]
	m.mu.Unlock()
	if cached != nil {
		return cached, resolved.Profile.Model, nil
	}
	chatModel, err := llmclient.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: resolved.Profile.BaseURL,
		APIKey:  resolved.APIKey,
		Model:   resolved.Profile.Model,
	})
	if err != nil {
		return nil, "", fmt.Errorf("resolve chat model for profile %s: %w", resolved.Profile.ID, err)
	}
	m.mu.Lock()
	if existing := m.cache[key]; existing != nil {
		chatModel = existing
	} else {
		m.cache[key] = chatModel
	}
	m.mu.Unlock()
	return chatModel, resolved.Profile.Model, nil
}

// ResolvedModelName returns the model identifier the next Generate/Stream call
// would use. It lets the usage wrapper attribute tokens to the runtime profile
// instead of the startup env model.
func (m *dynamicChatModel) ResolvedModelName(ctx context.Context) string {
	if m == nil {
		return ""
	}
	_, name, err := m.resolvedModel(ctx)
	if err != nil {
		return ""
	}
	return name
}

// ResolvedModelPrice returns the runtime profile's 1K-token prices so the outer
// usage wrapper can bill the same model the inner call actually uses.
func (m *dynamicChatModel) ResolvedModelPrice(ctx context.Context) (float64, float64) {
	if m == nil {
		return 0, 0
	}
	resolver := modelresolver.Default()
	if resolver == nil {
		return 0, 0
	}
	resolved, err := resolver.ResolveText(modelresolver.OwnerFromContext(ctx))
	if err != nil {
		return 0, 0
	}
	return resolved.Profile.PromptPricePer1K, resolved.Profile.CompletionPricePer1K
}

func (m *dynamicChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	chatModel, modelName, err := m.resolvedModel(ctx)
	if err != nil {
		return nil, err
	}
	if chatModel == nil {
		return nil, fmt.Errorf("no chat model is configured")
	}
	if modelName != "" {
		ctx = llmclient.WithModelName(ctx, modelName)
	}
	if promptPrice, completionPrice := m.ResolvedModelPrice(ctx); promptPrice > 0 || completionPrice > 0 {
		ctx = llmclient.WithModelPrice(ctx, promptPrice, completionPrice)
	}
	return chatModel.Generate(ctx, input, opts...)
}

func (m *dynamicChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	chatModel, modelName, err := m.resolvedModel(ctx)
	if err != nil {
		return nil, err
	}
	if chatModel == nil {
		return nil, fmt.Errorf("no chat model is configured")
	}
	if modelName != "" {
		ctx = llmclient.WithModelName(ctx, modelName)
	}
	if promptPrice, completionPrice := m.ResolvedModelPrice(ctx); promptPrice > 0 || completionPrice > 0 {
		ctx = llmclient.WithModelPrice(ctx, promptPrice, completionPrice)
	}
	return chatModel.Stream(ctx, input, opts...)
}

// staticChatModel adapts a fixed *openai.ChatModel into model.BaseChatModel for
// tests that construct agents directly.
func staticChatModel(inner *openai.ChatModel) model.BaseChatModel {
	if inner == nil {
		return nil
	}
	return inner
}

// wrapRuntimeModel picks the runtime proxy in production and the injected model
// in unit tests.
func wrapRuntimeModel(dynamic *dynamicChatModel, fixed *openai.ChatModel) model.BaseChatModel {
	if dynamic != nil {
		return dynamic
	}
	return staticChatModel(fixed)
}
