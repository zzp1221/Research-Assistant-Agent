// Package blobstore implements a small content-addressed store for large
// artifacts (images, reports). Files are written once per SHA-256 and referenced
// from PlanGraph artifacts by "blob:<sha256>", which removes the multi-megabyte
// duplication of base64 images across final_plot/plot_image/report artifacts.
package blobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const refPrefix = "blob:"

// Store is a filesystem-backed content-addressed blob store.
type Store struct {
	root string
}

func New(root string) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("blob store root is empty")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create blob store root: %w", err)
	}
	return &Store{root: root}, nil
}

// Put stores data and returns its content reference plus the byte size. Writing
// the same bytes twice is a no-op, which is what deduplicates repeated images.
func (s *Store) Put(data []byte) (string, int64, error) {
	if s == nil {
		return "", 0, fmt.Errorf("blob store is nil")
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	dir := filepath.Join(s.root, hash[:2])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 0, fmt.Errorf("create blob shard: %w", err)
	}
	path := filepath.Join(dir, hash)
	if _, err := os.Stat(path); err == nil {
		return refPrefix + hash, int64(len(data)), nil
	}
	temp, err := os.CreateTemp(dir, ".blob-*.tmp")
	if err != nil {
		return "", 0, fmt.Errorf("create blob temp file: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return "", 0, fmt.Errorf("write blob: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return "", 0, fmt.Errorf("sync blob: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", 0, fmt.Errorf("close blob: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return "", 0, fmt.Errorf("publish blob: %w", err)
	}
	return refPrefix + hash, int64(len(data)), nil
}

// Get loads the bytes behind a content reference.
func (s *Store) Get(ref string) ([]byte, error) {
	path, err := s.Path(ref)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Path resolves a content reference to a filesystem path after validating it.
func (s *Store) Path(ref string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("blob store is nil")
	}
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, refPrefix) {
		return "", fmt.Errorf("invalid blob reference %q", ref)
	}
	hash := strings.TrimPrefix(ref, refPrefix)
	if len(hash) != 64 {
		return "", fmt.Errorf("invalid blob reference %q", ref)
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", fmt.Errorf("invalid blob reference %q", ref)
	}
	return filepath.Join(s.root, hash[:2], hash), nil
}

// Exists reports whether the referenced blob is present.
func (s *Store) Exists(ref string) bool {
	path, err := s.Path(ref)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
