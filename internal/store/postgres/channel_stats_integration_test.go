package postgres

import (
	"context"
	"testing"

	"telesrv/internal/domain"
)

func TestChannelStatsPostgresAggregatesAndPagesPublicForwards(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	suffix := randomSuffix(t)
	users := NewUserStore(pool)
	owner, err := users.Create(ctx, domain.User{
		AccessHash: 99001, Phone: "+1888" + suffix + "01", FirstName: "StatsOwner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	channels := NewChannelStore(pool)
	channelIDs := make([]int64, 0, 3)
	t.Cleanup(func() {
		if len(channelIDs) > 0 {
			_, _ = pool.Exec(ctx, "DELETE FROM channels WHERE id = ANY($1::bigint[])", channelIDs)
		}
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", owner.ID)
	})
	period := domain.StatsPeriod{MinDate: 1_700_006_400, MaxDate: 1_700_611_200}
	source, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "PG stats source " + suffix, Broadcast: true, Date: period.MinDate - 100,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	channelIDs = append(channelIDs, source.Channel.ID)
	if _, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: source.Channel.ID, RandomID: 1, Message: "previous", Date: period.MinDate - 10,
	}); err != nil {
		t.Fatalf("send previous: %v", err)
	}
	post, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: source.Channel.ID, RandomID: 2, Message: "current", Date: period.MinDate + 10,
	})
	if err != nil {
		t.Fatalf("send current: %v", err)
	}
	if _, err := channels.GetChannelMessageViews(ctx, domain.ChannelMessageViewsRequest{
		UserID: owner.ID, ChannelID: source.Channel.ID, IDs: []int{post.Message.ID}, Increment: true, Date: period.MinDate + 20,
	}); err != nil {
		t.Fatalf("increment view: %v", err)
	}
	if _, err := channels.SetChannelMessageReactions(ctx, domain.SetChannelMessageReactionsRequest{
		UserID: owner.ID, ChannelID: source.Channel.ID, MessageID: post.Message.ID,
		Reactions: []domain.MessageReaction{{Type: domain.MessageReactionEmoji, Emoticon: "👍"}}, Date: period.MinDate + 30,
	}); err != nil {
		t.Fatalf("react: %v", err)
	}

	publicCreated, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "PG public destination " + suffix, Broadcast: true, Date: period.MinDate + 40,
	})
	if err != nil {
		t.Fatalf("create public destination: %v", err)
	}
	channelIDs = append(channelIDs, publicCreated.Channel.ID)
	publicChannel, err := channels.UpdateUsername(ctx, domain.UpdateChannelUsernameRequest{
		UserID: owner.ID, ChannelID: publicCreated.Channel.ID, Username: "statsfw" + suffix,
	})
	if err != nil {
		t.Fatalf("make destination public: %v", err)
	}
	privateCreated, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "PG private destination " + suffix, Broadcast: true, Date: period.MinDate + 40,
	})
	if err != nil {
		t.Fatalf("create private destination: %v", err)
	}
	channelIDs = append(channelIDs, privateCreated.Channel.ID)
	forward := &domain.MessageForward{
		From: domain.Peer{Type: domain.PeerTypeChannel, ID: source.Channel.ID}, Date: post.Message.Date, ChannelPost: post.Message.ID,
	}
	for i, date := range []int{period.MinDate + 50, period.MinDate + 60} {
		if _, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
			UserID: owner.ID, ChannelID: publicChannel.ID, RandomID: int64(10 + i), Message: "public forward", Forward: forward, Date: date,
		}); err != nil {
			t.Fatalf("send public forward %d: %v", i, err)
		}
	}
	if _, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: privateCreated.Channel.ID, RandomID: 20, Message: "private forward", Forward: forward, Date: period.MinDate + 70,
	}); err != nil {
		t.Fatalf("send private forward: %v", err)
	}

	stats, err := channels.GetChannelStats(ctx, domain.ChannelStatsRequest{
		ViewerUserID: owner.ID, ChannelID: source.Channel.ID, Period: period,
	})
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.Members.Current != 1 || stats.Messages.Current != 1 || stats.Messages.Previous != 1 ||
		stats.Viewers.Current != 1 || stats.ViewsPerPost.Current != 1 || stats.SharesPerPost.Current != 2 ||
		stats.ReactionsPerPost.Current != 1 {
		t.Fatalf("stats = %+v, want persisted values", stats)
	}
	if len(stats.Days) == 0 || stats.Days[0].Views != 1 || stats.Days[0].Shares != 2 || stats.Days[0].Reactions != 1 {
		t.Fatalf("stats days = %+v", stats.Days)
	}
	messageStats, err := channels.GetChannelMessageStats(ctx, domain.ChannelMessageStatsRequest{
		ViewerUserID: owner.ID, ChannelID: source.Channel.ID, MessageID: post.Message.ID, Period: period,
	})
	if err != nil {
		t.Fatalf("get message stats: %v", err)
	}
	if len(messageStats.Days) == 0 || messageStats.Days[0].Views != 1 || messageStats.Days[0].Reactions != 1 {
		t.Fatalf("message stats days = %+v", messageStats.Days)
	}
	first, err := channels.ListChannelMessagePublicForwards(ctx, domain.ChannelMessagePublicForwardListRequest{
		ViewerUserID: owner.ID, ChannelID: source.Channel.ID, MessageID: post.Message.ID, Limit: 1,
	})
	if err != nil {
		t.Fatalf("list first page: %v", err)
	}
	if first.Count != 2 || len(first.Messages) != 1 || first.NextOffset == "" {
		t.Fatalf("first page = %+v", first)
	}
	second, err := channels.ListChannelMessagePublicForwards(ctx, domain.ChannelMessagePublicForwardListRequest{
		ViewerUserID: owner.ID, ChannelID: source.Channel.ID, MessageID: post.Message.ID, Offset: first.NextOffset, Limit: 1,
	})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if second.Count != 2 || len(second.Messages) != 1 || second.Messages[0].ID == first.Messages[0].ID || second.NextOffset != "" {
		t.Fatalf("second page = %+v", second)
	}
}
