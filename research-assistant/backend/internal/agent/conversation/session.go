// Package conversation provides a minimal multi-turn LLM session used by
// repair loops. It keeps the system prompt and first user message immutable,
// appends assistant outputs verbatim, and delegates capacity to ModelRuntime.
package conversation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/modelresolver"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/recovery"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// Session is one LLM conversation with recoverable source history.
type Session struct {
	ID            string
	Messages      []*schema.Message
	MaxTurns      int // Deprecated: execution limits must not delete history.
	Repair        bool
	ContextReport contextwindow.Report

	hashes []string
}

// NewSession creates a session with a stable system prompt and first user
// message. History is never trimmed by turn count.
func NewSession(id, system, firstUser string) *Session {
	return &Session{
		ID: id,
		Messages: []*schema.Message{
			{Role: schema.System, Content: system},
			{Role: schema.User, Content: firstUser},
		},
	}
}

func NewRepairSession(id, system, firstUser string) *Session {
	s := NewSession(id, system, firstUser)
	s.Repair = true
	return s
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
	ctx = modelresolver.WithSubBranch(ctx, s.ID)
	if s.Repair {
		lastUser, lastAssistant := "", ""
		for _, m := range s.Messages {
			if m == nil {
				continue
			}
			if m.Role == schema.User {
				lastUser = m.Content
			}
			if m.Role == schema.Assistant {
				lastAssistant = m.Content
			}
		}
		failure := recovery.Classify(fmt.Errorf("%s", lastUser), "local_repair", recovery.Hash(lastAssistant))
		if err := recovery.Reserve(ctx, "local_repair", failure); err != nil {
			return nil, err
		}
	}
	s.ContextReport.Requests = contextwindow.RequestCounts{}
	ctx = contextwindow.WithReport(ctx, &s.ContextReport)
	s.hashes = append(s.hashes, s.PrefixHash())
	msg, err := llmclient.GenerateWithUsage(ctx, chat, s.Messages)
	if err != nil {
		return nil, err
	}
	s.AddAssistant(msg.Content)
	return msg, nil
}

// Trim is retained for source compatibility. Capacity is managed by tokens at
// the model boundary, so repair evidence is never dropped by turn count.
func (s *Session) Trim() {
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
		"context_state": s.ContextReport,
		"turns":         s.Turns(),
		"prompt_hashes": hashes,
		"messages":      cloneMessages(s.Messages),
		"repair":        s.Repair,
	}
}

func cloneMessages(messages []*schema.Message) []*schema.Message {
	raw, _ := json.Marshal(messages)
	var cloned []*schema.Message
	_ = json.Unmarshal(raw, &cloned)
	return cloned
}

// Restore recovers the visible source conversation. The same stable branch ID
// lets ModelRuntime restore its committed native generation independently.
func Restore(snapshot map[string]any) (*Session, error) {
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, err
	}
	var saved struct {
		ID            string               `json:"session_id"`
		Messages      []*schema.Message    `json:"messages"`
		Hashes        []string             `json:"prompt_hashes"`
		ContextReport contextwindow.Report `json:"context_state"`
		Repair        bool                 `json:"repair"`
	}
	if err := json.Unmarshal(raw, &saved); err != nil {
		return nil, err
	}
	if saved.ID == "" || len(saved.Messages) < 2 {
		return nil, fmt.Errorf("repair_session_unrecoverable")
	}
	return &Session{ID: saved.ID, Messages: saved.Messages, hashes: saved.Hashes, ContextReport: saved.ContextReport, Repair: saved.Repair}, nil
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
