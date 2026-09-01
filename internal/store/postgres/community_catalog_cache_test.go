package postgres

import "testing"

func TestCommunityCatalogCacheInvalidatesAndFlushes(t *testing.T) {
	cache := NewCommunityCatalogCache()
	cache.cache.Store(communityCatalogPresenceKey, false)
	listener := NewReadModelChangeListener("", ReadModelCacheSet{CommunityCatalog: cache}, nil)
	listener.handlePayload(`{"model":"community_catalog","owner_user_id":0,"peer_type":"community","peer_id":0}`)
	if _, ok := cache.cache.Peek(communityCatalogPresenceKey); ok {
		t.Fatal("community_catalog event did not invalidate presence gate")
	}
	cache.cache.Store(communityCatalogPresenceKey, true)
	listener.flush("test")
	if _, ok := cache.cache.Peek(communityCatalogPresenceKey); ok {
		t.Fatal("listener flush did not clear community catalog gate")
	}
}
