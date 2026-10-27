package literature

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxMemoryCacheEntries = 512

// cacheStore is the small subset of a cache the HTTP layer needs. Cache
// failures are deliberately silent: a missing or corrupt cache entry only
// means another provider request.
type cacheStore interface {
	Get(ctx context.Context, key string) ([]byte, bool)
	Set(ctx context.Context, key string, data []byte)
}

type cacheEnvelope struct {
	URL       string    `json:"url"`
	FetchedAt time.Time `json:"fetched_at"`
	Body      []byte    `json:"body"`
}

func cacheKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}

func newCache(dir string, ttl time.Duration) cacheStore {
	dir = strings.TrimSpace(dir)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			return &fileCache{dir: dir, ttl: ttl}
		}
	}
	return newMemoryCache(ttl)
}

func cacheExpired(fetchedAt, now time.Time, ttl time.Duration) bool {
	if fetchedAt.IsZero() {
		return true
	}
	if ttl <= 0 {
		return false
	}
	return now.Sub(fetchedAt) > ttl
}

type fileCache struct {
	dir string
	ttl time.Duration
}

func (c *fileCache) path(key string) string {
	return filepath.Join(c.dir, key+".json")
}

func (c *fileCache) Get(ctx context.Context, key string) ([]byte, bool) {
	if c == nil || c.dir == "" || key == "" {
		return nil, false
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, false
	}
	raw, err := os.ReadFile(c.path(key))
	if err != nil {
		return nil, false
	}
	var entry cacheEnvelope
	if err := json.Unmarshal(raw, &entry); err != nil {
		_ = os.Remove(c.path(key))
		return nil, false
	}
	if cacheExpired(entry.FetchedAt, time.Now(), c.ttl) || len(entry.Body) == 0 {
		_ = os.Remove(c.path(key))
		return nil, false
	}
	return entry.Body, true
}

func (c *fileCache) Set(ctx context.Context, key string, data []byte) {
	if c == nil || c.dir == "" || key == "" || len(data) == 0 {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	entry := cacheEnvelope{URL: key, FetchedAt: time.Now().UTC(), Body: data}
	raw, err := json.Marshal(entry)
	if err != nil {
		return
	}
	temp, err := os.CreateTemp(c.dir, ".cache-*.tmp")
	if err != nil {
		return
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if _, err := temp.Write(raw); err != nil {
		_ = temp.Close()
		return
	}
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return
	}
	if err := temp.Close(); err != nil {
		return
	}
	_ = os.Rename(tempName, c.path(key))
}

type memoryCacheEntry struct {
	fetchedAt time.Time
	body      []byte
}

type memoryCache struct {
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]memoryCacheEntry
}

func newMemoryCache(ttl time.Duration) *memoryCache {
	return &memoryCache{ttl: ttl, entries: make(map[string]memoryCacheEntry)}
}

func (c *memoryCache) Get(ctx context.Context, key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	if ctx != nil && ctx.Err() != nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if cacheExpired(entry.fetchedAt, time.Now(), c.ttl) {
		delete(c.entries, key)
		return nil, false
	}
	return append([]byte(nil), entry.body...), true
}

func (c *memoryCache) Set(ctx context.Context, key string, data []byte) {
	if c == nil || key == "" || len(data) == 0 {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if len(c.entries) >= maxMemoryCacheEntries {
		for existingKey, entry := range c.entries {
			if cacheExpired(entry.fetchedAt, now, c.ttl) {
				delete(c.entries, existingKey)
			}
		}
	}
	if len(c.entries) >= maxMemoryCacheEntries {
		for existingKey := range c.entries {
			delete(c.entries, existingKey)
			break
		}
	}
	c.entries[key] = memoryCacheEntry{fetchedAt: now, body: append([]byte(nil), data...)}
}
