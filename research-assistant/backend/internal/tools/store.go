package tools

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
)

// Registry errors shared by the API layer.
var (
	ErrNotFound = errors.New("tool not found")
	ErrForbidden = errors.New("tool forbidden")
	ErrConflict = errors.New("tool conflict")
	ErrInvalid  = errors.New("tool invalid")
)

// Snapshot is the persisted state of the tool ecosystem.
type Snapshot struct {
	Tools   []models.ToolDefinition `json:"tools"`
	Servers []models.MCPServer      `json:"servers"`
	Grants  []models.ToolGrant      `json:"grants"`
}

// Store persists tools, MCP servers and grants. Update is atomic and serialized
// so concurrent API requests cannot lose a registration.
type Store interface {
	Read() (Snapshot, error)
	Update(mutator func(*Snapshot) error) error
}

// MemoryStore is used by tests and when no persistent path is configured.
type MemoryStore struct {
	mu       sync.Mutex
	snapshot Snapshot
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{snapshot: Snapshot{Tools: []models.ToolDefinition{}, Servers: []models.MCPServer{}, Grants: []models.ToolGrant{}}}
}

func (s *MemoryStore) Read() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSnapshot(s.snapshot), nil
}

func (s *MemoryStore) Update(mutator func(*Snapshot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneSnapshot(s.snapshot)
	if err := mutator(&next); err != nil {
		return err
	}
	s.snapshot = next
	return nil
}

// FileStore keeps the ecosystem in one JSON file next to the plan store.
type FileStore struct {
	mu       sync.Mutex
	path     string
	snapshot Snapshot
	loaded   bool
}

func NewFileStore(path string) (*FileStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("tool store path is empty")
	}
	store := &FileStore{path: path}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *FileStore) load() error {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.snapshot = Snapshot{Tools: []models.ToolDefinition{}, Servers: []models.MCPServer{}, Grants: []models.ToolGrant{}}
			s.loaded = true
			return nil
		}
		return err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("decode tool store %s: %w", s.path, err)
	}
	if snapshot.Tools == nil {
		snapshot.Tools = []models.ToolDefinition{}
	}
	if snapshot.Servers == nil {
		snapshot.Servers = []models.MCPServer{}
	}
	if snapshot.Grants == nil {
		snapshot.Grants = []models.ToolGrant{}
	}
	s.snapshot = snapshot
	s.loaded = true
	return nil
}

func (s *FileStore) Read() (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		if err := s.load(); err != nil {
			return Snapshot{}, err
		}
	}
	return cloneSnapshot(s.snapshot), nil
}

func (s *FileStore) Update(mutator func(*Snapshot) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		if err := s.load(); err != nil {
			return err
		}
	}
	next := cloneSnapshot(s.snapshot)
	if err := mutator(&next); err != nil {
		return err
	}
	if err := s.persistLocked(next); err != nil {
		return err
	}
	s.snapshot = next
	return nil
}

func (s *FileStore) persistLocked(snapshot Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".tools-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := temp.Write(encoded); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, s.path)
}

func cloneSnapshot(source Snapshot) Snapshot {
	out := Snapshot{
		Tools:   append([]models.ToolDefinition(nil), source.Tools...),
		Servers: append([]models.MCPServer(nil), source.Servers...),
		Grants:  append([]models.ToolGrant(nil), source.Grants...),
	}
	return out
}

// Registry is the in-process facade over a Store. It enforces visibility,
// version immutability and builtin protection.
type Registry struct {
	store Store
	now   func() time.Time
}

func NewRegistry(store Store) *Registry {
	return &Registry{store: store, now: time.Now}
}

// SetClock is used by tests to make timestamps deterministic.
func (r *Registry) SetClock(now func() time.Time) { r.now = now }

