package postgres

import (
	"context"

	"telesrv/internal/readmodelcache"
	"telesrv/internal/store/postgres/sqlcgen"
)

const communityCatalogPresenceKey = "active"

// CommunityCatalogCache is the global, version-invalidated gate in front of
// owner-specific joined-Community reads. It caches only whether any non-deleted
// Community exists; it never caches membership, collapsed/pinned state or a
// Community payload.
type CommunityCatalogCache struct {
	cache *readmodelcache.Cache[string, bool]
}

func NewCommunityCatalogCache() *CommunityCatalogCache {
	return &CommunityCatalogCache{cache: readmodelcache.New[string, bool](readmodelcache.Config[string, bool]{MaxEntries: 1})}
}

func (c *CommunityCatalogCache) hasActive(ctx context.Context, db sqlcgen.DBTX) (bool, error) {
	if c == nil {
		var active bool
		err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM communities WHERE NOT deleted)`).Scan(&active)
		return active, err
	}
	return c.cache.GetOrLoad(ctx, communityCatalogPresenceKey, func() (bool, error) {
		var active bool
		err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM communities WHERE NOT deleted)`).Scan(&active)
		return active, err
	})
}

func (c *CommunityCatalogCache) invalidate() {
	if c != nil {
		c.cache.Invalidate(communityCatalogPresenceKey)
	}
}

func (c *CommunityCatalogCache) flush() {
	if c != nil {
		c.cache.Flush()
	}
}
