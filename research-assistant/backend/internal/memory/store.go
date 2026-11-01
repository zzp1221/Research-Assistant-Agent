package memory

import (
	"bufio"
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

// Store is the durable memory interface. Every method takes an owner-scoped
// record or query; there is no cross-owner read path.
type Store interface {
	Put(ctx context.Context, rec Record) (Record, error)
	Get(ctx context.Context, ownerID, id string) (Record, error)
	Update(ctx context.Context, ownerID, id string, patch Patch) (Record, error)
	Delete(ctx context.Context, ownerID, id string, hard bool) error
	Search(ctx context.Context, q Query) ([]ScoredRecord, error)
	Retrieve(ctx context.Context, q Query, coreBudget, retrievedBudget int) (Context, error)
	List(ctx context.Context, q Query) ([]Record, int, error)
	Stats(ctx context.Context, ownerID string) (Stats, error)
	Close() error
}

type walEntry struct {
	Op     string    `json:"op"`
	Record *Record   `json:"record,omitempty"`
	ID     string    `json:"id,omitempty"`
	At     time.Time `json:"at"`
}

type snapshotFile struct {
	Version int      `json:"version"`
	Records []Record `json:"records"`
}

// FileStore persists records in a JSONL WAL plus an atomically replaced
// snapshot. The in-memory map and BM25 index are rebuilt on startup.
type FileStore struct {
	mu       sync.RWMutex
	dir      string
	records  map[string]Record
	byHash   map[string]string
	idx      *Index
	wal      *os.File
	walCount int
	lockPath string
	persist  bool
}

const (
	walName      = "memory.wal.jsonl"
	snapshotName = "memory.snapshot.json"
	lockName     = ".memory.lock"
	compactEvery = 2000
)

// NewFileStore opens (or creates) a memory directory.
func NewFileStore(dir string) (*FileStore, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("memory directory is empty")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create memory directory: %w", err)
	}
	s := &FileStore{
		dir:      dir,
		records:  map[string]Record{},
		byHash:   map[string]string{},
		idx:      NewIndex(),
		persist:  true,
		lockPath: filepath.Join(dir, lockName),
	}
	if err := s.acquireLock(); err != nil {
		return nil, err
	}
	if err := s.loadSnapshot(); err != nil {
		s.releaseLock()
		return nil, err
	}
	if err := s.replayWAL(); err != nil {
		s.releaseLock()
		return nil, err
	}
	for _, rec := range s.records {
		s.indexRecord(rec)
	}
	wal, err := os.OpenFile(filepath.Join(dir, walName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		s.releaseLock()
		return nil, fmt.Errorf("open memory WAL: %w", err)
	}
	s.wal = wal
	return s, nil
}

// NewMemoryStore creates an in-memory store for tests and embedded use.
func NewMemoryStore() *FileStore {
	return &FileStore{
		records: map[string]Record{},
		byHash:  map[string]string{},
		idx:     NewIndex(),
		persist: false,
	}
}

func (s *FileStore) acquireLock() error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d\n%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339Nano))
			_ = f.Close()
			return nil
		}
		if !os.IsExist(err) {
			return fmt.Errorf("create memory lock: %w", err)
		}
		if info, statErr := os.Stat(s.lockPath); statErr == nil && time.Since(info.ModTime()) > 10*time.Minute {
			_ = os.Remove(s.lockPath)
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("memory store is locked by another process: %s", s.lockPath)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *FileStore) releaseLock() {
	if s.persist && s.lockPath != "" {
		_ = os.Remove(s.lockPath)
	}
}

func (s *FileStore) loadSnapshot() error {
	raw, err := os.ReadFile(filepath.Join(s.dir, snapshotName))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read memory snapshot: %w", err)
	}
	var snap snapshotFile
	if err := json.Unmarshal(raw, &snap); err != nil {
		return fmt.Errorf("decode memory snapshot: %w", err)
	}
	for _, rec := range snap.Records {
		s.records[rec.ID] = rec
		if rec.ContentHash != "" {
			s.byHash[rec.Scope.OwnerID+"|"+rec.ContentHash] = rec.ID
		}
	}
	return nil
}

func (s *FileStore) replayWAL() error {
	path := filepath.Join(s.dir, walName)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open memory WAL: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry walEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			// A torn tail write is expected after a crash; stop replaying at
			// the first unreadable line instead of losing the whole store.
			break
		}
		s.walCount++
		switch entry.Op {
		case "put":
			if entry.Record != nil && entry.Record.ID != "" {
				s.records[entry.Record.ID] = *entry.Record
				if entry.Record.ContentHash != "" {
					s.byHash[entry.Record.Scope.OwnerID+"|"+entry.Record.ContentHash] = entry.Record.ID
				}
			}
		case "delete":
			delete(s.records, entry.ID)
		}
	}
	return scanner.Err()
}

