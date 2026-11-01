// Package updater implements signed desktop update checks and downloads.
package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Manifest is the latest.json payload.
type Manifest struct {
	Version         string `json:"version"`
	URL             string `json:"url"`
	SHA256          string `json:"sha256"`
	Signature       string `json:"signature"`
	ReleaseNotesURL string `json:"release_notes_url,omitempty"`
}

// PublicKeyFromEnv reads the base64 Ed25519 public key from
// RA_UPDATE_PUBLIC_KEY.
func PublicKeyFromEnv() (ed25519.PublicKey, error) {
	raw := strings.TrimSpace(os.Getenv("RA_UPDATE_PUBLIC_KEY"))
	if raw == "" {
		return nil, fmt.Errorf("RA_UPDATE_PUBLIC_KEY is empty")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode RA_UPDATE_PUBLIC_KEY: %w", err)
	}
	if len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("RA_UPDATE_PUBLIC_KEY must be %d bytes, got %d", ed25519.PublicKeySize, len(decoded))
	}
	return ed25519.PublicKey(decoded), nil
}

// Checker fetches and validates the update manifest.
type Checker struct {
	Endpoint       string
	CurrentVersion string
	Client         *http.Client
	MaxBytes       int64
}

// Check returns the manifest when a newer version is available.
func (c *Checker) Check(ctx context.Context) (*Manifest, bool, error) {
	if strings.TrimSpace(c.Endpoint) == "" {
		return nil, false, fmt.Errorf("updater: endpoint is required")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.Endpoint, nil)
	if err != nil {
		return nil, false, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("updater: manifest returned %s", response.Status)
	}
	limit := c.MaxBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(body)) > limit {
		return nil, false, fmt.Errorf("updater: manifest exceeds %d bytes", limit)
	}
	var manifest Manifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, false, fmt.Errorf("updater: decode manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, false, err
	}
	if CompareVersions(manifest.Version, c.CurrentVersion) <= 0 {
		return &manifest, false, nil
	}
	return &manifest, true, nil
}

// Validate checks manifest fields that must be well-formed before use.
func (m *Manifest) Validate() error {
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("updater: manifest version is empty")
	}
	if len(m.SHA256) != 64 {
		return fmt.Errorf("updater: manifest sha256 must be 64 hex characters")
	}
	if _, err := hex.DecodeString(m.SHA256); err != nil {
		return fmt.Errorf("updater: invalid sha256: %w", err)
	}
	parsed, err := url.Parse(m.URL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return fmt.Errorf("updater: invalid download url %q", m.URL)
	}
	if strings.TrimSpace(m.Signature) == "" {
		return fmt.Errorf("updater: manifest signature is empty")
	}
	return nil
}

// CompareVersions compares semantic versions, returning -1, 0 or 1. A leading
// "v" and any pre-release/build suffix are ignored for the stable channel.
func CompareVersions(a, b string) int {
	parse := func(value string) []int {
		value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
		if index := strings.IndexAny(value, "-+"); index >= 0 {
			value = value[:index]
		}
		parts := strings.Split(value, ".")
		out := make([]int, 3)
		for i := 0; i < 3 && i < len(parts); i++ {
			number, err := strconv.Atoi(parts[i])
			if err != nil {
				number = 0
			}
			out[i] = number
		}
		return out
	}
	left, right := parse(a), parse(b)
	for i := 0; i < 3; i++ {
		if left[i] < right[i] {
			return -1
		}
		if left[i] > right[i] {
			return 1
		}
	}
	return 0
}

// Installer downloads and verifies signed updates.
type Installer struct {
	PublicKey ed25519.PublicKey
	Client    *http.Client
	MaxBytes  int64
	Timeout   time.Duration
}

