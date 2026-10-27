package api

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// tokenBucket is a per-principal token bucket used to bound expensive API
// calls (plan creation) without a third-party dependency.
type tokenBucket struct {
	tokens float64
	last   time.Time
}

type rateLimiter struct {
	mu           sync.Mutex
	buckets      map[string]*tokenBucket
	tokensPerSec float64
	burst        float64
	idleTTL      time.Duration
}

func newRateLimiter(perMinute, burst int) *rateLimiter {
	if perMinute <= 0 {
		perMinute = 10
	}
	if burst <= 0 {
		burst = 3
	}
	return &rateLimiter{
		buckets:      map[string]*tokenBucket{},
		tokensPerSec: float64(perMinute) / 60.0,
		burst:        float64(burst),
		idleTTL:      30 * time.Minute,
	}
}

func planRateLimitFromEnv() *rateLimiter {
	return newRateLimiter(envIntDefault("PLAN_RATE_LIMIT_PER_MINUTE", 10), envIntDefault("PLAN_RATE_LIMIT_BURST", 3))
}

// Allow consumes one token for key. When denied it returns the duration until
// the next token becomes available, for the Retry-After header.
func (l *rateLimiter) Allow(key string) (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.buckets == nil {
		l.buckets = map[string]*tokenBucket{}
	}
	for candidate, bucket := range l.buckets {
		if l.idleTTL > 0 && now.Sub(bucket.last) > l.idleTTL {
			delete(l.buckets, candidate)
		}
	}
	bucket := l.buckets[key]
	if bucket == nil {
		bucket = &tokenBucket{tokens: l.burst, last: now}
		l.buckets[key] = bucket
	}
	elapsed := now.Sub(bucket.last).Seconds()
	if elapsed > 0 {
		bucket.tokens += elapsed * l.tokensPerSec
		if bucket.tokens > l.burst {
			bucket.tokens = l.burst
		}
	}
	bucket.last = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	missing := 1 - bucket.tokens
	wait := time.Duration(missing / l.tokensPerSec * float64(time.Second))
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

func envIntDefault(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}
