package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// Root returns the configured upload root.
func Root() string {
	if configured := strings.TrimSpace(os.Getenv("UPLOAD_ROOT")); configured != "" {
		return filepath.Clean(configured)
	}
	return filepath.Join(os.TempDir(), "research-assistant-uploads")
}

// Directory returns the owner-scoped directory for one upload ID.
func Directory(ownerID, uploadID string) string {
	ownerHash := sha256.Sum256([]byte(ownerID))
	return filepath.Join(Root(), hex.EncodeToString(ownerHash[:16]), uploadID)
}

// MetadataPath returns metadata.json for an upload.
func MetadataPath(ownerID, uploadID string) string {
	return filepath.Join(Directory(ownerID, uploadID), "metadata.json")
}
