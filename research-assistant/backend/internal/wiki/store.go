package wiki

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"research-assistant-backend/internal/lockfile"
)

const (
	papersWALName      = "papers.jsonl"
	papersSnapshotName = "papers.snapshot.json"
	wikiLockName       = ".wiki.lock"
	wikiCompactEvery   = 500
)

type paperWALEntry struct {
	Op    string     `json:"op"`
	Paper *PaperPage `json:"paper,omitempty"`
	ID    string     `json:"id,omitempty"`
	At    time.Time  `json:"at"`
}

type paperSnapshot struct {
	Version int         `json:"version"`
	Papers  []PaperPage `json:"papers"`
}

// PaperStore is the rebuildable PaperPage projection cache. It is not a source
// of truth: deleting the directory and calling Rebuild reproduces every page
// from memory + documents + literature.
type PaperStore struct {
	mu       sync.RWMutex
	root     string
	papers   map[string]map[string]PaperPage // owner -> paper -> page
	walFiles map[string]*os.File
	walCount map[string]int
	lockFile *os.File
	persist  bool
}

// NewPaperStore opens (or creates) a wiki directory.
func NewPaperStore(root string) (*PaperStore, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("wiki directory is empty")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create wiki directory: %w", err)
	}
	store := &PaperStore{
		root:     root,
		papers:   map[string]map[string]PaperPage{},
		walFiles: map[string]*os.File{},
		walCount: map[string]int{},
		persist:  true,
	}
	lock, err := acquireFileLock(filepath.Join(root, wikiLockName))
	if err != nil {
		return nil, err
	}
	store.lockFile = lock
	if err := store.load(); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

// NewMemoryPaperStore builds an in-memory store for tests.
func NewMemoryPaperStore() *PaperStore {
	return &PaperStore{papers: map[string]map[string]PaperPage{}, walFiles: map[string]*os.File{}, walCount: map[string]int{}}
}

func acquireFileLock(path string) (*os.File, error) {
	return lockfile.Acquire(path, 3*time.Second)
}

func (s *PaperStore) ownerDir(ownerID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(ownerID)))
	return filepath.Join(s.root, "owners", hex.EncodeToString(sum[:16]))
}

func (s *PaperStore) snapshotPath(ownerID string) string {
	return filepath.Join(s.ownerDir(ownerID), papersSnapshotName)
}

func (s *PaperStore) walPath(ownerID string) string {
	return filepath.Join(s.ownerDir(ownerID), papersWALName)
}

