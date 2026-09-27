package memory

import (
	"sync"
	"time"
)

// ttlCache is the shared building block behind every in-process replacement
// for a Redis-backed rebuildable cache (active_channel_ids_page.go,
// dialog_list_snapshot.go, user_cache.go, bot_callback.go,
// inline_registry.go). Redis gave those caches TTL-based eviction for free;
// this reproduces it with a mutex-guarded map plus a background sweep, so an
// entry nobody ever reads again still gets reclaimed instead of accumulating
// for the life of the process.
type ttlCache[K comparable, V any] struct {
	mu       sync.Mutex
	defaultT time.Duration
	items    map[K]ttlEntry[V]
}

type ttlEntry[V any] struct {
	value   V
	expires time.Time
}

// newTTLCache builds an empty cache. defaultTTL is used by put when the
// caller passes ttl<=0 (mirrors the Redis constructors' "0 means use the
// package default" convention).
func newTTLCache[K comparable, V any](defaultTTL time.Duration) *ttlCache[K, V] {
	return &ttlCache[K, V]{defaultT: defaultTTL, items: make(map[K]ttlEntry[V])}
}

// get returns the value and true if key is present and not yet expired.
// Reading through an expired entry evicts it -- get is also how a client that
// never runs the sweep loop (e.g. a short-lived test) still can't observe a
// value past its expiry.
func (c *ttlCache[K, V]) get(key K) (V, bool) {
	v, ok, _ := c.getWithTTL(key)
	return v, ok
}

// getWithTTL also reports the remaining time to live, for the one caller
// (GetInlineCache) whose interface contract requires it. Floors at 1s like
// the Redis version did, to absorb the same PTTL-granularity race rather
// than reporting a zero/negative remaining TTL to a caller about to act on
// "still cached."
func (c *ttlCache[K, V]) getWithTTL(key K) (V, bool, time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		var zero V
		return zero, false, 0
	}
	remaining := time.Until(entry.expires)
	if remaining <= 0 {
		delete(c.items, key)
		var zero V
		return zero, false, 0
	}
	if remaining < time.Second {
		remaining = time.Second
	}
	return entry.value, true, remaining
}

// put stores value under key with the given ttl, or the cache's default when
// ttl<=0.
func (c *ttlCache[K, V]) put(key K, value V, ttl time.Duration) {
	if ttl <= 0 {
		ttl = c.defaultT
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = ttlEntry[V]{value: value, expires: time.Now().Add(ttl)}
}

// remainingTTL reports how long key has left, for callers (PutInlineWebDocumentBytes)
// that need to preserve an existing entry's expiry rather than resetting it.
func (c *ttlCache[K, V]) remainingTTL(key K) (time.Duration, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok {
		return 0, false
	}
	remaining := time.Until(entry.expires)
	if remaining <= 0 {
		delete(c.items, key)
		return 0, false
	}
	return remaining, true
}

func (c *ttlCache[K, V]) delete(key K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, key)
}

func (c *ttlCache[K, V]) deleteMany(keys []K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range keys {
		delete(c.items, key)
	}
}

// sweep removes every expired entry and returns the number removed. It
// satisfies the unexported sweepable interface in sweeper.go, so every
// ttlCache instantiation -- whatever its K/V -- can be registered with one
// shared background Sweeper instead of each running its own goroutine.
func (c *ttlCache[K, V]) sweep(now time.Time) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for key, entry := range c.items {
		if !entry.expires.After(now) {
			delete(c.items, key)
			removed++
		}
	}
	return removed
}