// EnsureBuiltins seeds the native tools that must exist for the minimum
// closed loop. Seeding is idempotent and never overwrites a user edit of a
// non-builtin tool.
func (r *Registry) EnsureBuiltins() error {
	return r.store.Update(func(snapshot *Snapshot) error {
		now := r.now()
		for _, def := range BuiltinToolDefinitions() {
			found := false
			for i := range snapshot.Tools {
				if snapshot.Tools[i].ID == def.ID {
					found = true
					// Builtin definitions are code-owned: keep them executable even
					// if an old record was disabled.
					snapshot.Tools[i].Builtin = true
					snapshot.Tools[i].Enabled = true
					break
				}
			}
			if !found {
				def.CreatedAt = now
				def.UpdatedAt = now
				snapshot.Tools = append(snapshot.Tools, def)
			}
		}
		return nil
	})
}

// ListTools returns global builtins plus the owner's tools. Archived versions
// are hidden from the picker but still resolvable by ID for history.
func (r *Registry) ListTools(ownerID string) ([]models.ToolDefinition, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return nil, err
	}
	out := make([]models.ToolDefinition, 0, len(snapshot.Tools))
	for _, def := range snapshot.Tools {
		if def.Archived {
			continue
		}
		if def.OwnerID != "" && def.OwnerID != ownerID {
			continue
		}
		out = append(out, def)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// GetTool resolves one tool regardless of archive state so historical records
// remain explainable. Visibility is checked by the caller/API.
func (r *Registry) GetTool(id string) (models.ToolDefinition, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return models.ToolDefinition{}, err
	}
	for _, def := range snapshot.Tools {
		if def.ID == id {
			return def, nil
		}
	}
	return models.ToolDefinition{}, ErrNotFound
}

// GetToolForOwner resolves a tool and enforces owner visibility.
func (r *Registry) GetToolForOwner(ownerID, id string) (models.ToolDefinition, error) {
	def, err := r.GetTool(id)
	if err != nil {
		return def, err
	}
	if def.OwnerID != "" && def.OwnerID != ownerID {
		return models.ToolDefinition{}, ErrForbidden
	}
	return def, nil
}

// SaveTool creates a new tool record. Same ID with a different version is a
// conflict: versions are immutable and must use a distinct ID suffix or an
// explicit upgrade endpoint.
func (r *Registry) SaveTool(def models.ToolDefinition) error {
	if err := ValidateToolDefinition(def); err != nil {
		return err
	}
	return r.store.Update(func(snapshot *Snapshot) error {
		for _, existing := range snapshot.Tools {
			if existing.ID == def.ID {
				return fmt.Errorf("%w: tool %s already exists", ErrConflict, def.ID)
			}
		}
		now := r.now()
		def.CreatedAt = now
		def.UpdatedAt = now
		snapshot.Tools = append(snapshot.Tools, def)
		return nil
	})
}

// UpdateTool applies a metadata patch. Schema/version changes must supply a new
// version and archive the old record, keeping historical calls explainable.
func (r *Registry) UpdateTool(ownerID string, isAdmin bool, id string, patch func(*models.ToolDefinition) error) (models.ToolDefinition, error) {
	var updated models.ToolDefinition
	err := r.store.Update(func(snapshot *Snapshot) error {
		index := -1
		for i := range snapshot.Tools {
			if snapshot.Tools[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			return ErrNotFound
		}
		current := snapshot.Tools[index]
		if current.Builtin && !isAdmin {
			return ErrForbidden
		}
		if current.OwnerID != "" && current.OwnerID != ownerID {
			return ErrForbidden
		}
		if err := patch(&current); err != nil {
			return err
		}
		if err := ValidateToolDefinition(current); err != nil {
			return err
		}
		current.UpdatedAt = r.now()
		snapshot.Tools[index] = current
		updated = current
		return nil
	})
	return updated, err
}

// DisableTool soft-deletes a tool. Historical records keep resolving it.
func (r *Registry) DisableTool(ownerID string, isAdmin bool, id string) error {
	_, err := r.UpdateTool(ownerID, isAdmin, id, func(def *models.ToolDefinition) error {
		def.Enabled = false
		return nil
	})
	return err
}

// ListServers returns global servers plus the owner's.
func (r *Registry) ListServers(ownerID string) ([]models.MCPServer, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return nil, err
	}
	out := make([]models.MCPServer, 0, len(snapshot.Servers))
	for _, server := range snapshot.Servers {
		if server.OwnerID != "" && server.OwnerID != ownerID {
			continue
		}
		out = append(out, server)
	}
	return out, nil
}

