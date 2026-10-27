package api

import (
	"sync"
	"time"
)

type idempotentResponse struct {
	status      int
	body        []byte
	contentType string
	createdAt   time.Time
}

type idempotencyCache struct {
	mu      sync.Mutex
	entries map[string]idempotentResponse
	ttl     time.Duration
	max     int
}

func newIdempotencyCache(ttl time.Duration) *idempotencyCache {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &idempotencyCache{entries: map[string]idempotentResponse{}, ttl: ttl, max: 4096}
}

func idempotencyKey(principal, key string) string {
	return principal + "|" + key
}

// Lookup returns a cached response for the principal/key pair.
func (c *idempotencyCache) Lookup(principal, key string) (idempotentResponse, bool) {
	if c == nil || key == "" {
		return idempotentResponse{}, false
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	id := idempotencyKey(principal, key)
	entry, ok := c.entries[id]
	if !ok {
		return idempotentResponse{}, false
	}
	if now.Sub(entry.createdAt) > c.ttl {
		delete(c.entries, id)
		return idempotentResponse{}, false
	}
	return entry, true
}

// Store caches a successful response. Only 2xx responses are cached: caching a
// validation error would make a corrected retry return the old failure.
func (c *idempotencyCache) Store(principal, key string, status int, body []byte, contentType string) {
	if c == nil || key == "" || status < 200 || status >= 300 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		for id, entry := range c.entries {
			if now.Sub(entry.createdAt) > c.ttl {
				delete(c.entries, id)
			}
		}
		if len(c.entries) >= c.max {
			c.entries = map[string]idempotentResponse{}
		}
	}
	c.entries[idempotencyKey(principal, key)] = idempotentResponse{
		status:      status,
		body:        append([]byte(nil), body...),
		contentType: contentType,
		createdAt:   now,
	}
}
