package memory

import (
	"context"
	"sync"
	"time"

	"telesrv/internal/store"
)

// RateLimiter is the in-process replacement for redisstore.RateLimiter: a
// fixed-window counter per key. Redis enforced the increment-then-check
// atomically with a Lua script because multiple Redis clients could race on
// the same key; in one process a mutex gives the same guarantee for free.
type RateLimiter struct {
	mu      sync.Mutex
	windows map[string]rateWindow
}

type rateWindow struct {
	count   int
	resetAt time.Time
}

var _ store.RateLimiter = (*RateLimiter)(nil)

// NewRateLimiter builds an empty rate limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{windows: make(map[string]rateWindow)}
}

func (l *RateLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, error) {
	return l.AllowN(ctx, key, 1, limit, window)
}

// AllowN mirrors redisstore.RateLimiter.AllowN exactly: cost<=0 or limit<=0
// always allows (no-op limiter), a non-positive window floors to 1s, and a
// fresh or expired window starts a new count from zero. retryAfterSeconds is
// the remaining window time rounded up, floored at 1, matching the PTTL
// rounding the Redis version did to avoid handing a caller a zero-second
// FLOOD_WAIT.
func (l *RateLimiter) AllowN(_ context.Context, key string, cost, limit int, window time.Duration) (bool, int, error) {
	if cost <= 0 || limit <= 0 {
		return true, 0, nil
	}
	if window <= 0 {
		window = time.Second
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.windows[key]
	if !ok || !w.resetAt.After(now) {
		w = rateWindow{count: 0, resetAt: now.Add(window)}
	}
	w.count += cost
	l.windows[key] = w
	if w.count <= limit {
		return true, 0, nil
	}
	retry := int((w.resetAt.Sub(now) + time.Second - time.Nanosecond) / time.Second)
	if retry <= 0 {
		retry = 1
	}
	return false, retry, nil
}

// sweep drops windows that have already expired -- a key nobody hits again
// after being rate-limited once must not sit in memory forever.
func (l *RateLimiter) sweep(now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	removed := 0
	for key, w := range l.windows {
		if !w.resetAt.After(now) {
			delete(l.windows, key)
			removed++
		}
	}
	return removed
}
