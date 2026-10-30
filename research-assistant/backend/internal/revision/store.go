// Package revision stores immutable plan revisions used by plan review/HITL.
package revision

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"research-assistant-backend/internal/models"
)

var (
	// ErrNotFound means the plan or revision does not exist.
	ErrNotFound = errors.New("revision not found")
	// ErrConflict means the supplied base revision is no longer the latest.
	ErrConflict = errors.New("revision conflict")
	// ErrInvalid marks caller input errors.
	ErrInvalid = errors.New("revision invalid")
)

// ConflictError carries the current latest revision so the API can return it.
type ConflictError struct {
	PlanID          string
	BaseRevision    int
	CurrentRevision int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("revision conflict: base=%d current=%d", e.BaseRevision, e.CurrentRevision)
}

func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// Store is the append-only revision repository.
type Store interface {
	// Create allocates the next revision number. When baseRevision > 0 it must
	// equal the current latest revision, otherwise ErrConflict is returned.
	Create(rev *models.PlanRevision, baseRevision int) (*models.PlanRevision, error)
	List(planID string, includeGraph bool) ([]models.PlanRevision, error)
	Get(planID string, revision int) (*models.PlanRevision, error)
	Latest(planID string) (*models.PlanRevision, error)
	UpdateStatus(planID string, revision int, status, approvedBy string, at *time.Time) error
	DeletePlan(planID string) error
}

// MemoryStore is the in-memory Store used by tests and single-process dev.
type MemoryStore struct {
	mu        sync.RWMutex
	revisions map[string][]*models.PlanRevision
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{revisions: map[string][]*models.PlanRevision{}}
}

func cloneRevision(rev *models.PlanRevision, includeGraph bool) *models.PlanRevision {
	if rev == nil {
		return nil
	}
	cloned := *rev
	if includeGraph && rev.Graph != nil {
		// JSON round-trip is the deep copy; it also matches how revisions are
		// persisted so in-memory and file behaviour stay identical.
		raw, err := json.Marshal(rev.Graph)
		if err == nil {
			var graph models.PlanGraph
			if json.Unmarshal(raw, &graph) == nil {
				cloned.Graph = &graph
			}
		}
	} else {
		cloned.Graph = nil
	}
	if rev.ApprovedAt != nil {
		at := *rev.ApprovedAt
		cloned.ApprovedAt = &at
	}
	if rev.DiffSummary != nil {
		cloned.DiffSummary = append([]string(nil), rev.DiffSummary...)
	}
	return &cloned
}

func (s *MemoryStore) Create(rev *models.PlanRevision, baseRevision int) (*models.PlanRevision, error) {
	if rev == nil || rev.PlanID == "" {
		return nil, fmt.Errorf("%w: plan id is required", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.revisions[rev.PlanID]
	latest := 0
	if len(list) > 0 {
		latest = list[len(list)-1].Revision
	}
	if baseRevision > 0 && baseRevision != latest {
		return nil, &ConflictError{PlanID: rev.PlanID, BaseRevision: baseRevision, CurrentRevision: latest}
	}
	stored := cloneRevision(rev, true)
	stored.Revision = latest + 1
	stored.ParentRevision = latest
	if stored.Status == "" {
		stored.Status = models.RevisionDraft
	}
	if stored.CreatedAt.IsZero() {
		stored.CreatedAt = time.Now().UTC()
	}
	stored.Hash = models.RevisionGraphHash(stored.Graph)
	if stored.ID == "" {
		stored.ID = fmt.Sprintf("rev-%s-%d", rev.PlanID, stored.Revision)
	}
	s.revisions[rev.PlanID] = append(list, stored)
	return cloneRevision(stored, true), nil
}

func (s *MemoryStore) List(planID string, includeGraph bool) ([]models.PlanRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.revisions[planID]
	out := make([]models.PlanRevision, 0, len(list))
	for _, rev := range list {
		out = append(out, *cloneRevision(rev, includeGraph))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out, nil
}

func (s *MemoryStore) Get(planID string, revision int) (*models.PlanRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rev := range s.revisions[planID] {
		if rev.Revision == revision {
			return cloneRevision(rev, true), nil
		}
	}
	return nil, ErrNotFound
}

func (s *MemoryStore) Latest(planID string) (*models.PlanRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.revisions[planID]
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return cloneRevision(list[len(list)-1], true), nil
}

func (s *MemoryStore) UpdateStatus(planID string, revision int, status, approvedBy string, at *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rev := range s.revisions[planID] {
		if rev.Revision != revision {
			continue
		}
		rev.Status = status
		if approvedBy != "" {
			rev.ApprovedBy = approvedBy
		}
		if at != nil {
			value := *at
			rev.ApprovedAt = &value
		}
		return nil
	}
	return ErrNotFound
}

func (s *MemoryStore) DeletePlan(planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.revisions, planID)
	return nil
}

// fileSnapshot is the on-disk representation. Revisions are grouped by plan.
type fileSnapshot struct {
	Version   int                               `json:"version"`
	Revisions map[string][]*models.PlanRevision `json:"revisions"`
}

// FileStore persists revisions as one atomically replaced JSON document.
type FileStore struct {
	mu   sync.RWMutex
	path string
	data fileSnapshot
}

func NewFileStore(path string) (*FileStore, error) {
	if path == "" {
		return nil, fmt.Errorf("revision store path is empty")
	}
	store := &FileStore{path: path, data: fileSnapshot{Version: 1, Revisions: map[string][]*models.PlanRevision{}}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return store, nil
		}
		return nil, err
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &store.data); err != nil {
			return nil, fmt.Errorf("parse revision store: %w", err)
		}
		if store.data.Revisions == nil {
			store.data.Revisions = map[string][]*models.PlanRevision{}
		}
	}
	return store, nil
}

