package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestIdempotencyStatesAndHandles(t *testing.T) {
	c := newIdempotencyCache(time.Hour)
	fp := planRequestFingerprint(RequestPayload{Intent: "hello"})
	first, execute, err := c.Begin("owner", "key", fp)
	if err != nil || !execute {
		t.Fatal("missing execution right", err)
	}
	waiter, execute, err := c.Begin("owner", "key", fp)
	if err != nil || execute || waiter != first {
		t.Fatal("duplicate execution")
	}
	if _, _, err := c.Begin("owner", "key", [32]byte{1}); err != errIdempotencyConflict {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := c.Wait(ctx, waiter); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	failure := idempotencyError(500, "failed", "failed", "")
	if !c.Fail(first, failure) {
		t.Fatal("fail not published")
	}
	retry, execute, err := c.Begin("owner", "key", fp)
	if err != nil || !execute || retry == first {
		t.Fatal("retry not released")
	}
	if c.Complete(first, idempotentResponse{status: 200}) {
		t.Fatal("old handle modified new execution")
	}
	response, state, err := c.Wait(t.Context(), waiter)
	if err != nil || state != "failed" || string(response.body) != string(failure.body) {
		t.Fatal("lost batch failure", state, err)
	}
	c.Complete(retry, idempotentResponse{status: 200, body: []byte("success")})
	result, _, _ := c.Wait(t.Context(), retry)
	result.body[0] = 'X'
	result, _, _ = c.Wait(t.Context(), retry)
	if string(result.body) != "success" {
		t.Fatal("mutable cached response")
	}
}
func TestIdempotencyCapacityAndExpiry(t *testing.T) {
	c := newIdempotencyCache(time.Hour)
	c.max = 3
	fp := [32]byte{}
	running, _, _ := c.Begin("owner", "running", fp)
	unknown, _, _ := c.Begin("owner", "unknown", fp)
	c.MarkCommitUnknown(unknown, "plan-1")
	success, _, _ := c.Begin("owner", "success", fp)
	c.Complete(success, idempotentResponse{status: 200})
	c.mu.Lock()
	running.createdAt = time.Now().Add(-48 * time.Hour)
	unknown.createdAt = running.createdAt
	c.mu.Unlock()
	if _, _, err := c.Begin("owner", "new", fp); err != errIdempotencyCapacity {
		t.Fatal("capacity did not reject", err)
	}
	for _, key := range []string{"running", "unknown", "success"} {
		if _, execute, err := c.Begin("owner", key, fp); err != nil || execute {
			t.Fatal("existing key lost", key)
		}
	}
	c.mu.Lock()
	success.completedAt = time.Now().Add(-2 * time.Hour)
	c.mu.Unlock()
	if _, execute, err := c.Begin("owner", "new", fp); err != nil || !execute {
		t.Fatal("expired success not pruned", err)
	}
	for _, key := range []string{"running", "unknown"} {
		if _, execute, _ := c.Begin("owner", key, fp); execute {
			t.Fatal("protected entry evicted")
		}
	}
	response, state, _ := c.Wait(t.Context(), unknown)
	if state != "commit_unknown" || response.status != http.StatusServiceUnavailable {
		t.Fatal(state, response)
	}
}
func TestIdempotencyWaitTimeoutAndIndependentKeys(t *testing.T) {
	c := newIdempotencyCache(time.Hour)
	c.waitTimeout = time.Millisecond
	first, _, _ := c.Begin("owner", "key", [32]byte{})
	for _, id := range []idempotencyID{{"other", "key"}, {"owner", "other"}, {"a|b", "c"}, {"a", "b|c"}} {
		if _, execute, err := c.Begin(id.owner, id.key, [32]byte{}); err != nil || !execute {
			t.Fatal("independent key blocked", err)
		}
	}
	if _, _, err := c.Wait(t.Context(), first); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if _, execute, _ := c.Begin("owner", "key", [32]byte{}); execute {
		t.Fatal("timeout cleared execution")
	}
}

func TestIdempotencyFailureIsPublishedToWholeBatch(t *testing.T) {
	c := newIdempotencyCache(time.Hour)
	first, _, _ := c.Begin("owner", "batch", [32]byte{})
	handles := make([]*idempotencyEntry, 16)
	for i := range handles {
		handles[i], _, _ = c.Begin("owner", "batch", [32]byte{})
	}
	result := idempotencyError(500, "planner_failed", "same failure", "")
	c.Fail(first, result)
	retry, execute, _ := c.Begin("owner", "batch", [32]byte{})
	if !execute || retry == first {
		t.Fatal("new submission did not acquire fresh handle")
	}
	for _, handle := range handles {
		response, state, err := c.Wait(t.Context(), handle)
		if err != nil || state != "failed" || string(response.body) != string(result.body) {
			t.Fatal("batch retried or lost failure", state, err)
		}
	}
}
