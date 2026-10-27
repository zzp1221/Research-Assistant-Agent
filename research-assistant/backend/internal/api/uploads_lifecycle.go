package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	uploadspkg "research-assistant-backend/internal/uploads"

	"github.com/gin-gonic/gin"
)

// uploadIndexDirectory stores one small marker file per SHA-256 so a repeated
// upload resolves to the original document without re-reading or re-parsing it.
func uploadIndexDirectory(ownerID string) string {
	ownerDir := filepath.Dir(uploadspkg.Directory(ownerID, "placeholder"))
	return filepath.Join(ownerDir, "index")
}

func findUploadByHash(ownerID, sha256Hex string) (uploadMetadata, bool) {
	sha256Hex = strings.TrimSpace(sha256Hex)
	if sha256Hex == "" {
		return uploadMetadata{}, false
	}
	raw, err := os.ReadFile(filepath.Join(uploadIndexDirectory(ownerID), sha256Hex))
	if err != nil {
		return uploadMetadata{}, false
	}
	uploadID := strings.TrimSpace(string(raw))
	if uploadID == "" {
		return uploadMetadata{}, false
	}
	metadata, err := loadOwnedUpload(ownerID, uploadID)
	if err != nil {
		return uploadMetadata{}, false
	}
	if metadata.SHA256 != sha256Hex {
		return uploadMetadata{}, false
	}
	return metadata, true
}

func writeUploadIndex(ownerID, sha256Hex, uploadID string) error {
	sha256Hex = strings.TrimSpace(sha256Hex)
	if sha256Hex == "" {
		return nil
	}
	directory := uploadIndexDirectory(ownerID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, sha256Hex), []byte(uploadID), 0o600)
}

func removeUploadIndexEntries(ownerID, uploadID string) {
	entries, err := os.ReadDir(uploadIndexDirectory(ownerID))
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(uploadIndexDirectory(ownerID), entry.Name())
		if raw, readErr := os.ReadFile(path); readErr == nil && strings.TrimSpace(string(raw)) == uploadID {
			_ = os.Remove(path)
		}
	}
}

// ownerUploadUsage returns the number of stored uploads and their total bytes.
func ownerUploadUsage(ownerID string) (int, int64, error) {
	root := filepath.Dir(uploadspkg.Directory(ownerID, "placeholder"))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	count := 0
	var bytes int64
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "index" {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(root, entry.Name(), "metadata.json"))
		if readErr != nil {
			continue
		}
		var metadata uploadMetadata
		if json.Unmarshal(raw, &metadata) != nil {
			continue
		}
		count++
		bytes += metadata.Size
	}
	return count, bytes, nil
}

func uploadQuota() (int, int64) {
	maxFiles := envIntDefault("UPLOAD_MAX_FILES", 100)
	maxMB := envIntDefault("UPLOAD_USER_QUOTA_MB", 512)
	return maxFiles, int64(maxMB) * 1024 * 1024
}

// ensureUploadQuota rejects an upload that would exceed the per-user quota.
func ensureUploadQuota(ownerID string, extraBytes int64) error {
	maxFiles, maxBytes := uploadQuota()
	count, used, err := ownerUploadUsage(ownerID)
	if err != nil {
		return fmt.Errorf("cannot inspect upload quota: %w", err)
	}
	if maxFiles > 0 && count >= maxFiles {
		return fmt.Errorf("upload quota exceeded: %d/%d files", count, maxFiles)
	}
	if maxBytes > 0 && used+extraBytes > maxBytes {
		return fmt.Errorf("upload quota exceeded: %d/%d bytes", used+extraBytes, maxBytes)
	}
	return nil
}

// deleteUpload removes an owned upload and any index entry that points to it.
func deleteUpload(c *gin.Context) {
	ownerID := resolveUserID(c)
	uploadID := c.Param("id")
	metadata, err := loadOwnedUpload(ownerID, uploadID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "upload not found", "code": "upload_not_found"})
		return
	}
	if err := os.RemoveAll(uploadDirectory(ownerID, uploadID)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "cannot delete upload", "code": "delete_failed"})
		return
	}
	removeUploadIndexEntries(ownerID, uploadID)
	recordAuditEvent("upload_deleted", ownerID, uploadID, map[string]any{"name": metadata.Name, "size": metadata.Size})
	c.JSON(http.StatusOK, gin.H{"deleted": true, "upload_id": uploadID})
}

// startUploadJanitor removes uploads that exceeded UPLOAD_TTL_DAYS. It runs
// daily and is disabled when the variable is unset or zero.
func startUploadJanitor(ctx context.Context) {
	raw := strings.TrimSpace(os.Getenv("UPLOAD_TTL_DAYS"))
	if raw == "" || raw == "0" {
		return
	}
	ttlDays := envIntDefault("UPLOAD_TTL_DAYS", 0)
	if ttlDays <= 0 {
		return
	}
	ttl := time.Duration(ttlDays) * 24 * time.Hour
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				removed := cleanupExpiredUploads(ttl)
				if removed > 0 {
					fmt.Printf("[uploads] janitor removed %d expired uploads\n", removed)
				}
			}
		}
	}()
}

func cleanupExpiredUploads(ttl time.Duration) int {
	rootEntries, err := os.ReadDir(uploadspkg.Root())
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-ttl)
	removed := 0
	for _, ownerEntry := range rootEntries {
		if !ownerEntry.IsDir() {
			continue
		}
		ownerDir := filepath.Join(uploadspkg.Root(), ownerEntry.Name())
		entries, readErr := os.ReadDir(ownerDir)
		if readErr != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == "index" {
				continue
			}
			metadataPath := filepath.Join(ownerDir, entry.Name(), "metadata.json")
			info, statErr := os.Stat(metadataPath)
			if statErr != nil || info.ModTime().After(cutoff) {
				continue
			}
			if os.RemoveAll(filepath.Join(ownerDir, entry.Name())) == nil {
				removed++
			}
		}
	}
	return removed
}
