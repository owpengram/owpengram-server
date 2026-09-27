package memory

import (
	"context"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// DefaultUserCacheTTL mirrors redisstore's default.
const DefaultUserCacheTTL = 5 * time.Minute

// UserCache is the in-process replacement for redisstore.UserCache. It
// caches viewer-independent base user rows. Redis needed a whole
// userBaseValue JSON envelope to survive serialization; in-process the
// domain.User is stored directly, so there is nothing to encode or corrupt.
type UserCache struct {
	c *ttlCache[int64, domain.User]
}

var _ store.UserCache = (*UserCache)(nil)

// NewUserCache builds the cache. ttl<=0 uses DefaultUserCacheTTL.
func NewUserCache(ttl time.Duration) *UserCache {
	if ttl <= 0 {
		ttl = DefaultUserCacheTTL
	}
	return &UserCache{c: newTTLCache[int64, domain.User](ttl)}
}

func (c *UserCache) GetByIDs(_ context.Context, ids []int64) (map[int64]domain.User, error) {
	out := make(map[int64]domain.User, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if u, ok := c.c.get(id); ok {
			out[id] = u
		}
	}
	return out, nil
}

func (c *UserCache) PutMany(_ context.Context, users []domain.User) error {
	for _, u := range users {
		if u.ID == 0 {
			continue
		}
		c.c.put(u.ID, u, 0)
	}
	return nil
}

func (c *UserCache) Delete(_ context.Context, ids []int64) error {
	c.c.deleteMany(ids)
	return nil
}

func (c *UserCache) sweep(now time.Time) int { return c.c.sweep(now) }
