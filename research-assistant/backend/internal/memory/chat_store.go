package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ChatTurn is one persisted chat message.
type ChatTurn struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	PlanID    string    `json:"plan_id,omitempty"`
	Tokens    int       `json:"tokens,omitempty"`
}

// ChatSession is the server-side chat history for one session.
type ChatSession struct {
	ID        string     `json:"id"`
	OwnerID   string     `json:"owner_id"`
	ProjectID string     `json:"project_id,omitempty"`
	Title     string     `json:"title,omitempty"`
	Summary   string     `json:"summary,omitempty"`
	Turns     []ChatTurn `json:"turns"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// ChatStore persists chat sessions in a single JSON file.
type ChatStore struct {
	mu   sync.RWMutex
	path string
	data map[string]ChatSession
}

func NewChatStore(path string) (*ChatStore, error) {
	store := &ChatStore{path: path, data: map[string]ChatSession{}}
	if strings.TrimSpace(path) == "" {
		return store, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create chat store directory: %w", err)
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read chat store: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &store.data); err != nil {
			return nil, fmt.Errorf("decode chat store: %w", err)
		}
	}
	return store, nil
}

func (s *ChatStore) Get(ownerID, id string) (ChatSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.data[id]
	if !ok || session.OwnerID != ownerID {
		return ChatSession{}, false
	}
	return session, true
}

func (s *ChatStore) Create(ownerID, projectID, title string) (ChatSession, error) {
	if strings.TrimSpace(ownerID) == "" {
		return ChatSession{}, fmt.Errorf("owner_id is required")
	}
	now := time.Now().UTC()
	session := ChatSession{
		ID:        "chat-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:16],
		OwnerID:   ownerID,
		ProjectID: projectID,
		Title:     strings.TrimSpace(title),
		Turns:     []ChatTurn{},
		CreatedAt: now,
		UpdatedAt: now,
	}
	if session.Title == "" {
		session.Title = "新会话"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[session.ID] = session
	if err := s.persistLocked(); err != nil {
		return ChatSession{}, err
	}
	return session, nil
}

func (s *ChatStore) Append(ownerID, id, projectID string, turn ChatTurn) (ChatSession, error) {
	if strings.TrimSpace(ownerID) == "" {
		return ChatSession{}, fmt.Errorf("owner_id is required")
	}
	if strings.TrimSpace(turn.Content) == "" {
		return ChatSession{}, fmt.Errorf("chat turn content is required")
	}
	turn.Tokens = EstimateTokens(turn.Content)
	if turn.CreatedAt.IsZero() {
		turn.CreatedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if !ok || session.OwnerID != ownerID {
		now := time.Now().UTC()
		session = ChatSession{
			ID:        id,
			OwnerID:   ownerID,
			ProjectID: projectID,
			Title:     deriveChatTitle(turn.Content),
			Turns:     []ChatTurn{},
			CreatedAt: now,
		}
	}
	if session.ProjectID == "" {
		session.ProjectID = projectID
	}
	session.Turns = append(session.Turns, turn)
	if session.Title == "" || session.Title == "新会话" {
		session.Title = deriveChatTitle(turn.Content)
	}
	session.UpdatedAt = time.Now().UTC()
	s.data[id] = session
	if err := s.persistLocked(); err != nil {
		return ChatSession{}, err
	}
	return session, nil
}

func (s *ChatStore) SetSummary(ownerID, id, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if !ok || session.OwnerID != ownerID {
		return fmt.Errorf("chat session not found")
	}
	session.Summary = summary
	session.UpdatedAt = time.Now().UTC()
	s.data[id] = session
	return s.persistLocked()
}

func (s *ChatStore) List(ownerID string) []ChatSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ChatSession, 0)
	for _, session := range s.data {
		if session.OwnerID == ownerID {
			out = append(out, session)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

func (s *ChatStore) Delete(ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if !ok || session.OwnerID != ownerID {
		return fmt.Errorf("chat session not found")
	}
	delete(s.data, id)
	return s.persistLocked()
}

func (s *ChatStore) persistLocked() error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode chat store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write chat store: %w", err)
	}
	return os.Rename(tmp, s.path)
}

func deriveChatTitle(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) > 40 {
		content = string(runes[:40]) + "…"
	}
	if content == "" {
		return "新会话"
	}
	return content
}

// ContextTurns returns the recent turns that fit the token budget plus a
// deterministic summary of the dropped turns. The summary is persisted on the
// session so restarts keep the compressed context.
func (s *ChatStore) ContextTurns(ownerID, id string, maxTokens, maxTurns int) (string, []ChatTurn) {
	s.mu.RLock()
	session, ok := s.data[id]
	s.mu.RUnlock()
	if !ok || session.OwnerID != ownerID {
		return "", nil
	}
	return summarizeForBudget(session, maxTokens, maxTurns)
}

func summarizeForBudget(session ChatSession, maxTokens, maxTurns int) (string, []ChatTurn) {
	if maxTokens <= 0 {
		maxTokens = 32000
	}
	if maxTurns <= 0 {
		maxTurns = 20
	}
	turns := session.Turns
	start := len(turns)
	used := 0
	kept := 0
	for i := len(turns) - 1; i >= 0; i-- {
		tokens := turns[i].Tokens
		if tokens <= 0 {
			tokens = EstimateTokens(turns[i].Content)
		}
		if kept >= maxTurns || (used+tokens > maxTokens && kept > 0) {
			break
		}
		used += tokens
		kept++
		start = i
	}
	summary := strings.TrimSpace(session.Summary)
	if start > 0 {
		dropped := turns[:start]
		part := SummarizeChatTurns(dropped)
		if part != "" {
			if summary != "" {
				summary += "\n"
			}
			summary += part
		}
	}
	return summary, append([]ChatTurn(nil), turns[start:]...)
}

// SummarizeChatTurns produces a deterministic, cheap summary of old turns. It
// deliberately avoids an LLM call so compaction never blocks a chat request.
func SummarizeChatTurns(turns []ChatTurn) string {
	if len(turns) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("[earlier turns summary]\n")
	limit := len(turns)
	if limit > 10 {
		limit = 10
	}
	for i := 0; i < limit; i += 2 {
		user := strings.TrimSpace(turns[i].Content)
		assistant := ""
		if i+1 < len(turns) {
			assistant = strings.TrimSpace(turns[i+1].Content)
		}
		user = truncateRunes(user, 80)
		assistant = truncateRunes(assistant, 80)
		builder.WriteString(fmt.Sprintf("- 用户：%s", user))
		if assistant != "" {
			builder.WriteString(fmt.Sprintf("；助手：%s", assistant))
		}
		builder.WriteString("\n")
	}
	if len(turns) > limit {
		builder.WriteString(fmt.Sprintf("（更早 %d 轮已省略）\n", len(turns)-limit))
	}
	return strings.TrimSpace(builder.String())
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "…"
}