func (s *FileStore) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *FileStore) Create(rev *models.PlanRevision, baseRevision int) (*models.PlanRevision, error) {
	if rev == nil || rev.PlanID == "" {
		return nil, fmt.Errorf("%w: plan id is required", ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.data.Revisions[rev.PlanID]
	latest := 0
	if len(list) > 0 {
		latest = list[len(list)-1].Revision
	}
	if baseRevision > 0 && baseRevision != latest {
		return nil, &ConflictError{PlanID: rev.PlanID, BaseRevision: baseRevision, CurrentRevision: latest}
	}
	stored := cloneRevision(rev, true)
	stored.Revision = latest + 1
	stored.ParentRevision = latest
	if stored.Status == "" {
		stored.Status = models.RevisionDraft
	}
	if stored.CreatedAt.IsZero() {
		stored.CreatedAt = time.Now().UTC()
	}
	stored.Hash = models.RevisionGraphHash(stored.Graph)
	if stored.ID == "" {
		stored.ID = fmt.Sprintf("rev-%s-%d", rev.PlanID, stored.Revision)
	}
	s.data.Revisions[rev.PlanID] = append(list, stored)
	if err := s.persistLocked(); err != nil {
		// Roll back the in-memory append so a failed write cannot invent a
		// revision that is not durable.
		s.data.Revisions[rev.PlanID] = list
		return nil, err
	}
	return cloneRevision(stored, true), nil
}

func (s *FileStore) List(planID string, includeGraph bool) ([]models.PlanRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.data.Revisions[planID]
	out := make([]models.PlanRevision, 0, len(list))
	for _, rev := range list {
		out = append(out, *cloneRevision(rev, includeGraph))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Revision < out[j].Revision })
	return out, nil
}

func (s *FileStore) Get(planID string, revisionNumber int) (*models.PlanRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, rev := range s.data.Revisions[planID] {
		if rev.Revision == revisionNumber {
			return cloneRevision(rev, true), nil
		}
	}
	return nil, ErrNotFound
}

func (s *FileStore) Latest(planID string) (*models.PlanRevision, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := s.data.Revisions[planID]
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return cloneRevision(list[len(list)-1], true), nil
}

func (s *FileStore) UpdateStatus(planID string, revisionNumber int, status, approvedBy string, at *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rev := range s.data.Revisions[planID] {
		if rev.Revision != revisionNumber {
			continue
		}
		rev.Status = status
		if approvedBy != "" {
			rev.ApprovedBy = approvedBy
		}
		if at != nil {
			value := *at
			rev.ApprovedAt = &value
		}
		return s.persistLocked()
	}
	return ErrNotFound
}

func (s *FileStore) DeletePlan(planID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Revisions, planID)
	return s.persistLocked()
}
