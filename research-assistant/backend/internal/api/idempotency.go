package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
	"sync"
	"time"
)

type idempotentResponse struct {
	status      int
	body        []byte
	contentType string
	retryAfter  string
}
type idempotencyID struct{ owner, key string }
type idempotencyEntry struct {
	id                     idempotencyID
	fingerprint            [32]byte
	state                  string
	createdAt, completedAt time.Time
	done                   chan struct{}
	response               idempotentResponse
	planID                 string
}
type idempotencyCache struct {
	mu          sync.Mutex
	entries     map[idempotencyID]*idempotencyEntry
	ttl         time.Duration
	max         int
	waitTimeout time.Duration
}

var errIdempotencyConflict = errors.New("idempotency_key_conflict")
var errIdempotencyCapacity = errors.New("idempotency_capacity_exceeded")

func newIdempotencyCache(ttl time.Duration) *idempotencyCache {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &idempotencyCache{entries: make(map[idempotencyID]*idempotencyEntry), ttl: ttl, max: 4096, waitTimeout: 30 * time.Second}
}

// Only normalize values also normalized before execution. An omitted session
// stays omitted here, regardless of the current session cookie.
func planRequestFingerprint(payload RequestPayload) [32]byte {
	encoded, _ := json.Marshal(struct {
		Version int            `json:"version"`
		Request RequestPayload `json:"request"`
	}{1, payload})
	return sha256.Sum256(encoded)
}

// Begin atomically claims a key. Slow work and waiting never hold the map lock.
func (c *idempotencyCache) Begin(owner, key string, fingerprint [32]byte) (*idempotencyEntry, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for id, entry := range c.entries {
		if entry.state == "succeeded" && now.Sub(entry.completedAt) >= c.ttl {
			delete(c.entries, id)
		}
	}
	id := idempotencyID{owner, key}
	if entry := c.entries[id]; entry != nil {
		if entry.fingerprint != fingerprint {
			return nil, false, errIdempotencyConflict
		}
		return entry, false, nil
	}
	if len(c.entries) >= c.max {
		return nil, false, errIdempotencyCapacity
	}
	entry := &idempotencyEntry{id: id, fingerprint: fingerprint, state: "in_progress", createdAt: now, done: make(chan struct{})}
	c.entries[id] = entry
	return entry, true, nil
}
func (c *idempotencyCache) Wait(ctx context.Context, entry *idempotencyEntry) (idempotentResponse, string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.waitTimeout)
	defer cancel()
	select {
	case <-entry.done:
		// Closing done publishes an immutable response, even after Fail removes it.
		response := entry.response
		response.body = bytes.Clone(response.body)
		return response, entry.state, nil
	case <-ctx.Done():
		return idempotentResponse{}, "", ctx.Err()
	}
}
func (c *idempotencyCache) finish(entry *idempotencyEntry, state, planID string, response idempotentResponse) bool {
	if entry == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries[entry.id] != entry || entry.state != "in_progress" {
		return false
	}
	response.body = bytes.Clone(response.body)
	entry.state, entry.planID, entry.response, entry.completedAt = state, planID, response, time.Now()
	if state == "failed" {
		delete(c.entries, entry.id)
	}
	close(entry.done)
	return true
}
func (c *idempotencyCache) Complete(entry *idempotencyEntry, response idempotentResponse) bool {
	return c.finish(entry, "succeeded", "", response)
}
func (c *idempotencyCache) Fail(entry *idempotencyEntry, response idempotentResponse) bool {
	return c.finish(entry, "failed", "", response)
}
func (c *idempotencyCache) MarkCommitUnknown(entry *idempotencyEntry, planID string) idempotentResponse {
	response := idempotencyError(http.StatusServiceUnavailable, "idempotency_commit_unknown", "Plan save outcome is unknown; inspect the original plan before retrying", planID)
	c.finish(entry, "commit_unknown", planID, response)
	return response
}
func idempotencyError(status int, code, message, planID string) idempotentResponse {
	body := gin.H{"code": code, "error": message}
	if planID != "" {
		body["plan_id"] = planID
	}
	encoded, _ := json.Marshal(body)
	return idempotentResponse{status: status, body: encoded, contentType: "application/json; charset=utf-8"}
}
func writeIdempotentResponse(c *gin.Context, response idempotentResponse, replay bool) {
	if replay {
		c.Header("Idempotency-Replayed", "true")
	}
	if response.retryAfter != "" {
		c.Header("Retry-After", response.retryAfter)
	}
	c.Data(response.status, response.contentType, response.body)
}

// Capture the executor's error response for all already-joined waiters.
type planResponseCapture struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func (w *planResponseCapture) Write(body []byte) (int, error) {
	w.body.Write(body)
	return w.ResponseWriter.Write(body)
}
func (w *planResponseCapture) WriteString(body string) (int, error) { return w.Write([]byte(body)) }
func logIdempotency(outcome, owner string, entry *idempotencyEntry) {
	log.Printf("idempotency outcome=%s owner=%s age=%s", outcome, owner, time.Since(entry.createdAt))
}
