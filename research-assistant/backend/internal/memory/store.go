package memory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"research-assistant-backend/internal/lockfile"
)

// Store is the durable memory interface. Every method takes an owner-scoped
// record or query; there is no cross-owner read path.
type Store interface {
	Put(ctx context.Context, rec Record) (Record, error)
	Get(ctx context.Context, ownerID, id string) (Record, error)
	Update(ctx context.Context, ownerID, id string, patch Patch) (Record, error)
	Delete(ctx context.Context, ownerID, id string, hard bool) error
	Search(ctx context.Context, q Query) ([]ScoredRecord, error)
	SearchWithContext(ctx context.Context, q Query, coreBudget, retrievedBudget int) (SearchResult, error)
	Retrieve(ctx context.Context, q Query, coreBudget, retrievedBudget int) (Context, error)
	List(ctx context.Context, q Query) ([]Record, int, error)
	PutBatch(ctx context.Context, records []Record) (imported, skipped int, err error)
	Stats(ctx context.Context, ownerID string) (Stats, error)
	Close() error
}

type walEntry struct {
	Entries  []walEntry `json:"entries,omitempty"`
	Checksum string     `json:"checksum,omitempty"`
	Op       string     `json:"op"`
	Record   *Record    `json:"record,omitempty"`
	ID       string     `json:"id,omitempty"`
	At       time.Time  `json:"at"`
}

type snapshotFile struct {
	Version  int      `json:"version"`
	Records  []Record `json:"records"`
	Checksum string   `json:"checksum,omitempty"`
}

// ErrStorageUnavailable marks persistence failures so API handlers can map them
// to 5xx instead of treating them as invalid request parameters.
var ErrStorageUnavailable = errors.New("memory storage unavailable")

// FileStore persists records in a JSONL WAL plus an atomically replaced
// snapshot. The in-memory map and BM25 index are rebuilt on startup.
type FileStore struct {
	mu          sync.RWMutex
	dir         string
	records     map[string]Record
	byDedupe    map[string]string
	byFact      map[string]string
	byOwner     map[string]map[string]struct{}
	idx         *Index
	wal         *os.File
	walCount    int
	walBytes    int64
	walReplayed bool
	lockPath    string
	persist     bool
}

const (
	walName           = "memory.wal.jsonl"
	snapshotName      = "memory.snapshot.json"
	snapshotBackup    = "memory.snapshot.json.bak"
	recoveryMarker    = ".recovery-required"
	lockName          = ".memory.lock"
	compactEvery      = 2000
	batchCompactEvery = 20000
	batchCompactBytes = 64 << 20
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
	if _, err := os.Stat(filepath.Join(dir, recoveryMarker)); err == nil {
		return nil, fmt.Errorf("memory store requires manual recovery: %s exists", recoveryMarker)
	}
	s := &FileStore{
		dir:      dir,
		records:  map[string]Record{},
		byDedupe: map[string]string{},
		byFact:   map[string]string{},
		byOwner:  map[string]map[string]struct{}{},
		idx:      NewIndex(),
		persist:  true,
		lockPath: filepath.Join(dir, lockName),
	}
	if err := s.acquireLock(); err != nil {
		return nil, err
	}
	snapshotErr := s.loadSnapshot()
	if snapshotErr != nil {
		s.releaseLock()
		return nil, snapshotErr
	}
	if !s.walReplayed {
		if err := s.replayWAL(); err != nil {
			s.releaseLock()
			return nil, err
		}
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
		records:  map[string]Record{},
		byDedupe: map[string]string{},
		byFact:   map[string]string{},
		byOwner:  map[string]map[string]struct{}{},
		idx:      NewIndex(),
		persist:  false,
	}
}

func (s *FileStore) acquireLock() error {
	file, err := lockfile.Acquire(s.lockPath, 3*time.Second)
	if err != nil {
		return fmt.Errorf("memory store lock: %w", err)
	}
	_ = file.Close()
	return nil
}

func (s *FileStore) releaseLock() {
	if s.persist && s.lockPath != "" {
		_ = os.Remove(s.lockPath)
	}
}

