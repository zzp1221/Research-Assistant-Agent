//go:build windows

package settings

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

var (
	procCryptProtectData   = syscall.NewLazyDLL("crypt32.dll").NewProc("CryptProtectData")
	procCryptUnprotectData = syscall.NewLazyDLL("crypt32.dll").NewProc("CryptUnprotectData")
)

// DPAPISecretStore stores base64 DPAPI blobs in secrets.dat.
type DPAPISecretStore struct {
	path string
}

func NewSecretStore(dataDir string) SecretStore {
	return &DPAPISecretStore{path: filepath.Join(dataDir, "secrets.dat")}
}

func (s *DPAPISecretStore) Save(key string, value []byte) error {
	encrypted, err := protectData(value)
	if err != nil {
		return err
	}
	values, _ := s.readAll()
	values[key] = base64.StdEncoding.EncodeToString(encrypted)
	return s.writeAll(values)
}

func (s *DPAPISecretStore) Load(key string) ([]byte, error) {
	values, err := s.readAll()
	if err != nil {
		return nil, err
	}
	encoded, ok := values[key]
	if !ok {
		return nil, os.ErrNotExist
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	return unprotectData(raw)
}

func (s *DPAPISecretStore) Delete(key string) error {
	values, _ := s.readAll()
	delete(values, key)
	return s.writeAll(values)
}

func (s *DPAPISecretStore) readAll() (map[string]string, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	values := map[string]string{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("decode secrets: %w", err)
	}
	return values, nil
}

func (s *DPAPISecretStore) writeAll(values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o600)
}

func protectData(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("cannot protect empty secret")
	}
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	result, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)),
	)
	if result == 0 {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.pbData)))
	return copyBlob(out), nil
}

func unprotectData(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("cannot unprotect empty secret")
	}
	in := dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
	var out dataBlob
	result, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0, uintptr(unsafe.Pointer(&out)),
	)
	if result == 0 {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.pbData)))
	return copyBlob(out), nil
}

func copyBlob(blob dataBlob) []byte {
	if blob.cbData == 0 || blob.pbData == nil {
		return nil
	}
	return append([]byte(nil), unsafe.Slice(blob.pbData, blob.cbData)...)
}
