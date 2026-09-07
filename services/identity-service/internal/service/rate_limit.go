package service

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"
)

// remoteKey is the context key that carries the caller's IP.
type remoteKey struct{}

// WithRemoteIP stores the caller IP for rate limiting.
func WithRemoteIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, remoteKey{}, ip)
}

func remoteIP(ctx context.Context) string {
	if v, ok := ctx.Value(remoteKey{}).(string); ok && v != "" {
		return v
	}
	return "unknown"
}

// ClientIP extracts a clean IP from an http.Request remote address.
func ClientIP(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return strings.TrimSpace(host)
	}
	return strings.TrimSpace(remoteAddr)
}

// attemptBucket tracks failures for a key (email+IP). After MaxAttempts
// failures within Window, further attempts are rejected until Lockout passes.
type attemptBucket struct {
	failures    int
	firstFail   time.Time
	lockedUntil time.Time
}

// RateLimiter is an in-process throttle. It is intentionally small and
// dependency-free; a production deployment would shard this state (Redis) but
// the single-writer demo instance keeps the guarantee process-local.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*attemptBucket
	max     int
	window  time.Duration
	lockout time.Duration
}

func NewRateLimiter(max int, window, lockout time.Duration) *RateLimiter {
	return &RateLimiter{
		buckets: map[string]*attemptBucket{},
		max:     max,
		window:  window,
		lockout: lockout,
	}
}

// Allow reports whether an attempt for key may proceed, recording it as a
// failure when recordFailure is true.
func (l *RateLimiter) Allow(key string, recordFailure bool) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		b = &attemptBucket{firstFail: now}
		l.buckets[key] = b
	}

	// Expired window → reset.
	if now.Sub(b.firstFail) > l.window {
		b.failures = 0
		b.firstFail = now
		b.lockedUntil = time.Time{}
	}

	// Hard lockout in force.
	if now.Before(b.lockedUntil) {
		return false
	}

	if recordFailure {
		b.failures++
		if b.failures >= l.max {
			b.lockedUntil = now.Add(l.lockout)
		}
	}
	return true
}

// Reset clears a key (used on successful authentication).
func (l *RateLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, key)
}
