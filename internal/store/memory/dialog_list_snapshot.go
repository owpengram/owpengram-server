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
//
// Entries are held one per user, not one per (user, OwnerHash): OwnerHash
// changes on every dialog change, so keying by it kept every superseded
// snapshot alive until its TTL -- in the 8k-session load test (2026-11-24)
// that was ~200 MB of dead snapshots per minute. A Get with a different
// OwnerHash is a miss, exactly as a different key was before.
type DialogListSnapshotCache struct {
	c *ttlCache[int64, dialogListSnapshotEntry]
}

type dialogListSnapshotEntry struct {
	ownerHash int64
	value     store.DialogListSnapshotCacheValue
}

var _ store.DialogListSnapshotCache = (*DialogListSnapshotCache)(nil)

// NewDialogListSnapshotCache builds the cache. ttl<=0 uses
// DefaultDialogListSnapshotTTL.
func NewDialogListSnapshotCache(ttl time.Duration) *DialogListSnapshotCache {
	if ttl <= 0 {
		ttl = DefaultDialogListSnapshotTTL
	}
	return &DialogListSnapshotCache{c: newTTLCache[int64, dialogListSnapshotEntry](ttl)}
}

func (c *DialogListSnapshotCache) GetDialogListSnapshot(_ context.Context, key store.DialogListSnapshotCacheKey) (store.DialogListSnapshotCacheValue, bool, error) {
	entry, ok := c.c.get(key.UserID)
	if !ok || entry.ownerHash != key.OwnerHash {
		return store.DialogListSnapshotCacheValue{}, false, nil
	}
	return entry.value, true, nil
}

func (c *DialogListSnapshotCache) PutDialogListSnapshot(_ context.Context, key store.DialogListSnapshotCacheKey, value store.DialogListSnapshotCacheValue) error {
	c.c.put(key.UserID, dialogListSnapshotEntry{ownerHash: key.OwnerHash, value: value}, 0)
	return nil
}

func (c *DialogListSnapshotCache) sweep(now time.Time) int { return c.c.sweep(now) }