func (s *PaperStore) load() error {
	ownersRoot := filepath.Join(s.root, "owners")
	entries, err := os.ReadDir(ownersRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(ownersRoot, entry.Name())
		var snapshot paperSnapshot
		if raw, readErr := os.ReadFile(filepath.Join(dir, papersSnapshotName)); readErr == nil {
			if decodeErr := json.Unmarshal(raw, &snapshot); decodeErr != nil {
				return fmt.Errorf("decode wiki snapshot: %w", decodeErr)
			}
		}
		for _, page := range snapshot.Papers {
			if strings.TrimSpace(page.OwnerID) == "" {
				continue
			}
			s.putMemory(page)
		}
		walPath := filepath.Join(dir, papersWALName)
		file, openErr := os.Open(walPath)
		if openErr != nil {
			if os.IsNotExist(openErr) {
				continue
			}
			return openErr
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scanner.Scan() {
			var entry paperWALEntry
			if json.Unmarshal(scanner.Bytes(), &entry) != nil {
				continue
			}
			switch entry.Op {
			case "put":
				if entry.Paper != nil {
					s.putMemory(*entry.Paper)
				}
			case "delete":
				s.deleteMemoryByID(entry.ID)
			}
		}
		_ = file.Close()
	}
	return nil
}

func (s *PaperStore) putMemory(page PaperPage) {
	if s.papers[page.OwnerID] == nil {
		s.papers[page.OwnerID] = map[string]PaperPage{}
	}
	s.papers[page.OwnerID][page.PaperID] = page
}

func (s *PaperStore) deleteMemoryByID(id string) {
	for owner, pages := range s.papers {
		if _, ok := pages[id]; ok {
			delete(s.papers[owner], id)
			return
		}
	}
}

func (s *PaperStore) walFor(ownerID string) (*os.File, error) {
	if file, ok := s.walFiles[ownerID]; ok {
		return file, nil
	}
	dir := s.ownerDir(ownerID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(s.walPath(ownerID), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	s.walFiles[ownerID] = file
	return file, nil
}

// Put upserts one PaperPage and appends it to the owner WAL.
func (s *PaperStore) Put(ctx context.Context, page PaperPage) (PaperPage, error) {
	_ = ctx
	if strings.TrimSpace(page.OwnerID) == "" {
		return PaperPage{}, fmt.Errorf("paper owner_id is required")
	}
	if strings.TrimSpace(page.PaperID) == "" {
		return PaperPage{}, fmt.Errorf("paper_id is required")
	}
	now := time.Now().UTC()
	if page.CreatedAt.IsZero() {
		page.CreatedAt = now
	}
	page.UpdatedAt = now
	s.mu.Lock()
	defer s.mu.Unlock()
	if previous, ok := s.papers[page.OwnerID][page.PaperID]; ok {
		page.CreatedAt = previous.CreatedAt
		if page.Revision <= previous.Revision {
			page.Revision = previous.Revision + 1
		}
	}
	if page.Revision <= 0 {
		page.Revision = 1
	}
	s.putMemory(page)
	if !s.persist {
		return page, nil
	}
	file, err := s.walFor(page.OwnerID)
	if err != nil {
		return PaperPage{}, err
	}
	encoded, err := json.Marshal(paperWALEntry{Op: "put", Paper: &page, At: now})
	if err != nil {
		return PaperPage{}, err
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		return PaperPage{}, err
	}
	s.walCount[page.OwnerID]++
	if s.walCount[page.OwnerID] >= wikiCompactEvery {
		if err := s.writeSnapshotLocked(page.OwnerID); err != nil {
			return PaperPage{}, err
		}
		s.walCount[page.OwnerID] = 0
	}
	return page, nil
}

func (s *PaperStore) writeSnapshotLocked(ownerID string) error {
	dir := s.ownerDir(ownerID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	pages := make([]PaperPage, 0, len(s.papers[ownerID]))
	for _, page := range s.papers[ownerID] {
		pages = append(pages, page)
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].PaperID < pages[j].PaperID })
	raw, err := json.Marshal(paperSnapshot{Version: 1, Papers: pages})
	if err != nil {
		return err
	}
	target := s.snapshotPath(ownerID)
	temp := target + ".tmp"
	if err := os.WriteFile(temp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, target)
}

// Get returns one owner-scoped paper page.
func (s *PaperStore) Get(ctx context.Context, ownerID, paperID string) (PaperPage, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	pages, ok := s.papers[strings.TrimSpace(ownerID)]
	if !ok {
		return PaperPage{}, fmt.Errorf("paper not found")
	}
	page, ok := pages[strings.TrimSpace(paperID)]
	if !ok {
		return PaperPage{}, fmt.Errorf("paper not found")
	}
	return page, nil
}

// List returns owner-scoped papers, newest first.
func (s *PaperStore) List(ctx context.Context, q PaperQuery) ([]PaperPage, int, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]PaperPage, 0, len(s.papers[q.OwnerID]))
	needle := strings.ToLower(strings.TrimSpace(q.Text))
	for _, page := range s.papers[q.OwnerID] {
		if q.ProjectID != "" && page.ProjectID != q.ProjectID {
			continue
		}
		switch q.Status {
		case "archived":
			if !page.Archived {
				continue
			}
		case "all":
		default:
			if page.Archived {
				continue
			}
		}
		if needle != "" {
			haystack := strings.ToLower(page.Title + " " + page.Abstract + " " + page.TLDR + " " + strings.Join(page.Authors, " ") + " " + page.DOI + " " + page.ArxivID)
			if !strings.Contains(haystack, needle) {
				continue
			}
		}
		all = append(all, page)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].UpdatedAt.Equal(all[j].UpdatedAt) {
			return all[i].PaperID < all[j].PaperID
		}
		return all[i].UpdatedAt.After(all[j].UpdatedAt)
	})
	total := len(all)
	offset := q.Cursor
	if offset < 0 {
		offset = 0
	}
	if offset >= total {
		return []PaperPage{}, total, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return all[offset:end], total, nil
}

// Archive marks one paper archived without deleting its points.
func (s *PaperStore) Archive(ctx context.Context, ownerID, paperID string, archived bool) (PaperPage, error) {
	page, err := s.Get(ctx, ownerID, paperID)
	if err != nil {
		return PaperPage{}, err
	}
	page.Archived = archived
	return s.Put(ctx, page)
}

// Stats counts papers for one owner.
func (s *PaperStore) Stats(ctx context.Context, ownerID string) Stats {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := Stats{ByKind: map[string]int{}}
	for _, page := range s.papers[ownerID] {
		if page.Archived {
			continue
		}
		stats.Papers++
	}
	return stats
}

// Close releases the process lock and closes WAL files.
func (s *PaperStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for ownerID, file := range s.walFiles {
		_ = s.writeSnapshotLocked(ownerID)
		_ = file.Close()
		delete(s.walFiles, ownerID)
	}
	if s.lockFile != nil {
		_ = s.lockFile.Close()
		_ = os.Remove(filepath.Join(s.root, wikiLockName))
		s.lockFile = nil
	}
	return nil
}