func (s *FileStore) indexRecord(rec Record) {
	if rec.Status == StatusDeleted {
		return
	}
	s.idx.Add(rec)
	if rec.ContentHash != "" {
		s.byHash[rec.Scope.OwnerID+"|"+rec.ContentHash] = rec.ID
	}
}

func (s *FileStore) Put(ctx context.Context, rec Record) (Record, error) {
	_ = ctx
	if strings.TrimSpace(rec.Scope.OwnerID) == "" {
		return Record{}, fmt.Errorf("memory owner_id is required")
	}
	if !rec.Kind.Valid() {
		return Record{}, fmt.Errorf("invalid memory kind: %s", rec.Kind)
	}
	now := time.Now().UTC()
	rec.Normalize(now)
	if err := ValidateRecord(&rec); err != nil {
		return Record{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	hashKey := rec.Scope.OwnerID + "|" + rec.ContentHash
	if existingID, ok := s.byHash[hashKey]; ok {
		if existing, exists := s.records[existingID]; exists && existing.Status != StatusDeleted {
			existing.UseCount++
			existing.LastUsedAt = &now
			existing.UpdatedAt = now
			s.records[existingID] = existing
			s.indexRecord(existing)
			if err := s.appendWAL(walEntry{Op: "put", Record: &existing, At: now}); err != nil {
				return Record{}, err
			}
			return existing, nil
		}
	}

	if strings.TrimSpace(rec.ID) == "" {
		rec.ID = "mem-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	}
	if previous, exists := s.records[rec.ID]; exists {
		rec.Version = previous.Version + 1
		rec.CreatedAt = previous.CreatedAt
		if previous.ContentHash != rec.ContentHash {
			delete(s.byHash, previous.Scope.OwnerID+"|"+previous.ContentHash)
			previous.Status = StatusSuperseded
			previous.Validity.ValidTo = &now
			previous.Validity.SupersededBy = rec.ID
			previous.UpdatedAt = now
			s.records[previous.ID] = previous
			s.indexRecord(previous)
		}
		if previous.Trust == TrustUserExplicit && rec.Trust != TrustUserExplicit && previous.ContentHash != rec.ContentHash {
			rec.Status = StatusPending
			rec.Conflict = true
		}
	}
	rec.UpdatedAt = now
	s.records[rec.ID] = rec
	s.indexRecord(rec)
	if err := s.appendWAL(walEntry{Op: "put", Record: &rec, At: now}); err != nil {
		return Record{}, err
	}
	if s.walCount >= compactEvery {
		if err := s.compactLocked(); err != nil {
			return Record{}, err
		}
	}
	return rec, nil
}

func (s *FileStore) Get(ctx context.Context, ownerID, id string) (Record, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	if !ok || rec.Scope.OwnerID != ownerID || rec.Status == StatusDeleted {
		return Record{}, fmt.Errorf("memory not found")
	}
	return rec, nil
}

func (s *FileStore) Update(ctx context.Context, ownerID, id string, patch Patch) (Record, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok || rec.Scope.OwnerID != ownerID || rec.Status == StatusDeleted {
		return Record{}, fmt.Errorf("memory not found")
	}
	if patch.Title != nil {
		rec.Title = strings.TrimSpace(*patch.Title)
	}
	if patch.Content != nil {
		rec.Content = strings.TrimSpace(*patch.Content)
	}
	if patch.Tags != nil {
		rec.Tags = append([]string(nil), (*patch.Tags)...)
	}
	if patch.Importance != nil {
		rec.Importance = *patch.Importance
	}
	if patch.Pinned != nil {
		rec.Pinned = *patch.Pinned
	}
	if patch.Status != nil {
		rec.Status = *patch.Status
	}
	rec.Version++
	rec.UpdatedAt = time.Now().UTC()
	delete(s.byHash, rec.Scope.OwnerID+"|"+rec.ContentHash)
	rec.ContentHash = ContentHash(rec.Kind, rec.Scope, rec.Title, rec.Content)
	rec.Tokens = EstimateTokens(RenderPlain(rec))
	if err := ValidateRecord(&rec); err != nil {
		return Record{}, err
	}
	s.records[id] = rec
	s.indexRecord(rec)
	if err := s.appendWAL(walEntry{Op: "put", Record: &rec, At: rec.UpdatedAt}); err != nil {
		return Record{}, err
	}
	return rec, nil
}

func (s *FileStore) Delete(ctx context.Context, ownerID, id string, hard bool) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok || rec.Scope.OwnerID != ownerID {
		return fmt.Errorf("memory not found")
	}
	now := time.Now().UTC()
	if hard {
		delete(s.records, id)
		s.idx.Remove(rec)
		if rec.ContentHash != "" {
			delete(s.byHash, rec.Scope.OwnerID+"|"+rec.ContentHash)
		}
		return s.appendWAL(walEntry{Op: "delete", ID: id, At: now})
	}
	rec.Status = StatusDeleted
	rec.UpdatedAt = now
	s.records[id] = rec
	s.idx.Remove(rec)
	return s.appendWAL(walEntry{Op: "put", Record: &rec, At: now})
}

func (s *FileStore) Search(ctx context.Context, q Query) ([]ScoredRecord, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.searchLocked(q), nil
}

func (s *FileStore) List(ctx context.Context, q Query) ([]Record, int, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	matches := make([]Record, 0)
	for _, rec := range s.records {
		if !matchQuery(rec, q) {
			continue
		}
		if q.Text != "" {
			needle := strings.ToLower(strings.TrimSpace(q.Text))
			if !strings.Contains(strings.ToLower(rec.Title+" "+rec.Content), needle) {
				continue
			}
		}
		matches = append(matches, rec)
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Pinned != matches[j].Pinned {
			return matches[i].Pinned
		}
		return matches[i].UpdatedAt.After(matches[j].UpdatedAt)
	})
	total := len(matches)
	offset := 0
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if q.TopK > 0 && q.TopK < len(matches) {
		// TopK is used as an offset in list contexts only through Limit.
	}
	if offset >= len(matches) {
		return []Record{}, total, nil
	}
	end := offset + limit
	if end > len(matches) {
		end = len(matches)
	}
	return matches[offset:end], total, nil
}

func (s *FileStore) Stats(ctx context.Context, ownerID string) (Stats, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := Stats{ByKind: map[string]int{}, ByStatus: map[string]int{}, UpdatedAt: time.Now().UTC()}
	for _, rec := range s.records {
		if rec.Scope.OwnerID != ownerID || rec.Status == StatusDeleted {
			continue
		}
		stats.Total++
		if rec.Status == StatusActive {
			stats.Active++
		}
		if rec.Pinned {
			stats.Pinned++
		}
		stats.ByKind[string(rec.Kind)]++
		stats.ByStatus[string(rec.Status)]++
		stats.Tokens += rec.Tokens
		if rec.UpdatedAt.After(stats.UpdatedAt) {
			stats.UpdatedAt = rec.UpdatedAt
		}
	}
	return stats, nil
}

// Owners returns the distinct owner ids present in the store.
func (s *FileStore) Owners() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]struct{}{}
	for _, rec := range s.records {
		if rec.Status != StatusDeleted && rec.Scope.OwnerID != "" {
			seen[rec.Scope.OwnerID] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for owner := range seen {
		out = append(out, owner)
	}
	sort.Strings(out)
	return out
}

func (s *FileStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.persist {
		_ = s.compactLocked()
		if s.wal != nil {
			_ = s.wal.Close()
			s.wal = nil
		}
		s.releaseLock()
	}
	return nil
}

func (s *FileStore) appendWAL(entry walEntry) error {
	if !s.persist || s.wal == nil {
		return nil
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode memory WAL: %w", err)
	}
	if _, err := s.wal.Write(append(raw, byte(10))); err != nil {
		return fmt.Errorf("append memory WAL: %w", err)
	}
	if err := s.wal.Sync(); err != nil {
		return fmt.Errorf("sync memory WAL: %w", err)
	}
	s.walCount++
	return nil
}

func (s *FileStore) compactLocked() error {
	if !s.persist {
		return nil
	}
	records := make([]Record, 0, len(s.records))
	for _, rec := range s.records {
		if rec.Status == StatusDeleted {
			continue
		}
		records = append(records, rec)
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	payload, err := json.MarshalIndent(snapshotFile{Version: 1, Records: records}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode memory snapshot: %w", err)
	}
	tmp := filepath.Join(s.dir, snapshotName+".tmp")
	if err := os.WriteFile(tmp, payload, 0o600); err != nil {
		return fmt.Errorf("write memory snapshot: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, snapshotName)); err != nil {
		return fmt.Errorf("replace memory snapshot: %w", err)
	}
	if s.wal != nil {
		if err := s.wal.Truncate(0); err != nil {
			return fmt.Errorf("truncate memory WAL: %w", err)
		}
		if _, err := s.wal.Seek(0, 0); err != nil {
			return fmt.Errorf("seek memory WAL: %w", err)
		}
	}
	s.walCount = 0
	return nil
}
