// Package uploadstore persists programmatically imported files with the same
// owner/hash layout as interactive uploads, so the UI and agents share one
// deduplicated upload store.
package uploadstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/documents"
	uploadspkg "research-assistant-backend/internal/uploads"

	"github.com/google/uuid"
)

// Metadata mirrors the metadata.json written by the upload API.
type Metadata struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	ContentType string    `json:"content_type"`
	Size        int64     `json:"size"`
	SHA256      string    `json:"sha256"`
	OwnerID     string    `json:"owner_id"`
	SessionID   string    `json:"session_id"`
	StoredPath  string    `json:"stored_path"`
	ScanStatus  string    `json:"scan_status,omitempty"`
	ScanDetail  string    `json:"scan_detail,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Result is the outcome of one programmatic import.
type Result struct {
	Metadata     Metadata
	Document     *documents.Document
	Deduplicated bool
}

func indexDirectory(ownerID string) string {
	ownerDir := filepath.Dir(uploadspkg.Directory(ownerID, "placeholder"))
	return filepath.Join(ownerDir, "index")
}

// FindByHash returns an existing upload for the owner/hash pair.
func FindByHash(ownerID, sha256Hex string) (Metadata, bool) {
	sha256Hex = strings.TrimSpace(sha256Hex)
	if sha256Hex == "" {
		return Metadata{}, false
	}
	raw, err := os.ReadFile(filepath.Join(indexDirectory(ownerID), sha256Hex))
	if err != nil {
		return Metadata{}, false
	}
	uploadID := strings.TrimSpace(string(raw))
	if uploadID == "" {
		return Metadata{}, false
	}
	metadata, err := Load(ownerID, uploadID)
	if err != nil || metadata.SHA256 != sha256Hex {
		return Metadata{}, false
	}
	return metadata, true
}

// Load reads one upload's metadata.
func Load(ownerID, uploadID string) (Metadata, error) {
	raw, err := os.ReadFile(filepath.Join(uploadspkg.Directory(ownerID, uploadID), "metadata.json"))
	if err != nil {
		return Metadata{}, err
	}
	var metadata Metadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return Metadata{}, err
	}
	if metadata.OwnerID != ownerID {
		return Metadata{}, fmt.Errorf("upload is not owned by this user")
	}
	return metadata, nil
}

func writeIndex(ownerID, sha256Hex, uploadID string) error {
	directory := indexDirectory(ownerID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, sha256Hex), []byte(uploadID), 0o600)
}

func ensureQuota(ownerID string, extraBytes int64) error {
	root := filepath.Dir(uploadspkg.Directory(ownerID, "placeholder"))
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	count := 0
	var used int64
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "index" {
			continue
		}
		raw, readErr := os.ReadFile(filepath.Join(root, entry.Name(), "metadata.json"))
		if readErr != nil {
			continue
		}
		var metadata Metadata
		if json.Unmarshal(raw, &metadata) != nil {
			continue
		}
		count++
		used += metadata.Size
	}
	maxFiles := envInt("UPLOAD_MAX_FILES", 100)
	maxBytes := int64(envInt("UPLOAD_USER_QUOTA_MB", 512)) * 1024 * 1024
	if maxFiles > 0 && count >= maxFiles {
		return fmt.Errorf("upload quota exceeded: %d/%d files", count, maxFiles)
	}
	if maxBytes > 0 && used+extraBytes > maxBytes {
		return fmt.Errorf("upload quota exceeded: %d/%d bytes", used+extraBytes, maxBytes)
	}
	return nil
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
		return parsed
	}
	return fallback
}

// SavePDF stores a PDF (deduplicating by SHA-256), extracts its document and
// returns the upload metadata. Extraction failures are persisted on the
// document but do not fail the import.
func SavePDF(ctx context.Context, ownerID, sessionID, name string, data []byte) (*Result, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("imported PDF is empty")
	}
	if !strings.HasPrefix(string(data), "%PDF") {
		return nil, fmt.Errorf("imported content is not a PDF")
	}
	name = sanitizeName(name)
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if existing, ok := FindByHash(ownerID, sha); ok {
		document, _ := documents.Load(filepath.Dir(existing.StoredPath))
		return &Result{Metadata: existing, Document: document, Deduplicated: true}, nil
	}
	if err := ensureQuota(ownerID, int64(len(data))); err != nil {
		return nil, err
	}
	uploadID := uuid.NewString()
	directory := uploadspkg.Directory(ownerID, uploadID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	storedPath := filepath.Join(directory, "content.pdf")
	if err := os.WriteFile(storedPath, data, 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	metadata := Metadata{
		ID: uploadID, Name: name, ContentType: "application/pdf",
		Size: int64(len(data)), SHA256: sha, OwnerID: ownerID, SessionID: sessionID,
		StoredPath: storedPath, ScanStatus: "skipped",
		ScanDetail: "programmatic literature import", CreatedAt: time.Now().UTC(),
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(directory, "metadata.json"), raw, 0o600); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	if err := writeIndex(ownerID, sha, uploadID); err != nil {
		// A missing index only costs dedupe, not correctness.
		_ = err
	}
	document := documents.ExtractPDF(ctx, uploadID, storedPath)
	if err := documents.Save(directory, document); err != nil {
		_ = os.RemoveAll(directory)
		return nil, err
	}
	return &Result{Metadata: metadata, Document: document}, nil
}

func sanitizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "imported-paper"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", "\x00", "")
	name = replacer.Replace(name)
	if len(name) > 180 {
		name = name[:180]
	}
	return name
}
