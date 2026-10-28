// Package conversation provides a minimal multi-turn LLM session used by
// repair loops. It keeps the system prompt and first user message immutable,
// appends assistant outputs verbatim, and bounds the retained history.
package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/models"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Session is one bounded LLM conversation.
type Session struct {
	ID       string
	Messages []*schema.Message
	MaxTurns int

	hashes []string
}

// NewSession creates a session with a stable system prompt and first user
// message. MaxTurns defaults to 6.
func NewSession(id, system, firstUser string) *Session {
	return &Session{
		ID: id,
		Messages: []*schema.Message{
			{Role: schema.System, Content: system},
			{Role: schema.User, Content: firstUser},
		},
		MaxTurns: 6,
	}
}

// AddAssistant appends an assistant message.
func (s *Session) AddAssistant(content string) {
	if s == nil {
		return
	}
	s.Messages = append(s.Messages, &schema.Message{Role: schema.Assistant, Content: content})
}

// AddUser appends a user message.
func (s *Session) AddUser(content string) {
	if s == nil {
		return
	}
	s.Messages = append(s.Messages, &schema.Message{Role: schema.User, Content: content})
}

// Generate sends the current conversation and appends the assistant reply.
func (s *Session) Generate(ctx context.Context, chat model.BaseChatModel) (*schema.Message, error) {
	if s == nil {
		return nil, nil
	}
	s.Trim()
	s.hashes = append(s.hashes, s.PrefixHash())
	msg, err := llmclient.GenerateWithUsage(ctx, chat, s.Messages)
	if err != nil {
		return nil, err
	}
	s.AddAssistant(msg.Content)
	return msg, nil
}

// Trim keeps system + first user and the most recent turns. Older middle turns
// are dropped in assistant/user pairs, never the immutable prefix.
func (s *Session) Trim() {
	if s == nil || len(s.Messages) <= 2 {
		return
	}
	maxTurns := s.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 6
	}
	keep := 2 + 2*(maxTurns-1)
	if len(s.Messages) <= keep {
		return
	}
	head := append([]*schema.Message(nil), s.Messages[:2]...)
	tail := append([]*schema.Message(nil), s.Messages[len(s.Messages)-(keep-2):]...)
	s.Messages = append(head, tail...)
}

// PrefixHash returns a stable hash of the current messages.
func (s *Session) PrefixHash() string {
	if s == nil {
		return ""
	}
	var builder strings.Builder
	for _, message := range s.Messages {
		if message == nil {
			continue
		}
		builder.WriteString(string(message.Role))
		builder.WriteString("\x00")
		builder.WriteString(message.Content)
		builder.WriteString("\x00")
	}
	sum := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(sum[:])
}

// Turns reports how many assistant turns the session has.
func (s *Session) Turns() int {
	if s == nil {
		return 0
	}
	count := 0
	for _, message := range s.Messages {
		if message != nil && message.Role == schema.Assistant {
			count++
		}
	}
	return count
}

// Snapshot returns the persisted metadata view of the session.
func (s *Session) Snapshot() map[string]any {
	if s == nil {
		return nil
	}
	hashes := append([]string(nil), s.hashes...)
	return map[string]any{
		"session_id":    s.ID,
		"turns":         s.Turns(),
		"prompt_hashes": hashes,
	}
}

// Record stores the session summary on task metadata.
func (s *Session) Record(task *models.Task) {
	if s == nil || task == nil {
		return
	}
	if task.Metadata == nil {
		task.Metadata = map[string]any{}
	}
	task.Metadata["repair_session"] = s.Snapshot()
}
