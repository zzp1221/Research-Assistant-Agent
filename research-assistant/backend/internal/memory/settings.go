package memory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SettingsStore persists per-owner memory settings in one JSON file.
type SettingsStore struct {
	mu   sync.RWMutex
	path string
	data map[string]Settings
}

func NewSettingsStore(path string) (*SettingsStore, error) {
	store := &SettingsStore{path: path, data: map[string]Settings{}}
	if strings.TrimSpace(path) == "" {
		return store, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create memory settings directory: %w", err)
	}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return store, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read memory settings: %w", err)
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &store.data); err != nil {
			return nil, fmt.Errorf("decode memory settings: %w", err)
		}
	}
	return store, nil
}

func (s *SettingsStore) Get(ownerID string) Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if value, ok := s.data[ownerID]; ok {
		return value
	}
	return DefaultSettings()
}

func (s *SettingsStore) Put(ownerID string, settings Settings) error {
	if strings.TrimSpace(ownerID) == "" {
		return fmt.Errorf("owner_id is required")
	}
	if settings.Mode == "" {
		settings.Mode = "on"
	}
	if settings.Mode != "on" && settings.Mode != "off" && settings.Mode != "shadow" {
		return fmt.Errorf("invalid memory mode")
	}
	if settings.RetrievalTopK < 0 || settings.RetrievalTopK > 100 || settings.CoreBudgetTokens < 0 || settings.CoreBudgetTokens > 16000 || settings.RetrievedBudgetTokens < 0 || settings.RetrievedBudgetTokens > 16000 || settings.RetentionDays < 0 || settings.RetentionDays > 36500 {
		return fmt.Errorf("memory settings out of range")
	}
	if settings.RetrievalTopK <= 0 {
		settings.RetrievalTopK = 8
	}
	if settings.CoreBudgetTokens <= 0 {
		settings.CoreBudgetTokens = 512
	}
	if settings.RetrievedBudgetTokens <= 0 {
		settings.RetrievedBudgetTokens = 2000
	}
	if settings.RetentionDays <= 0 {
		settings.RetentionDays = 180
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous, existed := s.data[ownerID]
	s.data[ownerID] = settings
	committed := false
	defer func() {
		if !committed {
			if existed {
				s.data[ownerID] = previous
			} else {
				delete(s.data, ownerID)
			}
		}
	}()
	if strings.TrimSpace(s.path) == "" {
		committed = true
		return nil
	}
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return fmt.Errorf("encode memory settings: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := writeFileSynced(tmp, raw); err != nil {
		return fmt.Errorf("write memory settings: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	committed = true
	return nil
}
