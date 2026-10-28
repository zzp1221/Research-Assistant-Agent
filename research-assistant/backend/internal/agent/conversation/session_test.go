package conversation

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

type echoModel struct {
	replies []string
	calls   int
}

func (m *echoModel) Generate(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	reply := "ok"
	if m.calls < len(m.replies) {
		reply = m.replies[m.calls]
	}
	m.calls++
	return &schema.Message{Role: schema.Assistant, Content: reply}, nil
}

func (m *echoModel) Stream(_ context.Context, _ []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, nil
}

func TestSessionGenerateAppendsAssistant(t *testing.T) {
	session := NewSession("test", "system", "first user")
	firstPrefix := session.PrefixHash()
	model := &echoModel{replies: []string{"patch 1", "patch 2"}}
	if _, err := session.Generate(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	if session.Turns() != 1 {
		t.Fatalf("turns=%d, want 1", session.Turns())
	}
	session.AddUser("run failed")
	secondPrefix := session.PrefixHash()
	if secondPrefix == firstPrefix {
		t.Fatal("prefix hash must change after a new user message")
	}
	if _, err := session.Generate(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	if session.Turns() != 2 {
		t.Fatalf("turns=%d, want 2", session.Turns())
	}
	if session.Messages[0].Role != schema.System || session.Messages[1].Content != "first user" {
		t.Fatal("immutable prefix changed")
	}
	if session.Messages[2].Content != "patch 1" {
		t.Fatal("previous assistant output was not preserved")
	}
}

func TestSessionTrimKeepsSystemAndFirstUser(t *testing.T) {
	session := NewSession("test", "system", "first user")
	session.MaxTurns = 2
	for i := 0; i < 6; i++ {
		session.AddAssistant("assistant")
		session.AddUser("user")
	}
	session.Trim()
	if session.Messages[0].Role != schema.System || session.Messages[1].Content != "first user" {
		t.Fatal("trim removed the immutable prefix")
	}
	if len(session.Messages) > 4 {
		t.Fatalf("trim kept %d messages, want <= 4", len(session.Messages))
	}
}

func TestSessionSnapshot(t *testing.T) {
	session := NewSession("id-1", "system", "first")
	session.AddAssistant("answer")
	snapshot := session.Snapshot()
	if snapshot["session_id"] != "id-1" || snapshot["turns"] != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if _, ok := snapshot["prompt_hashes"]; !ok {
		t.Fatalf("prompt_hashes missing: %#v", snapshot)
	}
}
