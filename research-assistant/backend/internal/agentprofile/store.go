package agentprofile

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
	ErrNotFound = errors.New("agent profile not found")
	ErrForbidden = errors.New("agent profile forbidden")
	ErrConflict = errors.New("agent profile conflict")
	ErrInvalid  = errors.New("agent profile invalid")
)

// Snapshot is the persisted state of profiles and prompt versions.
type Snapshot struct {
	Profiles []models.AgentProfile  `json:"profiles"`
	Prompts  []models.PromptVersion `json:"prompts"`
}

type Store interface {
	Read() (Snapshot, error)
	Update(mutator func(*Snapshot) error) error
}

type MemoryStore struct {
	mu       sync.Mutex
	snapshot Snapshot
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{snapshot: Snapshot{Profiles: []models.AgentProfile{}, Prompts: []models.PromptVersion{}}}
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

type FileStore struct {
	mu       sync.Mutex
	path     string
	snapshot Snapshot
	loaded   bool
}

func NewFileStore(path string) (*FileStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("agent profile store path is empty")
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
			s.snapshot = Snapshot{Profiles: []models.AgentProfile{}, Prompts: []models.PromptVersion{}}
			s.loaded = true
			return nil
		}
		return err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return fmt.Errorf("decode agent profile store %s: %w", s.path, err)
	}
	if snapshot.Profiles == nil {
		snapshot.Profiles = []models.AgentProfile{}
	}
	if snapshot.Prompts == nil {
		snapshot.Prompts = []models.PromptVersion{}
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
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".agents-*.json")
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
	return Snapshot{
		Profiles: append([]models.AgentProfile(nil), source.Profiles...),
		Prompts:  append([]models.PromptVersion(nil), source.Prompts...),
	}
}

// Registry is the in-process facade over the agent profile store.
type Registry struct {
	store Store
	now   func() time.Time
}

func NewRegistry(store Store) *Registry {
	return &Registry{store: store, now: time.Now}
}

func (r *Registry) SetClock(now func() time.Time) { r.now = now }

// EnsureBuiltins seeds one profile per built-in agent so the UI and planner
// have a single source of truth. Builtin profiles are global and read-only.
func (r *Registry) EnsureBuiltins() error {
	return r.store.Update(func(snapshot *Snapshot) error {
		now := r.now()
		for _, def := range BuiltinProfiles() {
			found := false
			for i := range snapshot.Profiles {
				if snapshot.Profiles[i].ID == def.ID {
					snapshot.Profiles[i].Builtin = true
					snapshot.Profiles[i].Enabled = true
					found = true
					break
				}
			}
			if !found {
				def.CreatedAt = now
				def.UpdatedAt = now
				snapshot.Profiles = append(snapshot.Profiles, def)
			}
		}
		return nil
	})
}

func (r *Registry) ListProfiles(ownerID string) ([]models.AgentProfile, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return nil, err
	}
	out := make([]models.AgentProfile, 0, len(snapshot.Profiles))
	for _, profile := range snapshot.Profiles {
		if profile.OwnerID != "" && profile.OwnerID != ownerID {
			continue
		}
		out = append(out, profile)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Builtin != out[j].Builtin {
			return out[i].Builtin
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

func (r *Registry) GetProfile(id string) (models.AgentProfile, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return models.AgentProfile{}, err
	}
	for _, profile := range snapshot.Profiles {
		if profile.ID == id {
			return profile, nil
		}
	}
	return models.AgentProfile{}, ErrNotFound
}

func (r *Registry) GetProfileForOwner(ownerID, id string) (models.AgentProfile, error) {
	profile, err := r.GetProfile(id)
	if err != nil {
		return profile, err
	}
	if profile.OwnerID != "" && profile.OwnerID != ownerID {
		return models.AgentProfile{}, ErrForbidden
	}
	return profile, nil
}

func (r *Registry) SaveProfile(profile models.AgentProfile) (models.AgentProfile, error) {
	if strings.TrimSpace(profile.Name) == "" {
		return models.AgentProfile{}, fmt.Errorf("%w: name is required", ErrInvalid)
	}
	if profile.Builtin {
		return models.AgentProfile{}, fmt.Errorf("%w: builtin profiles are code-owned", ErrForbidden)
	}
	var saved models.AgentProfile
	err := r.store.Update(func(snapshot *Snapshot) error {
		for _, existing := range snapshot.Profiles {
			if existing.ID == profile.ID {
				return fmt.Errorf("%w: profile %s already exists", ErrConflict, profile.ID)
			}
		}
		now := r.now()
		profile.CreatedAt = now
		profile.UpdatedAt = now
		snapshot.Profiles = append(snapshot.Profiles, profile)
		saved = profile
		return nil
	})
	return saved, err
}

func (r *Registry) UpdateProfile(ownerID string, isAdmin bool, id string, patch func(*models.AgentProfile) error) (models.AgentProfile, error) {
	var saved models.AgentProfile
	err := r.store.Update(func(snapshot *Snapshot) error {
		index := -1
		for i := range snapshot.Profiles {
			if snapshot.Profiles[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			return ErrNotFound
		}
		current := snapshot.Profiles[index]
		if current.Builtin && !isAdmin {
			return ErrForbidden
		}
		if current.OwnerID != "" && current.OwnerID != ownerID {
			return ErrForbidden
		}
		if err := patch(&current); err != nil {
			return err
		}
		current.UpdatedAt = r.now()
		snapshot.Profiles[index] = current
		saved = current
		return nil
	})
	return saved, err
}

func (r *Registry) DeleteProfile(ownerID string, isAdmin bool, id string) error {
	_, err := r.UpdateProfile(ownerID, isAdmin, id, func(profile *models.AgentProfile) error {
		profile.Enabled = false
		return nil
	})
	return err
}

// ListPrompts returns prompt versions visible to the owner, newest first.
func (r *Registry) ListPrompts(ownerID, name string) ([]models.PromptVersion, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return nil, err
	}
	out := make([]models.PromptVersion, 0, len(snapshot.Prompts))
	for _, prompt := range snapshot.Prompts {
		if prompt.OwnerID != "" && prompt.OwnerID != ownerID {
			continue
		}
		if name != "" && prompt.Name != name {
			continue
		}
		out = append(out, prompt)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Version > out[j].Version
	})
	return out, nil
}

