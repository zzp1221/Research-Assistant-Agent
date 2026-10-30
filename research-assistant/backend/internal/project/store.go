// Package project stores owner-scoped research projects used to group plans.
package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/models"

	"github.com/google/uuid"
)

var (
	// ErrNotFound means the project does not exist or is not visible to the owner.
	ErrNotFound = errors.New("project not found")
	// ErrInvalid marks caller input errors.
	ErrInvalid = errors.New("project invalid")
)

const maxProjectNameChars = 120

// Patch is a partial project update; nil fields are left unchanged.
type Patch struct {
	Name        *string
	Description *string
	Archived    *bool
}

// Store is the owner-scoped project repository.
type Store interface {
	List(ownerID string, includeArchived bool) ([]models.Project, error)
	Get(ownerID, id string) (models.Project, error)
	Create(ownerID, name, description string) (models.Project, error)
	Update(ownerID, id string, patch Patch) (models.Project, error)
	Delete(ownerID, id string) error
}

// MemoryStore is an in-memory Store used by tests and by deployments without a
// plan store path.
type MemoryStore struct {
	mu       sync.RWMutex
	projects map[string]*models.Project
	now      func() time.Time
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{projects: map[string]*models.Project{}, now: time.Now}
}

func (s *MemoryStore) List(ownerID string, includeArchived bool) ([]models.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]models.Project, 0, len(s.projects))
	for _, project := range s.projects {
		if project.OwnerID != ownerID {
			continue
		}
		if project.Archived && !includeArchived {
			continue
		}
		out = append(out, *project)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *MemoryStore) Get(ownerID, id string) (models.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	project, ok := s.projects[id]
	if !ok || project.OwnerID != ownerID {
		return models.Project{}, ErrNotFound
	}
	return *project, nil
}

func (s *MemoryStore) Create(ownerID, name, description string) (models.Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return models.Project{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if len([]rune(name)) > maxProjectNameChars {
		return models.Project{}, fmt.Errorf("%w: name is longer than %d characters", ErrInvalid, maxProjectNameChars)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.projects {
		if existing.OwnerID == ownerID && strings.EqualFold(existing.Name, name) {
			return models.Project{}, fmt.Errorf("%w: project name already exists", ErrInvalid)
		}
	}
	now := s.now().UTC()
	project := &models.Project{
		ID:          uuid.NewString(),
		OwnerID:     ownerID,
		Name:        name,
		Description: strings.TrimSpace(description),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.projects[project.ID] = project
	return *project, nil
}

func (s *MemoryStore) Update(ownerID, id string, patch Patch) (models.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, ok := s.projects[id]
	if !ok || project.OwnerID != ownerID {
		return models.Project{}, ErrNotFound
	}
	next := *project
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return models.Project{}, fmt.Errorf("%w: name is required", ErrInvalid)
		}
		if len([]rune(name)) > maxProjectNameChars {
			return models.Project{}, fmt.Errorf("%w: name is longer than %d characters", ErrInvalid, maxProjectNameChars)
		}
		for existingID, existing := range s.projects {
			if existingID != id && existing.OwnerID == ownerID && strings.EqualFold(existing.Name, name) {
				return models.Project{}, fmt.Errorf("%w: project name already exists", ErrInvalid)
			}
		}
		next.Name = name
	}
	if patch.Description != nil {
		next.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.Archived != nil {
		next.Archived = *patch.Archived
	}
	next.UpdatedAt = s.now().UTC()
	s.projects[id] = &next
	return next, nil
}

func (s *MemoryStore) Delete(ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, ok := s.projects[id]
	if !ok || project.OwnerID != ownerID {
		return ErrNotFound
	}
	delete(s.projects, id)
	return nil
}

// FileStore persists projects to a single atomically replaced JSON file.
type FileStore struct {
	mu       sync.RWMutex
	path     string
	projects map[string]*models.Project
	now      func() time.Time
}

func NewFileStore(path string) (*FileStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("project store path is empty")
	}
	store := &FileStore{path: path, projects: map[string]*models.Project{}, now: time.Now}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStore) load() error {
	raw, err := os.ReadFile(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read project store: %w", err)
	}
	if len(raw) == 0 {
		return nil
	}
	var payload struct {
		Projects map[string]*models.Project `json:"projects"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("decode project store: %w", err)
	}
	if payload.Projects != nil {
		s.projects = payload.Projects
	}
	return nil
}

func (s *FileStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create project store directory: %w", err)
	}
	raw, err := json.Marshal(struct {
		Projects map[string]*models.Project `json:"projects"`
	}{Projects: s.projects})
	if err != nil {
		return fmt.Errorf("encode project store: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".projects-*.tmp")
	if err != nil {
		return fmt.Errorf("create project store temp file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write project store: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, s.path)
}

func (s *FileStore) List(ownerID string, includeArchived bool) ([]models.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]models.Project, 0, len(s.projects))
	for _, project := range s.projects {
		if project.OwnerID != ownerID || (project.Archived && !includeArchived) {
			continue
		}
		out = append(out, *project)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out, nil
}

func (s *FileStore) Get(ownerID, id string) (models.Project, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	project, ok := s.projects[id]
	if !ok || project.OwnerID != ownerID {
		return models.Project{}, ErrNotFound
	}
	return *project, nil
}

func (s *FileStore) Create(ownerID, name, description string) (models.Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return models.Project{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if len([]rune(name)) > maxProjectNameChars {
		return models.Project{}, fmt.Errorf("%w: name is longer than %d characters", ErrInvalid, maxProjectNameChars)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.projects {
		if existing.OwnerID == ownerID && strings.EqualFold(existing.Name, name) {
			return models.Project{}, fmt.Errorf("%w: project name already exists", ErrInvalid)
		}
	}
	now := s.now().UTC()
	project := &models.Project{
		ID:          uuid.NewString(),
		OwnerID:     ownerID,
		Name:        name,
		Description: strings.TrimSpace(description),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	s.projects[project.ID] = project
	if err := s.persistLocked(); err != nil {
		delete(s.projects, project.ID)
		return models.Project{}, err
	}
	return *project, nil
}

func (s *FileStore) Update(ownerID, id string, patch Patch) (models.Project, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, ok := s.projects[id]
	if !ok || project.OwnerID != ownerID {
		return models.Project{}, ErrNotFound
	}
	next := *project
	if patch.Name != nil {
		name := strings.TrimSpace(*patch.Name)
		if name == "" {
			return models.Project{}, fmt.Errorf("%w: name is required", ErrInvalid)
		}
		if len([]rune(name)) > maxProjectNameChars {
			return models.Project{}, fmt.Errorf("%w: name is longer than %d characters", ErrInvalid, maxProjectNameChars)
		}
		for existingID, existing := range s.projects {
			if existingID != id && existing.OwnerID == ownerID && strings.EqualFold(existing.Name, name) {
				return models.Project{}, fmt.Errorf("%w: project name already exists", ErrInvalid)
			}
		}
		next.Name = name
	}
	if patch.Description != nil {
		next.Description = strings.TrimSpace(*patch.Description)
	}
	if patch.Archived != nil {
		next.Archived = *patch.Archived
	}
	next.UpdatedAt = s.now().UTC()
	s.projects[id] = &next
	if err := s.persistLocked(); err != nil {
		s.projects[id] = project
		return models.Project{}, err
	}
	return next, nil
}

func (s *FileStore) Delete(ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	project, ok := s.projects[id]
	if !ok || project.OwnerID != ownerID {
		return ErrNotFound
	}
	delete(s.projects, id)
	if err := s.persistLocked(); err != nil {
		s.projects[id] = project
		return err
	}
	return nil
}
