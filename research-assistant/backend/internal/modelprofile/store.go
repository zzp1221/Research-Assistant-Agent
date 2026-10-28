package modelprofile

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

const (
	storeFileVersion = 1
	profilesFileName = "model_profiles.json"
	routingFileName  = "routing.json"
)

type storeFile struct {
	Version  int        `json:"version"`
	Profiles []*Profile `json:"profiles"`
	SavedAt  time.Time  `json:"saved_at"`
}

type routingFile struct {
	Version  int        `json:"version"`
	Routings []*Routing `json:"routings"`
}

// Store is a file-backed model profile registry with atomic writes. It is safe
// for concurrent API and agent access.
type Store struct {
	mu       sync.RWMutex
	dir      string
	profiles map[string]*Profile
	routings map[string]*Routing
	now      func() time.Time
}

// NewStore opens (or creates) a store rooted at dir. An empty dir keeps the
// store memory-only, which is useful for tests.
func NewStore(dir string) (*Store, error) {
	store := &Store{dir: strings.TrimSpace(dir), now: time.Now, profiles: map[string]*Profile{}, routings: map[string]*Routing{}}
	if store.dir == "" {
		return store, nil
	}
	if err := os.MkdirAll(store.dir, 0o700); err != nil {
		return nil, fmt.Errorf("create model profile dir: %w", err)
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

// Dir returns the backing directory ("" when memory-only).
func (s *Store) Dir() string { return s.dir }

func (s *Store) load() error {
	path := filepath.Join(s.dir, profilesFileName)
	if data, err := os.ReadFile(path); err == nil {
		var file storeFile
		if err := json.Unmarshal(data, &file); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		for _, profile := range file.Profiles {
			if profile == nil || strings.TrimSpace(profile.ID) == "" {
				continue
			}
			s.profiles[profile.ID] = profile
		}
	}
	routingPath := filepath.Join(s.dir, routingFileName)
	if data, err := os.ReadFile(routingPath); err == nil {
		var file routingFile
		if err := json.Unmarshal(data, &file); err != nil {
			return fmt.Errorf("parse %s: %w", routingPath, err)
		}
		for _, routing := range file.Routings {
			if routing == nil {
				continue
			}
			s.routings[routing.OwnerID] = routing
		}
	}
	return nil
}

func (s *Store) persistLocked() error {
	if s.dir == "" {
		return nil
	}
	profiles := make([]*Profile, 0, len(s.profiles))
	for _, profile := range s.profiles {
		profiles = append(profiles, profile)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ID < profiles[j].ID })
	if err := writeJSONAtomic(filepath.Join(s.dir, profilesFileName), storeFile{Version: storeFileVersion, Profiles: profiles, SavedAt: s.now().UTC()}); err != nil {
		return err
	}
	routings := make([]*Routing, 0, len(s.routings))
	for _, routing := range s.routings {
		routings = append(routings, routing)
	}
	sort.Slice(routings, func(i, j int) bool { return routings[i].OwnerID < routings[j].OwnerID })
	return writeJSONAtomic(filepath.Join(s.dir, routingFileName), routingFile{Version: storeFileVersion, Routings: routings})
}

func writeJSONAtomic(path string, value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-"+filepath.Base(path)+"-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// List returns the profiles visible to owner: the owner's own profiles plus all
// global profiles, sorted by owner group then name.
func (s *Store) List(ownerID string) []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Profile, 0, len(s.profiles))
	for _, profile := range s.profiles {
		if profile.OwnerID != "" && profile.OwnerID != ownerID {
			continue
		}
		out = append(out, profile.Masked())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OwnerID != out[j].OwnerID {
			return out[i].OwnerID < out[j].OwnerID
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Get returns a visible profile (owner's own or global).
func (s *Store) Get(ownerID, id string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.profiles[id]
	if !ok {
		return Profile{}, false
	}
	if profile.OwnerID != "" && profile.OwnerID != ownerID {
		return Profile{}, false
	}
	return profile.Masked(), true
}

// GetRaw returns the stored profile without masking (internal use).
func (s *Store) GetRaw(id string) (*Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	profile, ok := s.profiles[id]
	if !ok {
		return nil, false
	}
	copy := *profile
	return &copy, true
}

// Create adds a profile owned by ownerID ("" creates a global profile). The
// plaintext secret is encrypted before it is stored or persisted.
func (s *Store) Create(ownerID string, input Input, maxPerUser int) (Profile, error) {
	now := s.now().UTC()
	profile := &Profile{
		ID:                   uuid.NewString(),
		OwnerID:              ownerID,
		Name:                 strings.TrimSpace(input.Name),
		Provider:             strings.TrimSpace(input.Provider),
		BaseURL:              strings.TrimSpace(input.BaseURL),
		Model:                strings.TrimSpace(input.Model),
		Roles:                normalizeRoles(input.Roles),
		SupportsVision:       normalizeVision(input.SupportsVision),
		TimeoutSeconds:       input.TimeoutSeconds,
		MaxOutputTokens:      input.MaxOutputTokens,
		ContextWindowTokens:  input.ContextWindowTokens,
		PromptPricePer1K:     input.PromptPricePer1K,
		CompletionPricePer1K: input.CompletionPricePer1K,
		Revision:             1,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if profile.Provider == "" {
		profile.Provider = ProviderOpenAICompatible
	}
	if profile.TimeoutSeconds <= 0 {
		profile.TimeoutSeconds = 120
	}
	if err := profile.validate(); err != nil {
		return Profile{}, err
	}
	key := strings.TrimSpace(input.APIKey)
	if key == "" {
		return Profile{}, fmt.Errorf("%w: api_key is required", ErrInvalid)
	}
	ciphertext, err := encryptSecret(key, profile.ID, ownerID, profile.Revision)
	if err != nil {
		return Profile{}, err
	}
	profile.APIKeyCiphertext = ciphertext
	profile.APIKeyLast4 = last4(key)
	profile.apiKey = key

	s.mu.Lock()
	defer s.mu.Unlock()
	if maxPerUser > 0 && ownerID != "" {
		count := 0
		for _, existing := range s.profiles {
			if existing.OwnerID == ownerID {
				count++
			}
		}
		if count >= maxPerUser {
			return Profile{}, ErrQuotaExceeded
		}
	}
	for _, existing := range s.profiles {
		if existing.OwnerID == ownerID && strings.EqualFold(existing.Name, profile.Name) {
			return Profile{}, ErrNameExists
		}
	}
	s.profiles[profile.ID] = profile
	if err := s.persistLocked(); err != nil {
		delete(s.profiles, profile.ID)
		return Profile{}, err
	}
	return profile.Masked(), nil
}

// Update mutates an existing visible profile. Empty APIKey keeps the old
// secret; every successful update bumps Revision.
func (s *Store) Update(ownerID, id string, input Input) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[id]
	if !ok || (profile.OwnerID != "" && profile.OwnerID != ownerID) {
		return Profile{}, ErrNotFound
	}
	next := *profile
	if name := strings.TrimSpace(input.Name); name != "" {
		next.Name = name
	}
	if provider := strings.TrimSpace(input.Provider); provider != "" {
		next.Provider = provider
	}
	if baseURL := strings.TrimSpace(input.BaseURL); baseURL != "" {
		next.BaseURL = baseURL
	}
	if model := strings.TrimSpace(input.Model); model != "" {
		next.Model = model
	}
	if len(input.Roles) > 0 {
		next.Roles = normalizeRoles(input.Roles)
	}
	if input.SupportsVision != "" {
		next.SupportsVision = normalizeVision(input.SupportsVision)
		now := s.now().UTC()
		next.VisionOverriddenAt = &now
	}
	if input.TimeoutSeconds > 0 {
		next.TimeoutSeconds = input.TimeoutSeconds
	}
	if input.MaxOutputTokens > 0 {
		next.MaxOutputTokens = input.MaxOutputTokens
	}
	if input.ContextWindowTokens > 0 {
		next.ContextWindowTokens = input.ContextWindowTokens
	}
	if input.PromptPricePer1K >= 0 {
		next.PromptPricePer1K = input.PromptPricePer1K
	}
	if input.CompletionPricePer1K >= 0 {
		next.CompletionPricePer1K = input.CompletionPricePer1K
	}
	for _, existing := range s.profiles {
		if existing.ID != id && existing.OwnerID == next.OwnerID && strings.EqualFold(existing.Name, next.Name) {
			return Profile{}, ErrNameExists
		}
	}
	// Every modification bumps the revision (design 4.2). Because the AAD binds
	// the revision, the sealed secret is re-encrypted under the new revision.
	previousRevision := next.Revision
	key := strings.TrimSpace(input.APIKey)
	if key == "" && strings.TrimSpace(next.APIKeyCiphertext) != "" {
		// Plaintext is not kept on disk; reload it from the sealed value first.
		if plain, err := decryptSecret(next.APIKeyCiphertext, next.ID, next.OwnerID, previousRevision); err == nil {
			key = plain
		}
	}
	next.Revision = previousRevision + 1
	if key != "" {
		ciphertext, err := encryptSecret(key, next.ID, next.OwnerID, next.Revision)
		if err != nil {
			return Profile{}, err
		}
		next.APIKeyCiphertext = ciphertext
		next.APIKeyLast4 = last4(key)
		next.apiKey = key
	}
	next.UpdatedAt = s.now().UTC()
	s.profiles[id] = &next
	if err := s.persistLocked(); err != nil {
		s.profiles[id] = profile
		return Profile{}, err
	}
	return next.Masked(), nil
}

// Delete removes a profile unless a routing still references it.
func (s *Store) Delete(ownerID, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[id]
	if !ok || (profile.OwnerID != "" && profile.OwnerID != ownerID) {
		return ErrNotFound
	}
	for _, routing := range s.routings {
		if routing.MainProfileID == id {
			return ErrInUse
		}
		for _, visionID := range routing.VisionProfileIDs {
			if visionID == id {
				return ErrInUse
			}
		}
		for _, fallbackID := range routing.FallbackProfileIDs {
			if fallbackID == id {
				return ErrInUse
			}
		}
	}
	delete(s.profiles, id)
	if err := s.persistLocked(); err != nil {
		s.profiles[id] = profile
		return err
	}
	return nil
}

// SetSupportsVision records a probe or runtime observation.
func (s *Store) SetSupportsVision(id, status, reason string, probedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	profile, ok := s.profiles[id]
	if !ok {
		return
	}
	profile.SupportsVision = normalizeVision(status)
	profile.VisionProbeReason = reason
	stamp := probedAt.UTC()
	profile.VisionProbedAt = &stamp
	profile.UpdatedAt = s.now().UTC()
	_ = s.persistLocked()
}

// Secret returns the decrypted API key for one profile.
func (s *Store) Secret(id string) (string, error) {
	s.mu.RLock()
	profile, ok := s.profiles[id]
	if !ok {
		s.mu.RUnlock()
		return "", ErrNotFound
	}
	cached := profile.apiKey
	ciphertext, ownerID, revision := profile.APIKeyCiphertext, profile.OwnerID, profile.Revision
	s.mu.RUnlock()
	if strings.TrimSpace(cached) != "" {
		return cached, nil
	}
	if strings.TrimSpace(ciphertext) == "" {
		return "", nil
	}
	return decryptSecret(ciphertext, id, ownerID, revision)
}

// UpsertBootstrap creates the env-derived global profile when no global main
// exists. It is idempotent and never overwrites user configuration.
func (s *Store) UpsertBootstrap(baseURL, model, apiKey string) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, profile := range s.profiles {
		if profile.OwnerID == "" && !profile.Bootstrap {
			return Profile{}, nil
		}
	}
	for _, profile := range s.profiles {
		if profile.OwnerID == "" && profile.Bootstrap {
			profile.BaseURL = baseURL
			profile.Model = model
			// The env file is the source of truth for the bootstrap profile. If
			// the operator rotates OPENAI_API_KEY, the persisted ciphertext must
			// follow the new key and the revision must move so resolver caches
			// cannot keep using the old client. Compare the full plaintext rather
			// than only the last four characters.
			currentKey := profile.apiKey
			if strings.TrimSpace(currentKey) == "" && strings.TrimSpace(profile.APIKeyCiphertext) != "" {
				if decrypted, err := decryptSecret(profile.APIKeyCiphertext, profile.ID, "", profile.Revision); err == nil {
					currentKey = decrypted
				}
			}
			if trimmed := strings.TrimSpace(apiKey); trimmed != "" && trimmed != currentKey {
				profile.Revision++
				profile.apiKey = trimmed
				profile.APIKeyLast4 = last4(trimmed)
				if EncryptionAvailable() {
					ciphertext, err := encryptSecret(trimmed, profile.ID, "", profile.Revision)
					if err != nil {
						return Profile{}, err
					}
					profile.APIKeyCiphertext = ciphertext
				}
			}
			profile.UpdatedAt = s.now().UTC()
			return Profile{}, s.persistLocked()
		}
	}
	now := s.now().UTC()
	profile := &Profile{
		ID:             uuid.NewString(),
		OwnerID:        "",
		Name:           "环境默认模型",
		Provider:       ProviderOpenAICompatible,
		BaseURL:        strings.TrimSpace(baseURL),
		Model:          strings.TrimSpace(model),
		Roles:          []string{RoleMain, RoleVision},
		SupportsVision: VisionUnknown,
		TimeoutSeconds: 120,
		Revision:       1,
		Bootstrap:      true,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if strings.TrimSpace(apiKey) == "" {
		return Profile{}, fmt.Errorf("%w: bootstrap model has no api key", ErrInvalid)
	}
	profile.apiKey = apiKey
	profile.APIKeyLast4 = last4(apiKey)
	if EncryptionAvailable() {
		ciphertext, err := encryptSecret(apiKey, profile.ID, "", profile.Revision)
		if err != nil {
			return Profile{}, err
		}
		profile.APIKeyCiphertext = ciphertext
	}
	s.profiles[profile.ID] = profile
	if err := s.persistLocked(); err != nil {
		delete(s.profiles, profile.ID)
		return Profile{}, err
	}
	return profile.Masked(), nil
}

// GetRouting returns the routing for owner ("" is the global routing).
func (s *Store) GetRouting(ownerID string) Routing {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if routing, ok := s.routings[ownerID]; ok {
		copy := *routing
		copy.VisionProfileIDs = append([]string(nil), routing.VisionProfileIDs...)
		copy.FallbackProfileIDs = append([]string(nil), routing.FallbackProfileIDs...)
		return copy
	}
	return Routing{OwnerID: ownerID}
}

// SaveRouting validates visibility and reference integrity then persists.
func (s *Store) SaveRouting(ownerID, mainProfileID string, visionIDs, fallbackIDs []string) (Routing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	mainProfileID = strings.TrimSpace(mainProfileID)
	if mainProfileID != "" {
		profile, ok := s.profiles[mainProfileID]
		if !ok || (profile.OwnerID != "" && profile.OwnerID != ownerID) {
			return Routing{}, fmt.Errorf("%w: main profile %s is not visible", ErrInvalid, mainProfileID)
		}
	}
	seen := map[string]bool{}
	cleanVision := make([]string, 0, len(visionIDs))
	for _, id := range visionIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		profile, ok := s.profiles[id]
		if !ok || (profile.OwnerID != "" && profile.OwnerID != ownerID) {
			return Routing{}, fmt.Errorf("%w: vision profile %s is not visible", ErrInvalid, id)
		}
		seen[id] = true
		cleanVision = append(cleanVision, id)
	}
	seenFallback := map[string]bool{}
	cleanFallback := make([]string, 0, len(fallbackIDs))
	for _, id := range fallbackIDs {
		id = strings.TrimSpace(id)
		if id == "" || seenFallback[id] || seen[id] {
			continue
		}
		profile, ok := s.profiles[id]
		if !ok || (profile.OwnerID != "" && profile.OwnerID != ownerID) {
			return Routing{}, fmt.Errorf("%w: fallback profile %s is not visible", ErrInvalid, id)
		}
		seenFallback[id] = true
		cleanFallback = append(cleanFallback, id)
	}
	routing := &Routing{
		OwnerID:            ownerID,
		MainProfileID:      mainProfileID,
		VisionProfileIDs:   cleanVision,
		FallbackProfileIDs: cleanFallback,
		UpdatedAt:          s.now().UTC(),
	}
	s.routings[ownerID] = routing
	if err := s.persistLocked(); err != nil {
		return Routing{}, err
	}
	copy := *routing
	return copy, nil
}
