package literature

import (
	"context"
	"strings"
	"sync"
	"time"
)

const (
	arxivHostRate   = 1.0
	defaultHostRate = 10.0
)

type tokenBucket struct {
	tokens float64
	burst  float64
	rate   float64
	last   time.Time
}

func (b *tokenBucket) refill(now time.Time) {
	if b == nil || !now.After(b.last) {
		return
	}
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
}

// hostLimiter is a small per-host token bucket. Different providers run at
// different rates and one slow or throttled provider must not delay the
// others, so buckets are keyed by hostname.
type hostLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	now     func() time.Time
}

func newHostLimiter() *hostLimiter {
	return &hostLimiter{buckets: make(map[string]*tokenBucket), now: time.Now}
}

func hostRate(host string) (rate, burst float64) {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "arxiv.org" || host == "export.arxiv.org" || strings.HasSuffix(host, ".arxiv.org") {
		return arxivHostRate, arxivHostRate
	}
	return defaultHostRate, defaultHostRate
}

func (l *hostLimiter) Wait(ctx context.Context, host string) error {
	if l == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	host = strings.ToLower(strings.TrimSpace(host))
	rate, burst := hostRate(host)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := l.now()
		l.mu.Lock()
		bucket := l.buckets[host]
		if bucket == nil || bucket.rate != rate {
			bucket = &tokenBucket{tokens: burst, burst: burst, rate: rate, last: now}
			l.buckets[host] = bucket
		}
		bucket.refill(now)
		if bucket.tokens >= 1 {
			bucket.tokens--
			l.mu.Unlock()
			return nil
		}
		waitSeconds := (1 - bucket.tokens) / bucket.rate
		l.mu.Unlock()
		if waitSeconds <= 0 {
			waitSeconds = 0.001
		}
		timer := time.NewTimer(time.Duration(waitSeconds * float64(time.Second)))
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