func (r *Registry) GetServer(id string) (models.MCPServer, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return models.MCPServer{}, err
	}
	for _, server := range snapshot.Servers {
		if server.ID == id {
			return server, nil
		}
	}
	return models.MCPServer{}, ErrNotFound
}

// SaveServer upserts an MCP server record.
func (r *Registry) SaveServer(server models.MCPServer) error {
	return r.store.Update(func(snapshot *Snapshot) error {
		now := r.now()
		for i := range snapshot.Servers {
			if snapshot.Servers[i].ID == server.ID {
				server.CreatedAt = snapshot.Servers[i].CreatedAt
				server.CreatedBy = snapshot.Servers[i].CreatedBy
				server.UpdatedAt = now
				snapshot.Servers[i] = server
				return nil
			}
		}
		server.CreatedAt = now
		server.UpdatedAt = now
		snapshot.Servers = append(snapshot.Servers, server)
		return nil
	})
}

// ReplaceServerTools atomically swaps the tools discovered from one server.
func (r *Registry) ReplaceServerTools(serverID string, defs []models.ToolDefinition) error {
	return r.store.Update(func(snapshot *Snapshot) error {
		kept := make([]models.ToolDefinition, 0, len(snapshot.Tools)+len(defs))
		for _, existing := range snapshot.Tools {
			if existing.ServerID == serverID && !existing.Archived {
				continue
			}
			kept = append(kept, existing)
		}
		now := r.now()
		for _, def := range defs {
			def.ServerID = serverID
			def.CreatedAt = now
			def.UpdatedAt = now
			kept = append(kept, def)
		}
		snapshot.Tools = kept
		return nil
	})
}

// ListGrants returns grants owned by the caller.
func (r *Registry) ListGrants(ownerID string) ([]models.ToolGrant, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return nil, err
	}
	out := make([]models.ToolGrant, 0, len(snapshot.Grants))
	for _, grant := range snapshot.Grants {
		if grant.OwnerID == ownerID {
			out = append(out, grant)
		}
	}
	return out, nil
}

// SaveGrant creates or replaces the grant for (owner, profile, tool).
func (r *Registry) SaveGrant(grant models.ToolGrant) (models.ToolGrant, error) {
	if strings.TrimSpace(grant.AgentProfileID) == "" || strings.TrimSpace(grant.ToolID) == "" {
		return models.ToolGrant{}, fmt.Errorf("%w: agent_profile_id and tool_id are required", ErrInvalid)
	}
	if grant.Approval != "" && !validApproval(grant.Approval) {
		return models.ToolGrant{}, fmt.Errorf("%w: invalid approval policy", ErrInvalid)
	}
	err := r.store.Update(func(snapshot *Snapshot) error {
		now := r.now()
		for i := range snapshot.Grants {
			if snapshot.Grants[i].OwnerID == grant.OwnerID &&
				snapshot.Grants[i].AgentProfileID == grant.AgentProfileID &&
				snapshot.Grants[i].ToolID == grant.ToolID {
				grant.ID = snapshot.Grants[i].ID
				grant.CreatedAt = snapshot.Grants[i].CreatedAt
				snapshot.Grants[i] = grant
				return nil
			}
		}
		if grant.ID == "" {
			grant.ID = fmt.Sprintf("grant-%d", now.UnixNano())
		}
		grant.CreatedAt = now
		snapshot.Grants = append(snapshot.Grants, grant)
		return nil
	})
	return grant, err
}

