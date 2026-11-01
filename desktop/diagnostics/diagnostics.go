// Package diagnostics builds a redacted support bundle for the desktop app.
package diagnostics

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"research-assistant-desktop/settings"
)

// Options configures one diagnostics bundle.
type Options struct {
	AppVersion   string
	DataDir      string
	LogDir       string
	Settings     settings.Settings
	BackendURL   string
	SandboxURL   string
	BackendToken string
	SandboxToken string
	HTTPClient   *http.Client
	Now          func() time.Time
	MaxLogBytes  int64
	CrashDir     string
}

// Build writes diagnostics-<timestamp>.zip under DataDir/diagnostics and
// returns its absolute path.
func Build(ctx context.Context, opts Options) (string, error) {
	if strings.TrimSpace(opts.DataDir) == "" {
		return "", fmt.Errorf("diagnostics: data dir is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	maxLog := opts.MaxLogBytes
	if maxLog <= 0 {
		maxLog = 5 << 20
	}
	outDir := filepath.Join(opts.DataDir, "diagnostics")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return "", fmt.Errorf("diagnostics: create output dir: %w", err)
	}
	stamp := now().Format("20060102-150405")
	outPath := filepath.Join(outDir, "diagnostics-"+stamp+".zip")
	tmpPath := outPath + ".part"
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("diagnostics: create bundle: %w", err)
	}
	zw := zip.NewWriter(file)

	writeJSON := func(name string, value any) error {
		writer, err := zw.Create(name)
		if err != nil {
			return err
		}
		encoder := json.NewEncoder(writer)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}

	manifest := map[string]any{
		"app_version":  opts.AppVersion,
		"os":           runtime.GOOS,
		"arch":         runtime.GOARCH,
		"go_version":   runtime.Version(),
		"generated_at": now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON("manifest.json", manifest); err != nil {
		_ = zw.Close()
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	redacted := RedactSettings(opts.Settings)
	if err := writeJSON("settings.redacted.json", redacted); err != nil {
		_ = zw.Close()
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	health := map[string]any{}
	if opts.BackendURL != "" {
		health["backend"] = fetchJSON(ctx, client, strings.TrimRight(opts.BackendURL, "/")+"/api/health", opts.BackendToken)
	}
	if opts.SandboxURL != "" {
		health["sandbox"] = fetchJSON(ctx, client, strings.TrimRight(opts.SandboxURL, "/")+"/api/v1/health", opts.SandboxToken)
	}
	if err := writeJSON("health.json", health); err != nil {
		_ = zw.Close()
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	system := map[string]any{
		"num_cpu":            runtime.NumCPU(),
		"go_version":         runtime.Version(),
		"os":                 runtime.GOOS,
		"arch":               runtime.GOARCH,
		"hostname":           hostname(),
		"memory_alloc_bytes": memoryAllocBytes(),
	}
	if err := writeJSON("system.json", system); err != nil {
		_ = zw.Close()
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	failedPlans := []map[string]any{}
	if opts.BackendURL != "" {
		if body := fetchJSON(ctx, client, strings.TrimRight(opts.BackendURL, "/")+"/api/plans?status=failed&limit=3", opts.BackendToken); body != nil {
			if raw, ok := body.(map[string]any); ok {
				if plans, ok := raw["plans"].([]any); ok {
					for _, item := range plans {
						if plan, ok := item.(map[string]any); ok {
							failedPlans = append(failedPlans, map[string]any{
								"id":           plan["id"],
								"title":        plan["title"],
								"status":       plan["status"],
								"outcome":      plan["outcome"],
								"failed_nodes": plan["failed_nodes"],
								"updated_at":   plan["updated_at"],
							})
						}
					}
				}
			}
		}
	}
	if err := writeJSON("failed-plans.json", map[string]any{"plans": failedPlans}); err != nil {
		_ = zw.Close()
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := writeCrashEntries(zw, opts.CrashDir); err != nil {
		_ = zw.Close()
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	for _, name := range []string{"shell.log", "backend.log", "sandbox.log"} {
		data, err := Tail(filepath.Join(opts.LogDir, name), maxLog)
		if err != nil || len(data) == 0 {
			continue
		}
		writer, err := zw.Create("logs/" + name)
		if err != nil {
			_ = zw.Close()
			_ = file.Close()
			_ = os.Remove(tmpPath)
			return "", err
		}
		if _, err := writer.Write(data); err != nil {
			_ = zw.Close()
			_ = file.Close()
			_ = os.Remove(tmpPath)
			return "", err
		}
	}
	if err := zw.Close(); err != nil {
		_ = file.Close()
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, outPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	return outPath, nil
}

// Tail returns at most maxBytes from the end of path. A missing file is not an
// error: diagnostics must still be produced when one service never started.
func Tail(path string, maxBytes int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	start := int64(0)
	if maxBytes > 0 && size > maxBytes {
		start = size - maxBytes
	}
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(file, size-start))
}

// RedactSettings returns a copy of settings with secrets masked.
func RedactSettings(cfg settings.Settings) map[string]any {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{"error": err.Error()}
	}
	return redactValue(value).(map[string]any)
}

func redactValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			if isSensitiveKey(key) {
				out[key] = "***"
				continue
			}
			out[key] = redactValue(item)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = redactValue(item)
		}
		return out
	default:
		return value
	}
}

func isSensitiveKey(key string) bool {
	normalized := strings.ToLower(key)
	if strings.HasSuffix(normalized, "_ref") {
		return false
	}
	for _, marker := range []string{"api_key", "apikey", "token", "secret", "password", "authorization", "cookie"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func fetchJSON(ctx context.Context, client *http.Client, url, token string) any {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return map[string]any{"status": response.StatusCode, "body": string(body)}
	}
	if response.StatusCode >= 300 {
		return map[string]any{"status": response.StatusCode, "body": decoded}
	}
	return decoded
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

func memoryAllocBytes() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Alloc
}

// crashEntry describes one Windows Error Reporting dump.
type crashEntry struct {
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

// collectCrashFiles returns the newest maxFiles .dmp files in dir.
func collectCrashFiles(dir string, maxFiles int) ([]crashEntry, []string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	type crashFile struct {
		name     string
		size     int64
		modified time.Time
	}
	files := make([]crashFile, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".dmp") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, crashFile{name: entry.Name(), size: info.Size(), modified: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modified.After(files[j].modified) })
	if maxFiles > 0 && len(files) > maxFiles {
		files = files[:maxFiles]
	}
	metadata := make([]crashEntry, 0, len(files))
	names := make([]string, 0, len(files))
	for _, file := range files {
		metadata = append(metadata, crashEntry{Name: file.name, Size: file.size, Modified: file.modified.UTC().Format(time.RFC3339)})
		names = append(names, file.name)
	}
	return metadata, names, nil
}

func writeCrashEntries(zw *zip.Writer, dir string) error {
	metadata, names, err := collectCrashFiles(dir, 3)
	if err != nil {
		return err
	}
	writer, err := zw.Create("crashes.json")
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(map[string]any{"dumps": metadata}); err != nil {
		return err
	}
	for _, name := range names {
		data, err := Tail(filepath.Join(dir, name), 1<<20)
		if err != nil || len(data) == 0 {
			continue
		}
		writer, err := zw.Create("crashes/" + name)
		if err != nil {
			return err
		}
		if _, err := writer.Write(data); err != nil {
			return err
		}
	}
	return nil
}
