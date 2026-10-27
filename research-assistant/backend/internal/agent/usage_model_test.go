package agent

import (
	"context"
	"testing"
	"time"

	"research-assistant-backend/internal/usage"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type fakeChatModel struct {
	message *schema.Message
	chunks  []*schema.Message
}

func (m *fakeChatModel) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	return m.message, nil
}

func (m *fakeChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray(m.chunks), nil
}

func TestUsageChatModelRecordsProviderTokens(t *testing.T) {
	recorder := usage.NewRecorder()
	ctx := usage.WithRecorder(context.Background(), recorder)
	wrapped := wrapUsageModel(&fakeChatModel{message: &schema.Message{
		Content:      "hello",
		ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 11, CompletionTokens: 7}},
	}})
	if _, err := wrapped.Generate(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	snapshot := recorder.Snapshot()
	if snapshot.LLMCalls != 1 || snapshot.PromptTokens != 11 || snapshot.CompletionTokens != 7 || snapshot.Estimated {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestUsageChatModelRecordsStreamedTokens(t *testing.T) {
	recorder := usage.NewRecorder()
	ctx := usage.WithRecorder(context.Background(), recorder)
	wrapped := wrapUsageModel(&fakeChatModel{chunks: []*schema.Message{
		{Content: "streamed "},
		{Content: "answer", ResponseMeta: &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: 20, CompletionTokens: 9}}},
	}})
	reader, err := wrapped.Stream(ctx, []*schema.Message{{Role: schema.User, Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, recvErr := reader.Recv(); recvErr != nil {
			break
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if recorder.Snapshot().LLMCalls > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	snapshot := recorder.Snapshot()
	if snapshot.LLMCalls != 1 || snapshot.PromptTokens != 20 || snapshot.CompletionTokens != 9 || snapshot.Estimated {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}
