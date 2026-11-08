// Package contextwindow stores model activity separately from authoritative
// user history. Files are private, versioned and never exported in diagnostics.
package contextwindow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"research-assistant-backend/internal/lockfile"
	"research-assistant-backend/internal/responses"
)

type Observation struct {
	Tokens     *int   `json:"tokens,omitempty"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
}
type CompactionRecord struct {
	Purpose        string           `json:"purpose"`
	Mode           string           `json:"mode"`
	Status         string           `json:"status"`
	SourceHash     string           `json:"source_hash"`
	Attempts       int              `json:"request_attempts"`
	LatencyMS      int64            `json:"latency_ms"`
	Usage          *responses.Usage `json:"usage,omitempty"`
	UsageEstimated bool             `json:"usage_estimated,omitempty"`
	CostUSD        *float64         `json:"cost_usd,omitempty"`
	ErrorCode      string           `json:"error_code,omitempty"`
	At             time.Time        `json:"at"`
}
type State struct {
	Generation      int                `json:"generation"`
	Binding         string             `json:"binding"`
	ProfileID       string             `json:"profile_id"`
	ProfileRevision int                `json:"profile_revision"`
	Mode            string             `json:"mode"`
	Source          []string           `json:"source_hashes"`
	Window          []responses.Item   `json:"window,omitempty"`
	Summary         json.RawMessage    `json:"summary,omitempty"`
	SummaryThrough  int                `json:"summary_through,omitempty"`
	LastInputHash   string             `json:"last_input_hash,omitempty"`
	LastText        string             `json:"last_text,omitempty"`
	Observation     Observation        `json:"token_observation"`
	Compactions     []CompactionRecord `json:"compactions,omitempty"`
}
type entry struct {
	gate chan struct{}
	refs int
}
type Store struct {
	dir   string
	mu    sync.Mutex
	locks map[string]*entry
}

func NewStore(dir string) *Store { return &Store{dir: dir, locks: map[string]*entry{}} }
func Hash(value []byte) string   { hash := sha256.Sum256(value); return hex.EncodeToString(hash[:]) }

// Acquire serializes a branch's logical calls. The store metadata mutex is
// never held over network I/O, and idle branch locks are released.
func (s *Store) Acquire(ctx context.Context, key string) (func(), error) {
	s.mu.Lock()
	e := s.locks[key]
	if e == nil {
		e = &entry{gate: make(chan struct{}, 1)}
		s.locks[key] = e
	}
	e.refs++
	s.mu.Unlock()
	releaseRef := func() {
		s.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(s.locks, key)
		}
		s.mu.Unlock()
	}
	select {
	case e.gate <- struct{}{}:
		if s.dir == "" {
			return func() { <-e.gate; releaseRef() }, nil
		}
		dir := filepath.Dir(s.path(key, 0))
		if err := os.MkdirAll(dir, 0700); err != nil {
			<-e.gate
			releaseRef()
			return nil, err
		}
		path := filepath.Join(dir, ".turn.lock")
		file, err := lockfile.Acquire(path, 2*time.Second)
		if err != nil {
			<-e.gate
			releaseRef()
			return nil, fmt.Errorf("context_turn_conflict: another writer owns the branch")
		}
		return func() { _ = lockfile.Release(file, path); <-e.gate; releaseRef() }, nil
	case <-ctx.Done():
		releaseRef()
		return nil, ctx.Err()
	}
}
func (s *Store) path(key string, generation int) string {
	if generation == 0 {
		return filepath.Join(s.dir, Hash([]byte(key)), "current.json")
	}
	return filepath.Join(s.dir, Hash([]byte(key)), fmt.Sprintf("generation-%08d.json", generation))
}
func (s *Store) Load(key string) (State, error) {
	raw, err := os.ReadFile(s.path(key, 0))
	if os.IsNotExist(err) {
		return State{}, nil
	}
	if err != nil {
		return State{}, err
	}
	var state State
	if err := json.Unmarshal(raw, &state); err != nil {
		return State{}, fmt.Errorf("context_state_invalid")
	}
	return state, nil
}

// Archive preserves submitted visible source material even when preflight or
// generation fails. It is not an index or a model activity window.
func (s *Store) Archive(key, hash string, raw []byte) error {
	dir := filepath.Join(filepath.Dir(s.path(key, 0)), "sources")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, hash+".json"), raw)
}

// Commit checks the source generation and writes a recoverable immutable
// generation before atomically replacing the current pointer.
// Callers hold Acquire for the complete logical turn (single process writer).
func (s *Store) Commit(key string, expected int, state *State) error {
	current, err := s.Load(key)
	if err != nil {
		return err
	}
	if current.Generation != expected {
		return fmt.Errorf("context_revision_conflict")
	}
	next := *state
	next.Generation = expected + 1
	if err := os.MkdirAll(filepath.Dir(s.path(key, 0)), 0700); err != nil {
		return err
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := atomicWrite(s.path(key, next.Generation), raw); err != nil {
		return err
	}
	if err := atomicWrite(s.path(key, 0), raw); err != nil {
		return err
	}
	*state = next
	return nil
}
func atomicWrite(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".context-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
