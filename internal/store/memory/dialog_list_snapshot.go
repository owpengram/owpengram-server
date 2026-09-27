package memory

import (
	"context"
	"time"

	"telesrv/internal/store"
)

// DefaultDialogListSnapshotTTL mirrors redisstore's default.
const DefaultDialogListSnapshotTTL = time.Hour

// DialogListSnapshotCache is the in-process replacement for
// redisstore.DialogListSnapshotCache. Redis stored this zstd-compressed,
// version-tagged and length-capped, because it had to survive serialization
// and a shared-process budget; in-process the value is held as a plain Go
// struct copy, so none of that machinery is needed. PostgreSQL read-model
// hashes remain the authority, exactly as the Redis version's doc comment
// says -- this cache still never decides whether an entry is current.
//
// Get returns the cached value's Dialogs/Messages/Users slices by reference,
// not a deep copy -- unlike the Redis version, which always decoded a fresh
// copy from bytes. Every current caller only reads these slices (to derive
// ids, or to build a differently-typed snapshot), never mutates an element
// in place; a future caller that needs to mutate what it gets back must
// copy first.
type DialogListSnapshotCache struct {
	c *ttlCache[store.DialogListSnapshotCacheKey, store.DialogListSnapshotCacheValue]
}

var _ store.DialogListSnapshotCache = (*DialogListSnapshotCache)(nil)

// NewDialogListSnapshotCache builds the cache. ttl<=0 uses
// DefaultDialogListSnapshotTTL.
func NewDialogListSnapshotCache(ttl time.Duration) *DialogListSnapshotCache {
	if ttl <= 0 {
		ttl = DefaultDialogListSnapshotTTL
	}
	return &DialogListSnapshotCache{c: newTTLCache[store.DialogListSnapshotCacheKey, store.DialogListSnapshotCacheValue](ttl)}
}

func (c *DialogListSnapshotCache) GetDialogListSnapshot(_ context.Context, key store.DialogListSnapshotCacheKey) (store.DialogListSnapshotCacheValue, bool, error) {
	v, ok := c.c.get(key)
	if !ok {
		return store.DialogListSnapshotCacheValue{}, false, nil
	}
	return v, true, nil
}

func (c *DialogListSnapshotCache) PutDialogListSnapshot(_ context.Context, key store.DialogListSnapshotCacheKey, value store.DialogListSnapshotCacheValue) error {
	c.c.put(key, value, 0)
	return nil
}

func (c *DialogListSnapshotCache) sweep(now time.Time) int { return c.c.sweep(now) }
