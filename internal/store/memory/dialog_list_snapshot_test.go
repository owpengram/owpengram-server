package memory

import (
	"context"
	"testing"
	"time"

	"telesrv/internal/store"
)

func TestDialogListSnapshotCacheMissesOnOtherOwnerHash(t *testing.T) {
	ctx := context.Background()
	c := NewDialogListSnapshotCache(time.Hour)
	key := store.DialogListSnapshotCacheKey{UserID: 7, OwnerHash: 100}
	if err := c.PutDialogListSnapshot(ctx, key, store.DialogListSnapshotCacheValue{DependencyHash: 1}); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := c.GetDialogListSnapshot(ctx, key); !ok || v.DependencyHash != 1 {
		t.Fatalf("Get same key = %+v, %v; want hit", v, ok)
	}
	if _, ok, _ := c.GetDialogListSnapshot(ctx, store.DialogListSnapshotCacheKey{UserID: 7, OwnerHash: 101}); ok {
		t.Fatal("Get with another OwnerHash must miss")
	}
	if _, ok, _ := c.GetDialogListSnapshot(ctx, store.DialogListSnapshotCacheKey{UserID: 8, OwnerHash: 100}); ok {
		t.Fatal("Get for another user must miss")
	}
}

// A new OwnerHash replaces the user's entry instead of adding one: superseded
// snapshots must not pile up until their TTL.
func TestDialogListSnapshotCacheKeepsOneEntryPerUser(t *testing.T) {
	ctx := context.Background()
	c := NewDialogListSnapshotCache(time.Hour)
	for hash := int64(1); hash <= 50; hash++ {
		key := store.DialogListSnapshotCacheKey{UserID: 7, OwnerHash: hash}
		if err := c.PutDialogListSnapshot(ctx, key, store.DialogListSnapshotCacheValue{DependencyHash: hash}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(c.c.items); n != 1 {
		t.Fatalf("entries = %d, want 1", n)
	}
	if v, ok, _ := c.GetDialogListSnapshot(ctx, store.DialogListSnapshotCacheKey{UserID: 7, OwnerHash: 50}); !ok || v.DependencyHash != 50 {
		t.Fatalf("latest Get = %+v, %v; want the hash-50 value", v, ok)
	}
}
