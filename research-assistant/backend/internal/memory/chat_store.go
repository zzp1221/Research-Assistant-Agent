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

// ChatReference is the provenance of one document passage the user selected in
// the PDF reader when asking a question. It records what the user pointed at,
// which is a user claim about the source, not server-verified verbatim evidence
// of the document text.
type ChatReference struct {
	UploadID     string `json:"upload_id"`
	DocumentName string `json:"document_name,omitempty"`
	Page         int    `json:"page"`
	Quote        string `json:"quote,omitempty"`
	// Source names how the passage was produced; currently "user_selection".
	Source string `json:"source,omitempty"`
}

// ChatTurn is one persisted chat message.
type ChatTurn struct {
	ID           string    `json:"id,omitempty"`
	Role         string    `json:"role"`
	Content      string    `json:"content"`
	ModelContent string    `json:"model_content,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	PlanID       string    `json:"plan_id,omitempty"`
	Tokens       int       `json:"tokens,omitempty"`
	// References carries the document passages attached to a user turn so a
	// later turn in the same session can keep citing the same pages. Content
	// stays the original question so titles, memory indexing and recall are
	// unaffected by the source context.
	References []ChatReference `json:"references,omitempty"`
}

// ChatSession is the server-side chat history for one session.
type ChatSession struct {
	ID             string     `json:"id"`
	OwnerID        string     `json:"owner_id"`
	ProjectID      string     `json:"project_id,omitempty"`
	Title          string     `json:"title,omitempty"`
	Summary        string     `json:"summary,omitempty"`
	SummaryThrough int        `json:"summary_through,omitempty"`
	SummaryVersion int        `json:"summary_version,omitempty"`
	SummaryEntries []string   `json:"summary_entries,omitempty"`
	Turns          []ChatTurn `json:"turns"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ChatStore persists chat sessions in a single JSON file.
type ChatStore struct {
	mu     sync.RWMutex
	path   string
	data   map[string]ChatSession
	index  *Index
	chunks map[string]Record
}

func NewChatStore(path string) (*ChatStore, error) {
	store := &ChatStore{path: path, data: map[string]ChatSession{}, index: NewIndex(), chunks: map[string]Record{}}
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
	for id, session := range store.data {
		for i := range session.Turns {
			if session.Turns[i].ID == "" {
				session.Turns[i].ID = fmt.Sprintf("%s-message-%d", id, i+1)
			}
			store.indexTurnLocked(session, session.Turns[i])
		}
		store.data[id] = session
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
	return cloneSession(session), true
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
		delete(s.data, session.ID)
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
	if turn.ID == "" {
		turn.ID = "message-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if ok && session.OwnerID != ownerID {
		return ChatSession{}, fmt.Errorf("chat session not found")
	}
	if ok && session.ProjectID != projectID {
		return ChatSession{}, fmt.Errorf("chat session project does not match")
	}
	previous := cloneSession(session)
	if !ok {
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
	// The store must not alias the caller's reference slice: a caller that
	// reuses or mutates its turn would otherwise rewrite persisted provenance.
	cloneChatTurnReferences(&turn)
	session.Turns = append(append([]ChatTurn(nil), session.Turns...), turn)
	if session.Title == "" || session.Title == "新会话" {
		session.Title = deriveChatTitle(turn.Content)
	}
	session.UpdatedAt = time.Now().UTC()
	s.data[id] = session
	if err := s.persistLocked(); err != nil {
		if ok {
			s.data[id] = previous
		} else {
			delete(s.data, id)
		}
		return ChatSession{}, err
	}
	s.indexTurnLocked(session, turn)
	return cloneSession(session), nil
}

func (s *ChatStore) SetSummary(ownerID, id, summary string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if !ok || session.OwnerID != ownerID {
		return fmt.Errorf("chat session not found")
	}
	previous := cloneSession(session)
	session.Summary = summary
	session.UpdatedAt = time.Now().UTC()
	s.data[id] = session
	if err := s.persistLocked(); err != nil {
		s.data[id] = previous
		return err
	}
	return nil
}

func (s *ChatStore) List(ownerID string) []ChatSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ChatSession, 0)
	for _, session := range s.data {
		if session.OwnerID == ownerID {
			out = append(out, cloneSession(session))
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
	if err := s.persistLocked(); err != nil {
		s.data[id] = session
		return err
	}
	for chunkID, rec := range s.chunks {
		if rec.Scope.OwnerID == ownerID && rec.Scope.SessionID == id {
			s.index.Remove(rec)
			delete(s.chunks, chunkID)
		}
	}
	return nil
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
	if err := writeFileSynced(tmp, raw); err != nil {
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
	summary, turns, _ := s.contextTurns(ownerID, id, maxTokens, maxTurns)
	return summary, turns
}

func (s *ChatStore) contextTurns(ownerID, id string, maxTokens, maxTurns int) (string, []ChatTurn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if !ok || session.OwnerID != ownerID {
		return "", nil, nil
	}
	previous := cloneSession(session)
	session = cloneSession(session)
	if maxTokens <= 0 {
		maxTokens = 32000
	}
	if maxTurns <= 0 {
		maxTurns = 20
	}
	summaryBudget := maxTokens / 8
	if summaryBudget > 1200 {
		summaryBudget = 1200
	}
	if summaryBudget < 1 {
		summaryBudget = 1
	}
	historyBudget := maxTokens - summaryBudget
	turns := session.Turns
	start := len(turns)
	used := 0
	kept := 0
	for i := len(turns) - 1; i >= 0; i-- {
		tokens := turns[i].Tokens
		if tokens <= 0 {
			tokens = EstimateTokens(turns[i].Content)
		}
		if kept >= maxTurns || used+tokens > historyBudget {
			break
		}
		used += tokens
		kept++
		start = i
	}
	if start > session.SummaryThrough {
		for _, turn := range turns[session.SummaryThrough:start] {
			if turn.Role != "user" {
				continue
			}
			if !isResearchStatement(turn.Content) && len(session.SummaryEntries) > 0 {
				continue
			}
			rec, err := chatRecord(session, turn, 0, truncateRunes(turn.Content, 600))
			if err != nil || rec.Trust == TrustUntrusted {
				continue
			}
			session.SummaryEntries = append(session.SummaryEntries, "- ["+turn.ID+"] "+truncateRunes(turn.Content, 300))
		}
		for len(session.SummaryEntries) > 1 && EstimateTokens(strings.Join(session.SummaryEntries, "\n")) > summaryBudget-20 {
			session.SummaryEntries = session.SummaryEntries[1:]
		}
		session.SummaryThrough = start
		session.SummaryVersion++
		session.Summary = "[earlier turns summary]\n" + strings.Join(session.SummaryEntries, "\n")
		session.Summary, _ = truncateToTokens(session.Summary, summaryBudget)
		s.data[id] = session
		if err := s.persistLocked(); err != nil {
			s.data[id] = previous
			return "", nil, err
		}
	}
	summary, _ := truncateToTokens(strings.TrimSpace(session.Summary), summaryBudget)
	recent := append([]ChatTurn(nil), turns[start:]...)
	if len(recent) == 0 && len(turns) > 0 && historyBudget > 0 {
		last := turns[len(turns)-1]
		last.Content, _ = truncateToTokens(last.Content, historyBudget)
		last.Tokens = EstimateTokens(last.Content)
		recent = []ChatTurn{last}
	}
	return summary, recent, nil
}

// SummarizeChatTurns produces a deterministic, cheap summary of old turns. It
// deliberately avoids an LLM call so compaction never blocks a chat request.
func SummarizeChatTurns(turns []ChatTurn) string {
	if len(turns) == 0 {
		return ""
	}
	parts := []string{"[earlier turns summary]"}
	for _, turn := range turns {
		if turn.Role == "user" {
			parts = append(parts, "- 用户："+truncateRunes(turn.Content, 300))
		}
	}
	text, _ := truncateToTokens(strings.Join(parts, "\n"), 1200)
	return text
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "…"
}
