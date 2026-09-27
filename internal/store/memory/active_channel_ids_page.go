package memory

import (
	"context"
	"time"

	"telesrv/internal/store"
)

// DefaultActiveChannelIDsPageTTL mirrors redisstore's default.
const DefaultActiveChannelIDsPageTTL = 24 * time.Hour

// ActiveChannelIDsPageCache is the in-process replacement for
// redisstore.ActiveChannelIDsPageCache. It is a rebuildable read cache --
// store.ActiveChannelIDsPageKey is already a plain comparable struct, so it
// serves directly as the map key with no encoding/hashing needed (Redis had
// to derive a string key; a Go map does not).
type ActiveChannelIDsPageCache struct {
	c *ttlCache[store.ActiveChannelIDsPageKey, []int64]
}

var _ store.ActiveChannelIDsPageCache = (*ActiveChannelIDsPageCache)(nil)

// NewActiveChannelIDsPageCache builds the cache. ttl<=0 uses
// DefaultActiveChannelIDsPageTTL.
func NewActiveChannelIDsPageCache(ttl time.Duration) *ActiveChannelIDsPageCache {
	if ttl <= 0 {
		ttl = DefaultActiveChannelIDsPageTTL
	}
	return &ActiveChannelIDsPageCache{c: newTTLCache[store.ActiveChannelIDsPageKey, []int64](ttl)}
}

func (c *ActiveChannelIDsPageCache) GetActiveChannelIDsPage(_ context.Context, key store.ActiveChannelIDsPageKey) ([]int64, bool, error) {
	ids, ok := c.c.get(key)
	if !ok {
		return nil, false, nil
	}
	return append([]int64(nil), ids...), true, nil
}

func (c *ActiveChannelIDsPageCache) PutActiveChannelIDsPage(_ context.Context, key store.ActiveChannelIDsPageKey, ids []int64) error {
	c.c.put(key, append([]int64(nil), ids...), 0)
	return nil
}

// sweep satisfies the unexported sweepable interface in sweeper.go.
func (c *ActiveChannelIDsPageCache) sweep(now time.Time) int { return c.c.sweep(now) }
