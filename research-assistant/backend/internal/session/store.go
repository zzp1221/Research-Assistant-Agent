package session

import (
	"context"
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

// Session is one server-owned conversation/workspace identity. Plans and chat
// turns attach to it so the frontend no longer invents a second id.
type Session struct {
	ID           string    `json:"id"`
	OwnerID      string    `json:"owner_id"`
	ProjectID    string    `json:"project_id,omitempty"`
	Title        string    `json:"title,omitempty"`
	Kind         string    `json:"kind,omitempty"` // plan | chat | mixed
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}

type Store interface {
	Create(ctx context.Context, ownerID, projectID, title, kind string) (Session, error)
	Ensure(ctx context.Context, id, ownerID, projectID, kind string) (Session, error)
	Get(ctx context.Context, ownerID, id string) (Session, error)
	List(ctx context.Context, ownerID string) ([]Session, error)
	Touch(ctx context.Context, ownerID, id string) error
	Delete(ctx context.Context, ownerID, id string) error
}

// FileStore persists sessions in one atomically replaced JSON file.
type FileStore struct {
	mu   sync.RWMutex
	path string
	data map[string]Session
}

func NewFileStore(path string) (*FileStore, error) {
	store := &FileStore{path: path, data: map[string]Session{}}
	if strings.TrimSpace(path) == "" {
		return store, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &store.data); err != nil {
			return nil, fmt.Errorf("decode sessions: %w", err)
		}
	}
	return store, nil
}

func (s *FileStore) Create(ctx context.Context, ownerID, projectID, title, kind string) (Session, error) {
	_ = ctx
	if strings.TrimSpace(ownerID) == "" {
		return Session{}, fmt.Errorf("owner_id is required")
	}
	now := time.Now().UTC()
	session := Session{
		ID:           "s-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20],
		OwnerID:      ownerID,
		ProjectID:    projectID,
		Title:        strings.TrimSpace(title),
		Kind:         normalizeKind(kind),
		CreatedAt:    now,
		UpdatedAt:    now,
		LastActiveAt: now,
	}
	if session.Title == "" {
		session.Title = "新会话"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[session.ID] = session
	if err := s.persistLocked(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *FileStore) Ensure(ctx context.Context, id, ownerID, projectID, kind string) (Session, error) {
	_ = ctx
	id = strings.TrimSpace(id)
	if id == "" {
		return s.Create(ctx, ownerID, projectID, "", kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.data[id]; ok {
		if existing.OwnerID != ownerID {
			return Session{}, fmt.Errorf("session not found")
		}
		changed := false
		if existing.ProjectID == "" && projectID != "" {
			existing.ProjectID = projectID
			changed = true
		}
		if existing.Kind == "" {
			existing.Kind = normalizeKind(kind)
			changed = true
		}
		if changed {
			existing.UpdatedAt = time.Now().UTC()
			s.data[id] = existing
			if err := s.persistLocked(); err != nil {
				return Session{}, err
			}
		}
		return existing, nil
	}
	now := time.Now().UTC()
	session := Session{ID: id, OwnerID: ownerID, ProjectID: projectID, Kind: normalizeKind(kind), Title: "新会话", CreatedAt: now, UpdatedAt: now, LastActiveAt: now}
	s.data[id] = session
	if err := s.persistLocked(); err != nil {
		return Session{}, err
	}
	return session, nil
}

func (s *FileStore) Get(ctx context.Context, ownerID, id string) (Session, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.data[id]
	if !ok || session.OwnerID != ownerID {
		return Session{}, fmt.Errorf("session not found")
	}
	return session, nil
}

func (s *FileStore) List(ctx context.Context, ownerID string) ([]Session, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Session, 0)
	for _, session := range s.data {
		if session.OwnerID == ownerID {
			out = append(out, session)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActiveAt.After(out[j].LastActiveAt) })
	return out, nil
}

func (s *FileStore) Touch(ctx context.Context, ownerID, id string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if !ok || session.OwnerID != ownerID {
		return fmt.Errorf("session not found")
	}
	now := time.Now().UTC()
	session.UpdatedAt = now
	session.LastActiveAt = now
	s.data[id] = session
	return s.persistLocked()
}

func (s *FileStore) Delete(ctx context.Context, ownerID, id string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.data[id]
	if !ok || session.OwnerID != ownerID {
		return fmt.Errorf("session not found")
	}
	delete(s.data, id)
	return s.persistLocked()
}

func (s *FileStore) persistLocked() error {
	if strings.TrimSpace(s.path) == "" {
		return nil
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode sessions: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("write sessions: %w", err)
	}
	return os.Rename(tmp, s.path)
}

func normalizeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "plan":
		return "plan"
	case "chat":
		return "chat"
	case "mixed":
		return "mixed"
	default:
		return "mixed"
	}
}
