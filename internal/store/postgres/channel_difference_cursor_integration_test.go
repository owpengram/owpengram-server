package postgres

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/domain"
)

func TestChannelDifferenceValidCursorBeforeRowCacheInvalidationPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	owner, err := NewUserStore(pool).Create(ctx, domain.User{
		AccessHash: 731, Phone: "+1994" + randomSuffix(t) + "01", FirstName: "CursorOwner",
	})
	if err != nil {
		t.Fatal(err)
	}
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})
	cache := NewChannelRowCache(16)
	channels := NewChannelStore(pool, WithChannelRowCache(cache))
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Cursor notification window", Megagroup: true, Date: 1701000300,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID = created.Channel.ID
	// Populate the real read-through cache, then commit a real send. Keeping
	// the listener paused models the interval between commit/push and NOTIFY
	// consumption; no persisted cursor or event is fabricated.
	if _, _, _, err := channels.getChannelForViewer(ctx, pool, owner.ID, channelID); err != nil {
		t.Fatal(err)
	}
	sent, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: 1701000301, Message: "committed cursor", Date: 1701000301,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cached, ok := cache.get(channelID); !ok || cached.Pts != created.Channel.Pts {
		t.Fatalf("expected pre-notification cached pts %d, got %d (cached=%v)", created.Channel.Pts, cached.Pts, ok)
	}
	for _, pts := range []int{-1, sent.Event.Pts + 1} {
		if _, err := channels.ListChannelDifference(ctx, domain.ChannelDifferenceRequest{
			UserID: owner.ID, ChannelID: channelID, Pts: pts, Limit: 100,
		}); !errors.Is(err, domain.ErrPersistentTimestamp) {
			t.Fatalf("invalid cursor %d error = %v, want ErrPersistentTimestamp", pts, err)
		}
	}
	diff, err := channels.ListChannelDifference(ctx, domain.ChannelDifferenceRequest{
		UserID: owner.ID, ChannelID: channelID, Pts: sent.Event.Pts, Limit: 100,
	})
	if err != nil {
		t.Fatalf("valid committed cursor rejected before cache invalidation: %v", err)
	}
	if !diff.Final || diff.Pts != sent.Event.Pts || len(diff.Events) != 0 || diff.TooLong {
		t.Fatalf("current committed cursor difference = %+v", diff)
	}
	// A retry must still load the durable tail, not turn every now-valid
	// cursor into an empty response at the latest PTS.
	second, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: 1701000302, Message: "second cursor", Date: 1701000302,
	})
	if err != nil {
		t.Fatal(err)
	}
	third, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: 1701000303, Message: "durable tail", Date: 1701000303,
	})
	if err != nil {
		t.Fatal(err)
	}
	diff, err = channels.ListChannelDifference(ctx, domain.ChannelDifferenceRequest{
		UserID: owner.ID, ChannelID: channelID, Pts: second.Event.Pts, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Final || diff.Pts != third.Event.Pts || len(diff.NewMessages) != 1 || diff.NewMessages[0].ID != third.Message.ID {
		t.Fatalf("committed cursor retry lost durable tail: %+v", diff)
	}
}
