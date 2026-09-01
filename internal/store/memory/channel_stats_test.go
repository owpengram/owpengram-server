package memory

import (
	"context"
	"errors"
	"testing"

	"telesrv/internal/domain"
)

func TestChannelStatsUseDurableFactsAndPagePublicForwards(t *testing.T) {
	ctx := context.Background()
	store := NewChannelStore()
	const owner, viewer int64 = 1, 2
	period := domain.StatsPeriod{MinDate: 1_700_006_400, MaxDate: 1_700_611_200}

	source, err := store.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner,
		Title:         "stats source",
		Broadcast:     true,
		MemberUserIDs: []int64{viewer},
		Date:          period.MinDate - 100,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	previous, err := store.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner, ChannelID: source.Channel.ID, RandomID: 100, Message: "previous", Date: period.MinDate - 10,
	})
	if err != nil {
		t.Fatalf("send previous: %v", err)
	}
	_ = previous
	post, err := store.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner, ChannelID: source.Channel.ID, RandomID: 101, Message: "current post", Date: period.MinDate + 10,
	})
	if err != nil {
		t.Fatalf("send current: %v", err)
	}
	if _, err := store.GetChannelMessageViews(ctx, domain.ChannelMessageViewsRequest{
		UserID: viewer, ChannelID: source.Channel.ID, IDs: []int{post.Message.ID}, Increment: true, Date: period.MinDate + 20,
	}); err != nil {
		t.Fatalf("increment view: %v", err)
	}
	reaction := domain.MessageReaction{Type: domain.MessageReactionEmoji, Emoticon: "👍"}
	if _, err := store.SetChannelMessageReactions(ctx, domain.SetChannelMessageReactionsRequest{
		UserID: viewer, ChannelID: source.Channel.ID, MessageID: post.Message.ID, Reactions: []domain.MessageReaction{reaction}, Date: period.MinDate + 30,
	}); err != nil {
		t.Fatalf("react: %v", err)
	}

	publicCreated, err := store.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner, Title: "public destination", Broadcast: true, Date: period.MinDate + 40,
	})
	if err != nil {
		t.Fatalf("create public destination: %v", err)
	}
	publicChannel, err := store.UpdateUsername(ctx, domain.UpdateChannelUsernameRequest{
		UserID: owner, ChannelID: publicCreated.Channel.ID, Username: "stats_forward_memory",
	})
	if err != nil {
		t.Fatalf("make public destination: %v", err)
	}
	privateChannel, err := store.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner, Title: "private destination", Broadcast: true, Date: period.MinDate + 40,
	})
	if err != nil {
		t.Fatalf("create private destination: %v", err)
	}
	forward := &domain.MessageForward{
		From: domain.Peer{Type: domain.PeerTypeChannel, ID: source.Channel.ID}, Date: post.Message.Date, ChannelPost: post.Message.ID,
	}
	for i, date := range []int{period.MinDate + 50, period.MinDate + 60} {
		if _, err := store.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
			UserID: owner, ChannelID: publicChannel.ID, RandomID: int64(200 + i), Message: "public forward", Forward: forward, Date: date,
		}); err != nil {
			t.Fatalf("send public forward %d: %v", i, err)
		}
	}
	if _, err := store.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner, ChannelID: privateChannel.Channel.ID, RandomID: 300, Message: "private forward", Forward: forward, Date: period.MinDate + 70,
	}); err != nil {
		t.Fatalf("send private forward: %v", err)
	}

	stats, err := store.GetChannelStats(ctx, domain.ChannelStatsRequest{
		ViewerUserID: owner, ChannelID: source.Channel.ID, Period: period,
	})
	if err != nil {
		t.Fatalf("get stats: %v", err)
	}
	if stats.Members.Current != 2 || stats.Messages.Current != 1 || stats.Messages.Previous != 1 ||
		stats.Viewers.Current != 1 || stats.Posters.Current != 1 || stats.ViewsPerPost.Current != 1 ||
		stats.SharesPerPost.Current != 2 || stats.ReactionsPerPost.Current != 1 {
		t.Fatalf("stats = %+v, want durable current/previous aggregates", stats)
	}
	if len(stats.Days) == 0 || len(stats.Days[0].ByReaction) != 1 || stats.Days[0].Shares != 2 {
		t.Fatalf("stats days = %+v, want view/share/reaction buckets", stats.Days)
	}
	messageStats, err := store.GetChannelMessageStats(ctx, domain.ChannelMessageStatsRequest{
		ViewerUserID: owner, ChannelID: source.Channel.ID, MessageID: post.Message.ID, Period: period,
	})
	if err != nil {
		t.Fatalf("get message stats: %v", err)
	}
	if len(messageStats.Days) == 0 || messageStats.Days[0].Views != 1 || messageStats.Days[0].Reactions != 1 {
		t.Fatalf("message stats days = %+v, want one view and reaction", messageStats.Days)
	}

	first, err := store.ListChannelMessagePublicForwards(ctx, domain.ChannelMessagePublicForwardListRequest{
		ViewerUserID: owner, ChannelID: source.Channel.ID, MessageID: post.Message.ID, Limit: 1,
	})
	if err != nil {
		t.Fatalf("list first forward page: %v", err)
	}
	if first.Count != 2 || len(first.Messages) != 1 || first.NextOffset == "" || first.Messages[0].ChannelID != publicChannel.ID {
		t.Fatalf("first forward page = %+v, want one of two public forwards", first)
	}
	second, err := store.ListChannelMessagePublicForwards(ctx, domain.ChannelMessagePublicForwardListRequest{
		ViewerUserID: owner, ChannelID: source.Channel.ID, MessageID: post.Message.ID, Offset: first.NextOffset, Limit: 1,
	})
	if err != nil {
		t.Fatalf("list second forward page: %v", err)
	}
	if second.Count != 2 || len(second.Messages) != 1 || second.Messages[0].ID == first.Messages[0].ID || second.NextOffset != "" {
		t.Fatalf("second forward page = %+v, want remaining public forward", second)
	}
	if _, err := store.ListChannelMessagePublicForwards(ctx, domain.ChannelMessagePublicForwardListRequest{
		ViewerUserID: owner, ChannelID: source.Channel.ID, MessageID: post.Message.ID, Offset: "bad", Limit: 1,
	}); !errors.Is(err, domain.ErrStatsOffsetInvalid) {
		t.Fatalf("invalid cursor err = %v, want ErrStatsOffsetInvalid", err)
	}
}
