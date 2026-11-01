package settings

import "errors"

// ErrSecretStoreUnsupported is returned when DPAPI is unavailable.
var ErrSecretStoreUnsupported = errors.New("secret store is only supported on windows")

// SecretStore persists encrypted secrets.
type SecretStore interface {
	Save(key string, value []byte) error
	Load(key string) ([]byte, error)
	Delete(key string) error
}

// MemorySecretStore is a test/in-memory implementation.
type MemorySecretStore struct {
	values map[string][]byte
}

func NewMemorySecretStore() *MemorySecretStore {
	return &MemorySecretStore{values: map[string][]byte{}}
}

func (s *MemorySecretStore) Save(key string, value []byte) error {
	s.values[key] = append([]byte(nil), value...)
	return nil
}

func (s *MemorySecretStore) Load(key string) ([]byte, error) {
	value, ok := s.values[key]
	if !ok {
		return nil, ErrSecretStoreUnsupported
	}
	return append([]byte(nil), value...), nil
}

func (s *MemorySecretStore) Delete(key string) error {
	delete(s.values, key)
	return nil
}