func snapshotChecksum(records []Record) string {
	raw, _ := json.Marshal(records)
	return strconv.FormatUint(uint64(crc32.ChecksumIEEE(raw)), 16)
}

func decodeSnapshot(raw []byte) (snapshotFile, error) {
	var snap snapshotFile
	if err := json.Unmarshal(raw, &snap); err != nil {
		return snap, err
	}
	if snap.Checksum != "" && !strings.EqualFold(snap.Checksum, snapshotChecksum(snap.Records)) {
		return snap, fmt.Errorf("snapshot checksum mismatch")
	}
	return snap, nil
}

func (s *FileStore) applySnapshot(snap snapshotFile) {
	for _, rec := range snap.Records {
		if rec.ContentHash == "" {
			rec.ContentHash = ContentHash(rec.Kind, rec.Scope, rec.Title, rec.Content)
		}
		s.records[rec.ID] = rec
	}
}

func (s *FileStore) loadSnapshot() error {
	path := filepath.Join(s.dir, snapshotName)
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read memory snapshot: %w", err)
	}
	snap, decodeErr := decodeSnapshot(raw)
	if decodeErr == nil {
		s.applySnapshot(snap)
		return nil
	}
	// Preserve the bad file for forensic inspection and try the last known good
	// backup before declaring the store unrecoverable.
	corruptPath := fmt.Sprintf("%s.corrupt-%d", path, time.Now().UTC().UnixNano())
	_ = os.Rename(path, corruptPath)
	if backupRaw, backupErr := os.ReadFile(filepath.Join(s.dir, snapshotBackup)); backupErr == nil {
		if backupSnap, backupDecodeErr := decodeSnapshot(backupRaw); backupDecodeErr == nil {
			s.applySnapshot(backupSnap)
			_ = os.WriteFile(path, backupRaw, 0o600)
			return nil
		}
	}
	// No usable backup: replay the WAL so a torn/removed snapshot does not
	// silently discard every committed record.
	if replayErr := s.replayWAL(); replayErr == nil && len(s.records) > 0 {
		s.walReplayed = true
		return nil
	}
	marker := filepath.Join(s.dir, recoveryMarker)
	_ = os.WriteFile(marker, []byte("memory snapshot is corrupt and the WAL is empty; manual recovery required\n"), 0o600)
	return fmt.Errorf("decode memory snapshot: %w", decodeErr)
}

