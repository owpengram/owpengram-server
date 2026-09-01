package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"telesrv/internal/domain"
)

func TestChannelDifferenceBaseLoaderRejectsChangedStableCutPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	owner, err := NewUserStore(pool).Create(ctx, domain.User{
		AccessHash: 721, Phone: "+1993" + suffix + "01", FirstName: "DiffCutOwner",
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
	channels := NewChannelStore(pool)
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Difference Cut " + suffix, Megagroup: true, Date: 1701000200,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID = created.Channel.ID
	first, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: 1701000201, Message: "first cut", Date: 1701000201,
	})
	if err != nil {
		t.Fatal(err)
	}
	captured, member, _, err := channels.getChannelForViewer(ctx, pool, owner.ID, channelID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: 1701000202, Message: "future cut", Date: 1701000202,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = channels.loadChannelDifferenceBase(ctx, captured, member, owner.ID, created.Channel.Pts, 100, true)
	if !errors.Is(err, errChannelDifferenceCutChanged) {
		t.Fatalf("load against captured pts %d after new event = %v, want stable-cut retry", first.Event.Pts, err)
	}
	diff, err := channels.ListChannelDifference(ctx, domain.ChannelDifferenceRequest{
		UserID: owner.ID, ChannelID: channelID, Pts: created.Channel.Pts, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Final || len(diff.Events) != 2 || diff.Events[0].Pts != first.Event.Pts {
		t.Fatalf("retried difference = %+v, want both stable events", diff)
	}
}

func TestChannelDifferenceBaseCacheSharesDurablePageAcrossViewersPostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{AccessHash: 701, Phone: "+1991" + suffix + "01", FirstName: "DiffCacheOwner"})
	if err != nil {
		t.Fatal(err)
	}
	memberA, err := users.Create(ctx, domain.User{AccessHash: 702, Phone: "+1991" + suffix + "02", FirstName: "DiffCacheA"})
	if err != nil {
		t.Fatal(err)
	}
	memberB, err := users.Create(ctx, domain.User{AccessHash: 703, Phone: "+1991" + suffix + "03", FirstName: "DiffCacheB"})
	if err != nil {
		t.Fatal(err)
	}
	var channelID int64
	t.Cleanup(func() {
		if channelID != 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = $1", channelID)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = ANY($1::bigint[])", []int64{owner.ID, memberA.ID, memberB.ID})
	})

	cache := NewChannelDifferenceBaseCache(32, 8<<20, time.Minute)
	channels := NewChannelStore(pool, WithChannelDifferenceBaseCache(cache))
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID,
		Title:         "Shared Difference " + suffix,
		Megagroup:     true,
		MemberUserIDs: []int64{memberA.ID, memberB.ID},
		Date:          1701000000,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID = created.Channel.ID
	sent, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: 1701000001,
		Message: "shared immutable page", MentionUserIDs: []int64{memberA.ID}, Date: 1701000001,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := func(userID int64) domain.ChannelDifference {
		t.Helper()
		diff, err := channels.ListChannelDifference(ctx, domain.ChannelDifferenceRequest{
			UserID: userID, ChannelID: channelID, Pts: created.Channel.Pts, Limit: 100,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !diff.Final || diff.Pts != sent.Event.Pts || len(diff.NewMessages) != 1 || diff.NewMessages[0].Body != "shared immutable page" {
			t.Fatalf("difference for %d = %+v", userID, diff)
		}
		if diff.Self.UserID != userID || diff.Dialog.UserID != userID {
			t.Fatalf("viewer overlay crossed accounts: self=%d dialog=%d want=%d", diff.Self.UserID, diff.Dialog.UserID, userID)
		}
		return diff
	}
	first := request(memberA.ID)
	if !first.NewMessages[0].Mentioned {
		t.Fatalf("member A mention overlay missing: %+v", first.NewMessages[0])
	}
	second := request(memberB.ID)
	if second.NewMessages[0].Mentioned || second.NewMessages[0].MediaUnread {
		t.Fatalf("member B received member A mention overlay: %+v", second.NewMessages[0])
	}
	snapshot := cache.Snapshot()
	if snapshot.Loads != 1 || snapshot.Entries != 1 || snapshot.Hits < 1 {
		t.Fatalf("shared base snapshot = %+v, want one load and a hit", snapshot)
	}
	first.NewMessages[0].Body = "caller mutation"
	third := request(memberA.ID)
	if third.NewMessages[0].Body != "shared immutable page" {
		t.Fatalf("caller mutation leaked into cache: %+v", third.NewMessages[0])
	}
}

func TestChannelDifferenceRetentionInvalidatesSharedBasePostgres(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	owner, err := NewUserStore(pool).Create(ctx, domain.User{
		AccessHash: 711, Phone: "+1992" + suffix + "01", FirstName: "DiffRetentionOwner",
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

	cache := NewChannelDifferenceBaseCache(32, 8<<20, time.Minute)
	channels := NewChannelStore(pool, WithChannelDifferenceBaseCache(cache))
	created, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "Difference Retention " + suffix, Megagroup: true, Date: 1701000100,
	})
	if err != nil {
		t.Fatal(err)
	}
	channelID = created.Channel.ID
	first, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: channelID, RandomID: 1701000101, Message: "first", Date: 1701000101,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := channels.ListChannelDifference(ctx, domain.ChannelDifferenceRequest{
		UserID: owner.ID, ChannelID: channelID, Pts: created.Channel.Pts, Limit: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if cache.Snapshot().Entries != 1 {
		t.Fatalf("entries before prune = %d, want 1", cache.Snapshot().Entries)
	}
	pruned, err := channels.PruneChannelUpdateEvents(ctx, channelID, first.Event.Pts, 100)
	if err != nil {
		t.Fatal(err)
	}
	if pruned.Deleted == 0 || cache.Snapshot().Entries != 0 {
		t.Fatalf("prune/cache = %+v/%+v, want deletion and immediate invalidation", pruned, cache.Snapshot())
	}
	diff, err := channels.ListChannelDifference(ctx, domain.ChannelDifferenceRequest{
		UserID: owner.ID, ChannelID: channelID, Pts: created.Channel.Pts, Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !diff.TooLong || diff.Pts != first.Event.Pts {
		t.Fatalf("difference after retained floor = %+v, want tooLong at pts %d", diff, first.Event.Pts)
	}
}
