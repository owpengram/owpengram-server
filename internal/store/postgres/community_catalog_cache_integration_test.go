package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestCommunityCatalogCacheInvalidatesFromDatabaseTrigger(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	cache := NewCommunityCatalogCache()

	active, err := cache.hasActive(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if active {
		t.Skip("test database already contains an active Community")
	}

	// A sentinel is a deterministic LISTEN-ready barrier: the listener flushes
	// it immediately after LISTEN succeeds.
	cache.cache.Store(communityCatalogPresenceKey, true)
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener := NewReadModelChangeListener(os.Getenv("TELESRV_TEST_POSTGRES_DSN"), ReadModelCacheSet{CommunityCatalog: cache}, nil)
	go listener.Run(lctx)
	if !waitUntil(2*time.Second, func() bool {
		_, ok := cache.cache.Peek(communityCatalogPresenceKey)
		return !ok
	}) {
		t.Fatal("read-model listener did not flush Community sentinel")
	}
	if active, err = cache.hasActive(ctx, pool); err != nil || active {
		t.Fatalf("re-warm empty catalog active=%v err=%v", active, err)
	}

	users := NewUserStore(pool)
	suffix := randomSuffix(t)
	owner, err := users.Create(ctx, domain.User{AccessHash: 471, Phone: "+1887" + suffix + "01", FirstName: "CommunityGateOwner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	communityID := time.Now().UnixNano() & 0x3fffffffffffffff
	if communityID == 0 {
		communityID = 1
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM communities WHERE id=$1", communityID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id=$1", owner.ID)
	})
	if _, err := pool.Exec(ctx, `
INSERT INTO communities(id,access_hash,creator_user_id,title,date)
VALUES($1,$2,$3,$4,$5)`, communityID, -communityID, owner.ID, "Catalog trigger "+suffix, 1700000770); err != nil {
		t.Fatalf("insert Community: %v", err)
	}
	if !waitUntil(3*time.Second, func() bool {
		_, ok := cache.cache.Peek(communityCatalogPresenceKey)
		return !ok
	}) {
		t.Fatal("communities trigger did not invalidate catalog presence")
	}
	active, err = cache.hasActive(ctx, pool)
	if err != nil || !active {
		t.Fatalf("catalog after insert active=%v err=%v, want true", active, err)
	}
}