func (s *FileStore) replayWAL() error {
	path := filepath.Join(s.dir, walName)
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		s.walReplayed = true
		return nil
	}
	if err != nil {
		return fmt.Errorf("open memory WAL: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 128*1024*1024)
	var goodBytes int64
	brokenTail := false
	for scanner.Scan() {
		lineBytes := int64(len(scanner.Bytes()) + 1)
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			goodBytes += lineBytes
			continue
		}
		var entry walEntry
		decodeErr := json.Unmarshal([]byte(line), &entry)
		if decodeErr == nil && entry.Op == "transaction" && entry.Checksum != walChecksum(entry.Entries) {
			decodeErr = fmt.Errorf("transaction checksum mismatch")
		}
		if decodeErr != nil {
			// Only an incomplete final frame can be discarded. Corruption in
			// the middle must not silently discard later committed writes.
			for scanner.Scan() {
				if strings.TrimSpace(scanner.Text()) != "" {
					return fmt.Errorf("memory WAL is corrupt before committed entries: %w", decodeErr)
				}
			}
			brokenTail = true
			break
		}
		entries := []walEntry{entry}
		if entry.Op == "transaction" {
			entries = entry.Entries
		}
		for _, entry := range entries {
			s.walCount++
			switch entry.Op {
			case "put":
				if entry.Record != nil && entry.Record.ID != "" {
					rec := *entry.Record
					if rec.ContentHash == "" {
						rec.ContentHash = ContentHash(rec.Kind, rec.Scope, rec.Title, rec.Content)
					}
					s.records[rec.ID] = rec
				}
			case "delete":
				delete(s.records, entry.ID)
			}
		}
		goodBytes += lineBytes
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if brokenTail {
		if err := os.Truncate(path, goodBytes); err != nil {
			return fmt.Errorf("repair torn memory WAL tail: %w", err)
		}
	} else if info, err := file.Stat(); err == nil && info.Size() > 0 {
		last := []byte{0}
		if _, err := file.ReadAt(last, info.Size()-1); err == nil && last[0] != '\n' {
			repair, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			_, writeErr := repair.Write([]byte{'\n'})
			syncErr := repair.Sync()
			_ = repair.Close()
			if writeErr != nil {
				return writeErr
			}
			if syncErr != nil {
				return syncErr
			}
		}
	}
	s.walReplayed = true
	return nil
}

func (s *FileStore) indexRecord(rec Record) {
	if rec.ContentHash == "" {
		rec.ContentHash = ContentHash(rec.Kind, rec.Scope, rec.Title, rec.Content)
	}
	if rec.Status == StatusDeleted {
		s.removeOwnerRecord(rec.Scope.OwnerID, rec.ID)
		return
	}
	if rec.Scope.OwnerID != "" {
		if s.byOwner[rec.Scope.OwnerID] == nil {
			s.byOwner[rec.Scope.OwnerID] = map[string]struct{}{}
		}
		s.byOwner[rec.Scope.OwnerID][rec.ID] = struct{}{}
	}
	s.idx.Add(rec)
	if identity, _ := recordIdentity(rec); identity != "" {
		if rec.Status == StatusActive {
			s.byFact[identity] = rec.ID
		} else if s.byFact[identity] == rec.ID {
			delete(s.byFact, identity)
		}
	}
	key := rec.DedupeKey()
	if key == "" {
		return
	}
	if rec.Status == StatusActive || rec.Status == StatusPending {
		s.byDedupe[key] = rec.ID
	} else if s.byDedupe[key] == rec.ID {
		delete(s.byDedupe, key)
	}
}

func (s *FileStore) removeOwnerRecord(ownerID, id string) {
	if ownerID == "" {
		return
	}
	if set := s.byOwner[ownerID]; set != nil {
		delete(set, id)
		if len(set) == 0 {
			delete(s.byOwner, ownerID)
		}
	}
}

func (s *FileStore) Put(ctx context.Context, rec Record) (Record, error) {
	_ = ctx
	rec = cloneRecord(rec)
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
	if existing, ok := s.records[rec.ID]; ok && existing.Scope.OwnerID != rec.Scope.OwnerID {
		return Record{}, fmt.Errorf("memory id belongs to another owner")
	}
	if identity, ambiguous := recordIdentity(rec); identity != "" && rec.Status == StatusActive {
		if id, found := s.byFact[identity]; found && id != rec.ID {
			previous := s.records[id]
			if previous.ContentHash != rec.ContentHash {
				if ambiguous {
					rec.Status, rec.Conflict = StatusPending, true
					if rec.Attributes == nil {
						rec.Attributes = map[string]any{}
					}
					rec.Attributes["conflicts_with"] = id
				} else {
					rec.ID = id
				}
			}
		}
	}

	if existingID, ok := s.byDedupe[rec.DedupeKey()]; ok {
		if existing, exists := s.records[existingID]; exists && existing.DedupeKey() == rec.DedupeKey() && existing.Status != StatusDeleted && existing.Status != StatusSuperseded && existing.Status != StatusArchived {
			updated := existing
			updated.UseCount++
			updated.LastUsedAt = &now
			updated.UpdatedAt = now
			if err := s.appendWAL(walEntry{Op: "put", Record: &updated, At: now}); err != nil {
				return Record{}, err
			}
			s.records[existingID] = updated
			s.indexRecord(updated)
			return cloneRecord(updated), nil
		}
	}

	if strings.TrimSpace(rec.ID) == "" {
		rec.ID = "mem-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
	}
	identifyRevision(&rec)
	entries := make([]walEntry, 0, 2)
	var previous Record
	hasPrevious := false
	if existing, exists := s.records[rec.ID]; exists {
		previous = cloneRecord(existing)
		hasPrevious = true
		if previous.ID == previous.RevisionID {
			return Record{}, fmt.Errorf("historical memory revision is immutable")
		}
		rec.Version = previous.Version + 1
		rec.CreatedAt = previous.CreatedAt
		rec.FactID = previous.FactID
		if rec.FactID == "" {
			rec.FactID = previous.ID
		}
		rec.RevisionID = revisionID(rec.FactID, rec.Version)
		previous = closeRevision(previous, &rec, now)
		entries = append(entries, walEntry{Op: "put", Record: &previous, At: now})
		if previous.Trust == TrustUserExplicit && rec.Trust != TrustUserExplicit && previous.ContentHash != rec.ContentHash {
			rec.Status = StatusPending
			rec.Conflict = true
			// Preserve the trusted current fact until a user approves the candidate.
			rec.ID = "mem-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
			rec.FactID, rec.RevisionID = rec.ID, ""
			rec.Version = 1
			rec.Validity = Validity{RecordedAt: now}
			if rec.Attributes == nil {
				rec.Attributes = map[string]any{}
			}
			rec.Attributes["conflicts_with"] = existing.ID
			identifyRevision(&rec)
			entries = nil
			hasPrevious = false
		}
	}
	rec.UpdatedAt = now
	entries = append(entries, walEntry{Op: "put", Record: &rec, At: now})
	if err := s.appendWALBatch(entries); err != nil {
		return Record{}, err
	}
	if hasPrevious && previous.Status == StatusSuperseded {
		s.records[previous.ID] = previous
		s.indexRecord(previous)
	}
	if old, ok := s.records[rec.ID]; ok {
		s.removeRecordIndexes(old)
	}
	s.records[rec.ID] = rec
	s.indexRecord(rec)
	if s.walCount >= compactEvery {
		_ = s.compactLocked()
	}
	return cloneRecord(rec), nil
}

func (s *FileStore) Get(ctx context.Context, ownerID, id string) (Record, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[id]
	if !ok || rec.Scope.OwnerID != ownerID || rec.Status == StatusDeleted {
		return Record{}, fmt.Errorf("memory not found")
	}
	return cloneRecord(rec), nil
}

func (s *FileStore) Update(ctx context.Context, ownerID, id string, patch Patch) (Record, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[id]
	if !ok || rec.Scope.OwnerID != ownerID || rec.Status == StatusDeleted {
		return Record{}, fmt.Errorf("memory not found")
	}
	if rec.ID == rec.RevisionID {
		return Record{}, fmt.Errorf("historical memory revision is immutable")
	}
	rec = cloneRecord(rec)
	previous := cloneRecord(rec)
	oldKey := rec.DedupeKey()
	if patch.procedureUse != nil {
		changed, err := applyProcedureUse(&rec, *patch.procedureUse)
		if err != nil {
			return Record{}, err
		}
		if !changed {
			return cloneRecord(rec), nil
		}
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
	if patch.Refs != nil {
		rec.Refs = append([]SourceRef(nil), (*patch.Refs)...)
	}
	if patch.Attributes != nil {
		rec.Attributes = map[string]any{}
		for key, value := range *patch.Attributes {
			rec.Attributes[key] = value
		}
	}
	rec.Version++
	rec.UpdatedAt = time.Now().UTC()
	identifyRevision(&rec)
	rec.RevisionID = revisionID(rec.FactID, rec.Version)
	rec.ContentHash = ContentHash(rec.Kind, rec.Scope, rec.Title, rec.Content)
	rec.Tokens = EstimateTokens(RenderPlain(rec))
	if err := ValidateRecord(&rec); err != nil {
		return Record{}, err
	}
	var history *Record
	entries := make([]walEntry, 0, 2)
	{
		old := closeRevision(previous, &rec, rec.UpdatedAt)
		history = &old
		entries = append(entries, walEntry{Op: "put", Record: history, At: rec.UpdatedAt})
	}
	entries = append(entries, walEntry{Op: "put", Record: &rec, At: rec.UpdatedAt})
	if err := s.appendWALBatch(entries); err != nil {
		return Record{}, err
	}
	if oldKey != rec.DedupeKey() && s.byDedupe[oldKey] == id {
		delete(s.byDedupe, oldKey)
	}
	s.removeRecordIndexes(previous)
	s.records[id] = rec
	if history != nil {
		s.records[history.ID] = *history
		s.indexRecord(*history)
	}
	if rec.Status == StatusDeleted {
		s.idx.Remove(rec)
		s.removeOwnerRecord(rec.Scope.OwnerID, id)
		if s.byDedupe[rec.DedupeKey()] == id {
			delete(s.byDedupe, rec.DedupeKey())
		}
	} else {
		s.indexRecord(rec)
	}
	return cloneRecord(rec), nil
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
	identifyRevision(&rec)
	entries := make([]walEntry, 0)
	for _, candidate := range s.records {
		if candidate.Scope.OwnerID != ownerID {
			continue
		}
		identifyRevision(&candidate)
		if candidate.FactID != rec.FactID {
			continue
		}
		if hard {
			entries = append(entries, walEntry{Op: "delete", ID: candidate.ID, At: now})
		} else {
			candidate.Status, candidate.UpdatedAt = StatusDeleted, now
			entries = append(entries, walEntry{Op: "put", Record: &candidate, At: now})
		}
	}
	if err := s.appendWALBatch(entries); err != nil {
		return err
	}
	for _, entry := range entries {
		recordID := entry.ID
		if entry.Record != nil {
			recordID = entry.Record.ID
		}
		s.removeRecordIndexes(s.records[recordID])
		s.removeOwnerRecord(ownerID, recordID)
		if hard {
			delete(s.records, recordID)
		} else {
			s.records[recordID] = *entry.Record
		}
	}
	return nil
}

func (s *FileStore) removeRecordIndexes(rec Record) {
	s.idx.Remove(rec)
	if key := rec.DedupeKey(); s.byDedupe[key] == rec.ID {
		delete(s.byDedupe, key)
	}
	if identity, _ := recordIdentity(rec); identity != "" && s.byFact[identity] == rec.ID {
		delete(s.byFact, identity)
	}
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
	// List is the owner-scoped management surface (list/export/admin). Strict
	// project/session scoping applies to retrieval (Search/Retrieve).
	listQuery := q
	listQuery.AllScopes = true
	matches := make([]Record, 0)
	for id := range s.byOwner[listQuery.OwnerID] {
		rec, ok := s.records[id]
		if !ok || !matchQuery(rec, listQuery) {
			continue
		}
		if q.Text != "" {
			needle := strings.ToLower(strings.TrimSpace(q.Text))
			if !strings.Contains(strings.ToLower(rec.Title+" "+rec.Content), needle) {
				continue
			}
		}
		matches = append(matches, cloneRecord(rec))
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
	for id := range s.byOwner[ownerID] {
		rec, ok := s.records[id]
		if !ok || rec.Scope.OwnerID != ownerID || rec.Status == StatusDeleted {
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
	for owner, records := range s.byOwner {
		if owner != "" && len(records) > 0 {
			seen[owner] = struct{}{}
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
	return s.appendWALBatch([]walEntry{entry})
}

func (s *FileStore) appendWALBatch(entries []walEntry) error {
	if len(entries) == 0 || !s.persist {
		return nil
	}
	if s.wal == nil {
		return fmt.Errorf("%w: store is closed", ErrStorageUnavailable)
	}
	var buffer bytes.Buffer
	frames := []walEntry{{Op: "transaction", Entries: entries, Checksum: walChecksum(entries), At: time.Now().UTC()}}
	for index := range frames {
		raw, err := json.Marshal(frames[index])
		if err != nil {
			return fmt.Errorf("encode memory WAL: %w", err)
		}
		buffer.Write(raw)
		buffer.WriteByte('\n')
	}
	info, err := s.wal.Stat()
	if err != nil {
		return fmt.Errorf("%w: stat memory WAL: %v", ErrStorageUnavailable, err)
	}
	written, writeErr := s.wal.Write(buffer.Bytes())
	if writeErr == nil && written != buffer.Len() {
		writeErr = fmt.Errorf("short WAL write")
	}
	if writeErr == nil {
		writeErr = s.wal.Sync()
	}
	if writeErr != nil {
		// An unacknowledged transaction must not become visible after restart.
		rollbackErr := s.wal.Truncate(info.Size())
		if rollbackErr == nil {
			rollbackErr = s.wal.Sync()
		}
		if rollbackErr != nil {
			_ = os.WriteFile(filepath.Join(s.dir, recoveryMarker), []byte("WAL rollback failed; manual recovery required\n"), 0o600)
			_ = s.wal.Close()
			s.wal = nil
		}
		return fmt.Errorf("%w: commit memory WAL: %v (rollback: %v)", ErrStorageUnavailable, writeErr, rollbackErr)
	}
	s.walCount += len(entries)
	s.walBytes += int64(buffer.Len())
	return nil
}

func walChecksum(entries []walEntry) string {
	raw, _ := json.Marshal(entries)
	return strconv.FormatUint(uint64(crc32.ChecksumIEEE(raw)), 16)
}

// PutBatch imports a slice of records under one lock and one WAL fsync. It is
// used by /api/memories/import so 100k records do not pay one fsync each.
func (s *FileStore) PutBatch(ctx context.Context, records []Record) (int, int, error) {
	_ = ctx
	if len(records) == 0 {
		return 0, 0, nil
	}
	now := time.Now().UTC()
	prepared := make([]Record, 0, len(records))
	skipped := 0
	for index := range records {
		rec := cloneRecord(records[index])
		if strings.TrimSpace(rec.Scope.OwnerID) == "" || !rec.Kind.Valid() {
			skipped++
			continue
		}
		updatedAt := rec.UpdatedAt
		rec.Normalize(now)
		if !updatedAt.IsZero() {
			rec.UpdatedAt = updatedAt
		}
		if err := ValidateRecord(&rec); err != nil {
			skipped++
			continue
		}
		prepared = append(prepared, rec)
	}
	if len(prepared) == 0 {
		return 0, skipped, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	final := map[string]Record{}
	batchSeen := map[string]string{}
	batchFacts := map[string]string{}
	for _, rec := range prepared {
		if staged, ok := final[rec.ID]; ok && staged.Scope.OwnerID != rec.Scope.OwnerID {
			return 0, skipped, fmt.Errorf("memory id belongs to another owner")
		}
		if existing, ok := s.records[rec.ID]; ok && existing.Scope.OwnerID != rec.Scope.OwnerID {
			return 0, skipped, fmt.Errorf("memory id belongs to another owner")
		}
		identity, ambiguous := recordIdentity(rec)
		if identity != "" && rec.Status == StatusActive {
			id := batchFacts[identity]
			if id == "" {
				id = s.byFact[identity]
			}
			if id != "" && id != rec.ID {
				previous := s.records[id]
				if staged, ok := final[id]; ok {
					previous = staged
				}
				if previous.ContentHash != rec.ContentHash {
					if ambiguous {
						rec.Status, rec.Conflict = StatusPending, true
						if rec.Attributes == nil {
							rec.Attributes = map[string]any{}
						}
						rec.Attributes["conflicts_with"] = id
					} else {
						rec.ID = id
					}
				}
			}
		}
		key := rec.DedupeKey()
		if existingID, ok := s.byDedupe[key]; ok && (rec.Status == StatusActive || rec.Status == StatusPending) {
			if existing, exists := s.records[existingID]; exists && existing.DedupeKey() == key && (existing.Status == StatusActive || existing.Status == StatusPending) {
				base := existing
				if pending, done := final[existingID]; done {
					base = pending
				}
				if base.DedupeKey() == key {
					base.UseCount++
					base.LastUsedAt = &now
					base.UpdatedAt = now
					final[existingID] = base
					continue
				}
			}
		}
		if existingID, ok := batchSeen[key]; ok && final[existingID].DedupeKey() == key && (rec.Status == StatusActive || rec.Status == StatusPending) {
			base := final[existingID]
			base.UseCount++
			base.LastUsedAt = &now
			base.UpdatedAt = now
			final[existingID] = base
			continue
		}
		if strings.TrimSpace(rec.ID) == "" {
			rec.ID = "mem-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
		}
		identifyRevision(&rec)
		previous, exists := final[rec.ID]
		if !exists {
			previous, exists = s.records[rec.ID]
		}
		if exists {
			if previous.ID == previous.RevisionID {
				if previous.ContentHash != rec.ContentHash {
					return 0, skipped, fmt.Errorf("historical memory revision is immutable")
				}
				continue
			}
			rec.Version = previous.Version + 1
			rec.CreatedAt = previous.CreatedAt
			rec.FactID = previous.FactID
			if rec.FactID == "" {
				rec.FactID = previous.ID
			}
			rec.RevisionID = revisionID(rec.FactID, rec.Version)
			{
				if previous.Trust == TrustUserExplicit && rec.Trust != TrustUserExplicit {
					rec.Status, rec.Conflict = StatusPending, true
					rec.ID = "mem-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
					rec.FactID, rec.RevisionID = rec.ID, ""
					rec.Version = 1
					rec.Validity = Validity{RecordedAt: now}
					if rec.Attributes == nil {
						rec.Attributes = map[string]any{}
					}
					rec.Attributes["conflicts_with"] = previous.ID
					identifyRevision(&rec)
				} else {
					old := closeRevision(cloneRecord(previous), &rec, now)
					final[old.ID] = old
				}
			}
			if previous.Trust == TrustUserExplicit && rec.Trust != TrustUserExplicit && previous.ContentHash != rec.ContentHash {
				rec.Status = StatusPending
				rec.Conflict = true
			}
		}
		final[rec.ID] = rec
		if rec.Status == StatusActive || rec.Status == StatusPending {
			batchSeen[key] = rec.ID
		}
		if identity != "" && rec.Status == StatusActive {
			batchFacts[identity] = rec.ID
		}
	}
	ids := make([]string, 0, len(final))
	for id := range final {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	entries := make([]walEntry, 0, len(ids))
	for _, id := range ids {
		record := final[id]
		entries = append(entries, walEntry{Op: "put", Record: &record, At: now})
	}
	if err := s.appendWALBatch(entries); err != nil {
		return 0, skipped, err
	}
	for _, id := range ids {
		if old, ok := s.records[id]; ok {
			s.removeRecordIndexes(old)
		}
		s.records[id] = final[id]
		s.indexRecord(final[id])
	}
	if s.walCount >= batchCompactEvery || s.walBytes >= batchCompactBytes {
		_ = s.compactLocked()
	}
	return len(prepared), skipped, nil
}

func writeFileSynced(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
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
	payload, err := json.MarshalIndent(snapshotFile{Version: 1, Records: records, Checksum: snapshotChecksum(records)}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode memory snapshot: %w", err)
	}
	target := filepath.Join(s.dir, snapshotName)
	tmp := target + ".tmp"
	if err := writeFileSynced(tmp, payload); err != nil {
		return fmt.Errorf("write memory snapshot: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		return fmt.Errorf("replace memory snapshot: %w", err)
	}
	// Keep a second copy so a single corrupted primary file can still recover.
	backup := filepath.Join(s.dir, snapshotBackup)
	backupTmp := backup + ".tmp"
	if err := writeFileSynced(backupTmp, payload); err == nil {
		_ = os.Rename(backupTmp, backup)
	}
	if s.wal != nil {
		if err := s.wal.Truncate(0); err != nil {
			return fmt.Errorf("truncate memory WAL: %w", err)
		}
		if _, err := s.wal.Seek(0, 0); err != nil {
			return fmt.Errorf("seek memory WAL: %w", err)
		}
		_ = s.wal.Sync()
	}
	s.walCount = 0
	s.walBytes = 0
	return nil
}
