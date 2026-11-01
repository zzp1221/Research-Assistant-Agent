package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"research-assistant-backend/internal/apppaths"
)

// Root returns the configured upload root.
func Root() string {
	if configured := strings.TrimSpace(os.Getenv("UPLOAD_ROOT")); configured != "" {
		return filepath.Clean(configured)
	}
	if strings.TrimSpace(os.Getenv("APP_DATA_DIR")) != "" {
		return filepath.Join(apppaths.DataDir(), "uploads")
	}
	return filepath.Join(os.TempDir(), "research-assistant-uploads")
}

// Directory returns the owner-scoped directory for one upload ID.
func Directory(ownerID, uploadID string) string {
	ownerHash := sha256.Sum256([]byte(ownerID))
	return filepath.Join(Root(), hex.EncodeToString(ownerHash[:16]), uploadID)
}