func (r *Registry) GetPrompt(id string) (models.PromptVersion, error) {
	snapshot, err := r.store.Read()
	if err != nil {
		return models.PromptVersion{}, err
	}
	for _, prompt := range snapshot.Prompts {
		if prompt.ID == id {
			return prompt, nil
		}
	}
	return models.PromptVersion{}, ErrNotFound
}

func (r *Registry) GetPromptForOwner(ownerID, id string) (models.PromptVersion, error) {
	prompt, err := r.GetPrompt(id)
	if err != nil {
		return prompt, err
	}
	if prompt.OwnerID != "" && prompt.OwnerID != ownerID {
		return models.PromptVersion{}, ErrForbidden
	}
	return prompt, nil
}

// CreatePrompt appends a new immutable version for the named prompt.
func (r *Registry) CreatePrompt(ownerID, name, template, createdBy string) (models.PromptVersion, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return models.PromptVersion{}, fmt.Errorf("%w: prompt name is required", ErrInvalid)
	}
	if strings.TrimSpace(template) == "" {
		return models.PromptVersion{}, fmt.Errorf("%w: template is required", ErrInvalid)
	}
	var created models.PromptVersion
	err := r.store.Update(func(snapshot *Snapshot) error {
		maxVersion := 0
		for _, prompt := range snapshot.Prompts {
			if prompt.OwnerID == ownerID && prompt.Name == name && prompt.Version > maxVersion {
				maxVersion = prompt.Version
			}
		}
		now := r.now()
		created = models.PromptVersion{
			ID:        "prompt-" + uuid.NewString(),
			OwnerID:   ownerID,
			Name:      name,
			Version:   maxVersion + 1,
			Template:  template,
			Variables: ExtractVariables(template),
			Status:    models.PromptStatusDraft,
			CreatedBy: createdBy,
			CreatedAt: now,
		}
		snapshot.Prompts = append(snapshot.Prompts, created)
		return nil
	})
	return created, err
}

// ActivatePrompt moves the active pointer for a prompt name. History is never
// rewritten: the previous active version is archived.
func (r *Registry) ActivatePrompt(ownerID, id string) (models.PromptVersion, error) {
	var activated models.PromptVersion
	err := r.store.Update(func(snapshot *Snapshot) error {
		index := -1
		for i := range snapshot.Prompts {
			if snapshot.Prompts[i].ID == id {
				index = i
				break
			}
		}
		if index < 0 {
			return ErrNotFound
		}
		target := snapshot.Prompts[index]
		if target.OwnerID != ownerID {
			return ErrForbidden
		}
		for i := range snapshot.Prompts {
			other := &snapshot.Prompts[i]
			// Exactly one active version per (owner, name). Activating a version
			// archives every sibling, including drafts, so the active pointer is
			// never ambiguous.
			if other.OwnerID == target.OwnerID && other.Name == target.Name && other.ID != target.ID {
				other.Status = models.PromptStatusArchived
			}
		}
		snapshot.Prompts[index].Status = models.PromptStatusActive
		activated = snapshot.Prompts[index]
		return nil
	})
	return activated, err
}

func (r *Registry) UpdatePromptEval(id string, score float64, detail string) error {
	return r.store.Update(func(snapshot *Snapshot) error {
		for i := range snapshot.Prompts {
			if snapshot.Prompts[i].ID == id {
				snapshot.Prompts[i].EvalScore = score
				snapshot.Prompts[i].EvalDetail = detail
				return nil
			}
		}
		return ErrNotFound
	})
}

// BuiltinProfiles mirrors the fixed runners so the platform has one profile
// model. These records are display/compatibility only; the fixed runners stay
// authoritative for built-in agent names.
func BuiltinProfiles() []models.AgentProfile {
	now := time.Now().UTC()
	names := []struct {
		id    string
		name  string
		tools []string
	}{
		{"builtin:librarian_agent", "Librarian", []string{"paper.search", "paper.read", "repository.discover"}},
		{"builtin:data_agent", "Data", []string{"artifact.read", "metrics.analyze", "report.write"}},
		{"builtin:coder_agent", "Coder", []string{"repository.read", "workspace.write", "code.generate"}},
		{"builtin:sandbox_agent", "Sandbox", []string{"sandbox.create", "sandbox.command", "sandbox.python", "sandbox.delete"}},
		{"builtin:research_coding_agent", "Research Coding", []string{"repository.read", "workspace.write_scoped", "sandbox.command", "metrics.validate"}},
		{"builtin:benchmark_agent", "Benchmark", []string{"dataset.audit", "dataset.split_scoped", "metrics.contract"}},
		{"builtin:answer_agent", "Answer", []string{"conversation.respond"}},
		{"builtin:general_agent", "General", []string{"conversation.respond"}},
	}
	out := make([]models.AgentProfile, 0, len(names))
	for _, entry := range names {
		out = append(out, models.AgentProfile{
			ID:            entry.id,
			Name:          entry.name,
			Description:   "Built-in agent profile (fixed runner).",
			ToolAllowlist: entry.tools,
			Builtin:       true,
			Enabled:       true,
			CreatedAt:     now,
			UpdatedAt:     now,
		})
	}
	return out
}
