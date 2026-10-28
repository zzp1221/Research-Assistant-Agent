package planner

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

// runtimePlannerModel resolves the planner's chat model from the runtime
// profile of the plan owner. When no profile store is installed it falls back
// to the startup model so unit tests and env-only deployments keep working.
type runtimePlannerModel struct {
	fallback *openai.ChatModel

	mu    sync.Mutex
	cache map[string]*openai.ChatModel
}

func newRuntimePlannerModel(fallback *openai.ChatModel) *runtimePlannerModel {
	return &runtimePlannerModel{fallback: fallback, cache: map[string]*openai.ChatModel{}}
}

func (m *runtimePlannerModel) pick(ctx context.Context) (model.BaseChatModel, error) {
	resolver := modelresolver.Default()
	if resolver == nil {
		if m.fallback == nil {
			return nil, fmt.Errorf("planner model is not configured")
		}
		return m.fallback, nil
	}
	resolved, err := resolver.ResolveText(modelresolver.OwnerFromContext(ctx))
	if err != nil {
		if m.fallback != nil {
			return m.fallback, nil
		}
		return nil, err
	}
	if resolved.Profile.ID == "env-bootstrap" && m.fallback != nil {
		return m.fallback, nil
	}
	key := fmt.Sprintf("%s@%d", resolved.Profile.ID, resolved.Profile.Revision)
	m.mu.Lock()
	cached := m.cache[key]
	m.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	created, createErr := llmclient.NewChatModel(ctx, &openai.ChatModelConfig{
		BaseURL: resolved.Profile.BaseURL,
		APIKey:  resolved.APIKey,
		Model:   resolved.Profile.Model,
	})
	if createErr != nil {
		return nil, createErr
	}
	m.mu.Lock()
	if existing := m.cache[key]; existing != nil {
		created = existing
	} else {
		m.cache[key] = created
	}
	m.mu.Unlock()
	return created, nil
}

func (m *runtimePlannerModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	picked, err := m.pick(ctx)
	if err != nil {
		return nil, err
	}
	return picked.Generate(ctx, input, opts...)
}

func (m *runtimePlannerModel) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	picked, err := m.pick(ctx)
	if err != nil {
		return nil, err
	}
	return picked.Stream(ctx, input, opts...)
}

// available reports whether the planner can attempt an LLM plan at all.
func (m *runtimePlannerModel) available() bool {
	if m == nil {
		return false
	}
	if m.fallback != nil {
		return true
	}
	return modelresolver.Default() != nil
}
