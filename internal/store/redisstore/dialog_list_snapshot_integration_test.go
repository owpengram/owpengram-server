package redisstore

import (
	"bytes"
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

func TestDialogListSnapshotCacheRoundTripAndCorruptFailClosed(t *testing.T) {
	addr := os.Getenv("TELESRV_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("set TELESRV_TEST_REDIS_ADDR to run redis integration test")
	}
	ctx := context.Background()
	c, err := Open(ctx, addr, "", 0)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	key := store.DialogListSnapshotCacheKey{
		UserID: time.Now().UnixNano(), OwnerHash: 7001,
	}
	redisKey := dialogListSnapshotKey(key)
	t.Cleanup(func() { _ = c.Del(ctx, redisKey).Err() })
	cache := NewDialogListSnapshotCache(c, time.Minute)
	want := store.DialogListSnapshotCacheValue{
		DependencyHash: 8001,
		Dialogs: []domain.Dialog{{
			Peer: domain.Peer{Type: domain.PeerTypeChannel, ID: 91}, TopMessage: 7, TopMessageDate: 70,
			Draft:         &domain.DialogDraft{Peer: domain.Peer{Type: domain.PeerTypeChannel, ID: 91}, Date: 71, Message: "shared draft"},
			DefaultSendAs: &domain.Peer{Type: domain.PeerTypeChannel, ID: 93},
			ChannelMember: &domain.ChannelMember{
				ChannelID: 91, UserID: 92, Role: domain.ChannelRoleAdmin, Status: domain.ChannelMemberActive,
				AvailableMinPts: 3, AdminRights: domain.ChannelAdminRights{PostMessages: true},
			},
		}},
		Messages: []domain.Message{{
			ID: 8, Peer: domain.Peer{Type: domain.PeerTypeUser, ID: 92}, Body: "private top",
		}},
		Users: []domain.User{{ID: 92, FirstName: "peer"}},
		State: domain.UpdateState{Pts: 3, Date: 4, Seq: 5},
	}
	if err := cache.PutDialogListSnapshot(ctx, key, want); err != nil {
		t.Fatalf("put: %v", err)
	}
	raw, err := c.Get(ctx, redisKey).Bytes()
	if err != nil {
		t.Fatalf("get encoded value: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte{0x28, 0xb5, 0x2f, 0xfd}) {
		t.Fatalf("snapshot is not a zstd frame: prefix=%x", raw[:min(len(raw), 4)])
	}
	got, found, err := cache.GetDialogListSnapshot(ctx, key)
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("get = %+v found=%v err=%v, want %+v", got, found, err, want)
	}
	if ttl, err := c.TTL(ctx, redisKey).Result(); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("ttl = %v err=%v", ttl, err)
	}

	if err := c.Set(ctx, redisKey, "{bad json", time.Minute).Err(); err != nil {
		t.Fatalf("seed corrupt value: %v", err)
	}
	if _, _, err := cache.GetDialogListSnapshot(ctx, key); err == nil {
		t.Fatal("corrupt cache value accepted")
	}
	if exists, err := c.Exists(ctx, redisKey).Result(); err != nil || exists != 0 {
		t.Fatalf("corrupt key exists=%d err=%v, want deleted", exists, err)
	}
}