// DownloadAndVerify downloads manifest.URL, verifies its size, sha256 and
// ed25519 signature, then atomically stores it in destDir. The signature is
// computed over the lower-case hex sha256 digest of the file.
func (i *Installer) DownloadAndVerify(ctx context.Context, manifest *Manifest, destDir string) (string, error) {
	if len(i.PublicKey) != ed25519.PublicKeySize {
		return "", fmt.Errorf("updater: public key is not configured")
	}
	if err := manifest.Validate(); err != nil {
		return "", err
	}
	client := i.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	if i.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, i.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifest.URL, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("updater: download returned %s", response.Status)
	}
	limit := i.MaxBytes
	if limit <= 0 {
		limit = 300 << 20
	}
	if response.ContentLength > limit {
		return "", fmt.Errorf("updater: download exceeds %d bytes", limit)
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", err
	}
	temp, err := os.CreateTemp(destDir, "update-*.part")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	hasher := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hasher), io.LimitReader(response.Body, limit+1))
	if err != nil {
		return "", err
	}
	if written > limit {
		return "", fmt.Errorf("updater: download exceeds %d bytes", limit)
	}
	if err := temp.Sync(); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", err
	}
	digest := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(digest, manifest.SHA256) {
		return "", fmt.Errorf("updater: sha256 mismatch: got %s want %s", digest, manifest.SHA256)
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil {
		return "", fmt.Errorf("updater: decode signature: %w", err)
	}
	if !ed25519.Verify(i.PublicKey, []byte(strings.ToLower(digest)), signature) {
		return "", fmt.Errorf("updater: signature verification failed")
	}
	name := "ResearchAssistant-" + sanitizeVersion(manifest.Version) + ".exe"
	finalPath := filepath.Join(destDir, name)
	if err := os.Rename(tempPath, finalPath); err != nil {
		return "", err
	}
	return finalPath, nil
}

// UpdateStatus is the user-facing update state.
type UpdateStatus struct {
	Available       bool   `json:"available"`
	Version         string `json:"version,omitempty"`
	ReleaseNotesURL string `json:"release_notes_url,omitempty"`
	Path            string `json:"path,omitempty"`
}

// Launcher starts a downloaded installer. Tests inject a fake.
type Launcher func(path string) error

// Manager ties the manifest checker and signed installer together.
type Manager struct {
	Checker     *Checker
	Installer   *Installer
	DownloadDir string
	Launcher    Launcher
}

// Check reports whether a newer signed build is available.
func (m *Manager) Check(ctx context.Context) (UpdateStatus, error) {
	if m == nil || m.Checker == nil {
		return UpdateStatus{}, fmt.Errorf("updater: manager is not configured")
	}
	manifest, available, err := m.Checker.Check(ctx)
	if err != nil {
		return UpdateStatus{}, err
	}
	status := UpdateStatus{Available: available}
	if manifest != nil {
		status.Version = manifest.Version
		status.ReleaseNotesURL = manifest.ReleaseNotesURL
	}
	return status, nil
}

// Download fetches and verifies the update for version (or the latest).
func (m *Manager) Download(ctx context.Context, version string) (UpdateStatus, error) {
	if m == nil || m.Checker == nil || m.Installer == nil {
		return UpdateStatus{}, fmt.Errorf("updater: manager is not configured")
	}
	manifest, available, err := m.Checker.Check(ctx)
	if err != nil {
		return UpdateStatus{}, err
	}
	if !available || manifest == nil {
		return UpdateStatus{}, fmt.Errorf("updater: no update is available")
	}
	if requested := strings.TrimSpace(version); requested != "" && requested != manifest.Version {
		return UpdateStatus{}, fmt.Errorf("updater: requested version %s does not match latest %s", requested, manifest.Version)
	}
	downloadDir := strings.TrimSpace(m.DownloadDir)
	if downloadDir == "" {
		downloadDir = filepath.Join(os.TempDir(), "ra-update")
	}
	path, err := m.Installer.DownloadAndVerify(ctx, manifest, downloadDir)
	if err != nil {
		return UpdateStatus{}, err
	}
	return UpdateStatus{Available: true, Version: manifest.Version, ReleaseNotesURL: manifest.ReleaseNotesURL, Path: path}, nil
}

// Install launches a previously verified installer. The default launcher is
// Windows-only and uses the NSIS silent flag.
func (m *Manager) Install(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("updater: installer path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("updater: installer not found: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("updater: installer path is a directory")
	}
	launcher := m.Launcher
	if launcher == nil {
		if runtime.GOOS != "windows" {
			return fmt.Errorf("updater: installer launch is only supported on windows")
		}
		launcher = func(installer string) error {
			return exec.Command(installer, "/S").Start()
		}
	}
	return launcher(path)
}

func sanitizeVersion(version string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "..", "_")
	return replacer.Replace(strings.TrimSpace(version))
}
