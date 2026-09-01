package redisstore

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"telesrv/internal/store"
)

func TestActiveChannelIDsPageCacheRoundTripAndCorruptFailClosed(t *testing.T) {
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
	key := store.ActiveChannelIDsPageKey{
		UserID: time.Now().UnixNano(), Generation: 701, AfterChannelID: 10, Limit: 1000,
	}
	redisKey := activeChannelIDsPageRedisKey(key)
	t.Cleanup(func() { _ = c.Del(ctx, redisKey).Err() })
	cache := NewActiveChannelIDsPageCache(c, time.Minute)
	want := []int64{11, 20, 30}
	if err := cache.PutActiveChannelIDsPage(ctx, key, want); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, found, err := cache.GetActiveChannelIDsPage(ctx, key)
	if err != nil || !found || !slices.Equal(got, want) {
		t.Fatalf("get = %v found=%v err=%v", got, found, err)
	}
	got[0] = 999
	again, _, err := cache.GetActiveChannelIDsPage(ctx, key)
	if err != nil || !slices.Equal(again, want) {
		t.Fatalf("cached value aliased: %v err=%v", again, err)
	}
	if ttl, err := c.TTL(ctx, redisKey).Result(); err != nil || ttl <= 0 || ttl > time.Minute {
		t.Fatalf("ttl = %v err=%v", ttl, err)
	}
	if err := c.Set(ctx, redisKey, `{"schema":1,"key":{"UserID":1}}`, time.Minute).Err(); err != nil {
		t.Fatalf("seed corrupt value: %v", err)
	}
	if _, _, err := cache.GetActiveChannelIDsPage(ctx, key); err == nil {
		t.Fatal("corrupt cache value accepted")
	}
	if exists, err := c.Exists(ctx, redisKey).Result(); err != nil || exists != 0 {
		t.Fatalf("corrupt key exists=%d err=%v", exists, err)
	}
}

func TestActiveChannelIDsPageCacheRejectsUnorderedPage(t *testing.T) {
	cache := NewActiveChannelIDsPageCache(nil, time.Minute)
	key := store.ActiveChannelIDsPageKey{UserID: 1, Generation: -1, Limit: 1000}
	if err := validateActiveChannelIDsPage(key, []int64{2, 2}); err == nil {
		t.Fatal("duplicate channel ID accepted")
	}
	if err := cache.PutActiveChannelIDsPage(context.Background(), key, nil); err == nil {
		t.Fatal("nil Redis client accepted")
	}
}
