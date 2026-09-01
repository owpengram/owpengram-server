package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func TestUserStoreUpdateLastSeenBatchPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	users := NewUserStore(pool)
	suffix := time.Now().UnixNano() % 1_000_000_000
	first, err := users.Create(ctx, domain.User{
		AccessHash: 1, Phone: fmt.Sprintf("17781%d", suffix), FirstName: "PresenceBatchFirst",
	})
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := users.Create(ctx, domain.User{
		AccessHash: 2, Phone: fmt.Sprintf("17782%d", suffix), FirstName: "PresenceBatchSecond",
	})
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{first.ID, second.ID})
	})

	if err := users.UpdateLastSeenBatch(ctx, []store.UserLastSeenUpdate{
		{UserID: second.ID, LastSeenAt: 20},
		{UserID: first.ID, LastSeenAt: 10},
		{UserID: first.ID, LastSeenAt: 30},
	}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if err := users.UpdateLastSeenBatch(ctx, []store.UserLastSeenUpdate{
		{UserID: first.ID, LastSeenAt: 5},
		{UserID: second.ID, LastSeenAt: 25},
	}); err != nil {
		t.Fatalf("second batch: %v", err)
	}

	loadedFirst, found, err := users.ByID(ctx, first.ID)
	if err != nil || !found || loadedFirst.LastSeenAt != 30 {
		t.Fatalf("first last seen = %d found=%v err=%v, want 30", loadedFirst.LastSeenAt, found, err)
	}
	loadedSecond, found, err := users.ByID(ctx, second.ID)
	if err != nil || !found || loadedSecond.LastSeenAt != 25 {
		t.Fatalf("second last seen = %d found=%v err=%v, want 25", loadedSecond.LastSeenAt, found, err)
	}
}
