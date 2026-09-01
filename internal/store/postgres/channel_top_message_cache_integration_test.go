package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestChannelTopMessageCacheInvalidatesOnTopPayloadNotify(t *testing.T) {
	pool := testPool(t)
	dsn := os.Getenv("TELESRV_TEST_POSTGRES_DSN")
	ctx := context.Background()
	suffix := randomSuffix(t)

	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 461,
		Phone:      "+1888" + suffix + "91",
		FirstName:  "TopCacheOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})

	topCache := NewChannelTopMessageCache(32)
	rowCache := NewChannelRowCache(32)
	channels := NewChannelStore(pool,
		WithChannelRowCache(rowCache),
		WithChannelTopMessageCache(topCache),
	)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Top cache " + suffix,
		Megagroup:     true,
		Date:          1700000760,
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	channelID = created.Channel.ID
	sent, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID:    owner.ID,
		ChannelID: channelID,
		RandomID:  761,
		Message:   "before cache invalidation",
		Date:      1700000761,
	})
	if err != nil {
		t.Fatalf("send channel message: %v", err)
	}
	key := channelMessageLookupKey{channelID: channelID, id: sent.Message.ID}

	// Seed one sentinel so listener reconnect flush is an observable readiness
	// barrier instead of a timing sleep.
	topCache.cache.Store(key, domain.ChannelMessage{ChannelID: channelID, ID: sent.Message.ID, Body: "sentinel"})
	lctx, cancel := context.WithCancel(ctx)
	defer cancel()
	listener := NewReadModelChangeListener(dsn, ReadModelCacheSet{
		ChannelRows:        rowCache,
		ChannelTopMessages: topCache,
	}, nil)
	go listener.Run(lctx)
	if !waitUntil(2*time.Second, func() bool {
		_, ok := topCache.cache.Peek(key)
		return !ok
	}) {
		t.Fatal("read-model listener did not establish LISTEN and flush sentinel")
	}

	view, err := channels.GetChannelDialogs(ctx, owner.ID, []int64{channelID})
	if err != nil {
		t.Fatalf("warm channel dialog: %v", err)
	}
	if len(view.Messages) != 1 || view.Messages[0].Body != "before cache invalidation" {
		t.Fatalf("warm messages = %+v", view.Messages)
	}
	if cached, ok := topCache.cache.Peek(key); !ok || cached.Body != "before cache invalidation" {
		t.Fatalf("top payload was not cached: ok=%v value=%+v", ok, cached)
	}
	materialized, err := channels.HydrateChannelDialogSnapshot(ctx, owner.ID, view.Dialogs)
	if err != nil {
		t.Fatalf("hydrate materialized owner dialog: %v", err)
	}
	if len(materialized.Dialogs) != 1 || len(materialized.Channels) != 1 || len(materialized.Messages) != 1 ||
		materialized.Dialogs[0].Peer.ID != channelID || materialized.Messages[0].Body != "before cache invalidation" {
		t.Fatalf("materialized channel snapshot = dialogs:%+v channels:%+v messages:%+v",
			materialized.Dialogs, materialized.Channels, materialized.Messages)
	}

	const after = "after cache invalidation"
	if _, err := pool.Exec(ctx, `
UPDATE channel_messages
SET body=$3, edit_date=$4
WHERE channel_id=$1 AND id=$2`, channelID, sent.Message.ID, after, 1700000762); err != nil {
		t.Fatalf("update top payload: %v", err)
	}
	if !waitUntil(3*time.Second, func() bool {
		_, ok := topCache.cache.Peek(key)
		return !ok
	}) {
		t.Fatal("top channel message cache was not invalidated by channel_base")
	}

	view, err = channels.GetChannelDialogs(ctx, owner.ID, []int64{channelID})
	if err != nil {
		t.Fatalf("read channel dialog after invalidation: %v", err)
	}
	if len(view.Messages) != 1 || view.Messages[0].Body != after {
		t.Fatalf("post-invalidation messages = %+v, want body %q", view.Messages, after)
	}
}
