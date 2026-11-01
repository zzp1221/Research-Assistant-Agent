//go:build !windows

package settings

// NewSecretStore returns an unsupported store outside Windows.
func NewSecretStore(string) SecretStore {
	return &unsupportedSecretStore{}
}

type unsupportedSecretStore struct{}

func (s *unsupportedSecretStore) Save(string, []byte) error   { return ErrSecretStoreUnsupported }
func (s *unsupportedSecretStore) Load(string) ([]byte, error) { return nil, ErrSecretStoreUnsupported }
func (s *unsupportedSecretStore) Delete(string) error         { return ErrSecretStoreUnsupported }