func (r *Registry) DeleteGrant(ownerID, id string) error {
	return r.store.Update(func(snapshot *Snapshot) error {
		for i := range snapshot.Grants {
			if snapshot.Grants[i].ID == id && snapshot.Grants[i].OwnerID == ownerID {
				snapshot.Grants = append(snapshot.Grants[:i], snapshot.Grants[i+1:]...)
				return nil
			}
		}
		return ErrNotFound
	})
}

// FindGrant resolves the grant for a profile/tool pair.
func (r *Registry) FindGrant(ownerID, profileID, toolID string) (*models.ToolGrant, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return nil, err
	}
	for i := range snapshot.Grants {
		grant := snapshot.Grants[i]
		if grant.OwnerID == ownerID && grant.AgentProfileID == profileID && grant.ToolID == toolID {
			return &grant, nil
		}
	}
	return nil, nil
}

// ValidateToolDefinition enforces the registry contract.
func ValidateToolDefinition(def models.ToolDefinition) error {
	if strings.TrimSpace(def.ID) == "" {
		return fmt.Errorf("%w: id is required", ErrInvalid)
	}
	if !strings.Contains(def.ID, ":") {
		return fmt.Errorf("%w: id must be namespaced (native:, http:, mcp:)", ErrInvalid)
	}
	if strings.TrimSpace(def.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if strings.TrimSpace(def.Version) == "" {
		return fmt.Errorf("%w: version is required", ErrInvalid)
	}
	switch def.Transport {
	case models.ToolTransportNative, models.ToolTransportHTTP, models.ToolTransportMCPStdio, models.ToolTransportMCPHTTP:
	default:
		return fmt.Errorf("%w: unsupported transport %q", ErrInvalid, def.Transport)
	}
	if def.Transport == models.ToolTransportNative && strings.TrimSpace(def.Native) == "" {
		return fmt.Errorf("%w: native implementation is required", ErrInvalid)
	}
	if def.Transport == models.ToolTransportHTTP && (def.HTTP == nil || strings.TrimSpace(def.HTTP.URL) == "") {
		return fmt.Errorf("%w: http.url is required", ErrInvalid)
	}
	if def.Approval != "" && !validApproval(def.Approval) {
		return fmt.Errorf("%w: invalid approval policy", ErrInvalid)
	}
	if def.TimeoutSec < 0 || def.TimeoutSec > 3600 {
		return fmt.Errorf("%w: timeout_sec must be between 0 and 3600", ErrInvalid)
	}
	if def.MaxOutputBytes < 0 || def.MaxOutputBytes > 64<<20 {
		return fmt.Errorf("%w: max_output_bytes must be between 0 and 64MiB", ErrInvalid)
	}
	if def.InputSchema != "" && !json.Valid([]byte(def.InputSchema)) {
		return fmt.Errorf("%w: input_schema is not valid JSON", ErrInvalid)
	}
	if def.OutputSchema != "" && !json.Valid([]byte(def.OutputSchema)) {
		return fmt.Errorf("%w: output_schema is not valid JSON", ErrInvalid)
	}
	return nil
}

func validApproval(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case models.ToolApprovalNever, models.ToolApprovalAlways, models.ToolApprovalWrite, models.ToolApprovalNetwork:
		return true
	default:
		return false
	}
}

// DeleteServer removes an MCP server and every tool discovered from it. It is
// used for cleanup; historical ToolCallRecords keep their tool id/version.
func (r *Registry) DeleteServer(ownerID string, isAdmin bool, id string) error {
	return r.store.Update(func(snapshot *Snapshot) error {
		index := -1
		for i := range snapshot.Servers {
			if snapshot.Servers[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			return ErrNotFound
		}
		server := snapshot.Servers[index]
		if server.OwnerID != "" && server.OwnerID != ownerID && !isAdmin {
			return ErrForbidden
		}
		snapshot.Servers = append(snapshot.Servers[:index], snapshot.Servers[index+1:]...)
		kept := make([]models.ToolDefinition, 0, len(snapshot.Tools))
		for _, def := range snapshot.Tools {
			if def.ServerID == id {
				continue
			}
			kept = append(kept, def)
		}
		snapshot.Tools = kept
		return nil
	})
}
