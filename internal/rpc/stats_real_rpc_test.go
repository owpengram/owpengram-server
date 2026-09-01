package rpc

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap/zaptest"

	appchannels "telesrv/internal/app/channels"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func TestStatsRPCProjectsRealGraphsAndPublicForwardPages(t *testing.T) {
	ctx := context.Background()
	const now = 1_700_611_200
	users := memory.NewUserStore()
	owner, err := users.Create(ctx, domain.User{AccessHash: 901, Phone: "15550000901", FirstName: "Stats"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	channels := memory.NewChannelStore()
	source, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "stats source", Broadcast: true, Date: now - 100,
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	post, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
		UserID: owner.ID, ChannelID: source.Channel.ID, RandomID: 1, Message: "post", Date: now - 90,
	})
	if err != nil {
		t.Fatalf("send post: %v", err)
	}
	if _, err := channels.GetChannelMessageViews(ctx, domain.ChannelMessageViewsRequest{
		UserID: owner.ID, ChannelID: source.Channel.ID, IDs: []int{post.Message.ID}, Increment: true, Date: now - 80,
	}); err != nil {
		t.Fatalf("increment post view: %v", err)
	}
	if _, err := channels.SetChannelMessageReactions(ctx, domain.SetChannelMessageReactionsRequest{
		UserID: owner.ID, ChannelID: source.Channel.ID, MessageID: post.Message.ID,
		Reactions: []domain.MessageReaction{{Type: domain.MessageReactionEmoji, Emoticon: "🔥"}}, Date: now - 70,
	}); err != nil {
		t.Fatalf("react to post: %v", err)
	}
	destinationCreated, err := channels.CreateChannel(ctx, domain.CreateChannelRequest{
		CreatorUserID: owner.ID, Title: "public destination", Broadcast: true, Date: now - 60,
	})
	if err != nil {
		t.Fatalf("create destination: %v", err)
	}
	destination, err := channels.UpdateUsername(ctx, domain.UpdateChannelUsernameRequest{
		UserID: owner.ID, ChannelID: destinationCreated.Channel.ID, Username: "stats_rpc_forward",
	})
	if err != nil {
		t.Fatalf("make destination public: %v", err)
	}
	forward := &domain.MessageForward{
		From: domain.Peer{Type: domain.PeerTypeChannel, ID: source.Channel.ID}, Date: post.Message.Date, ChannelPost: post.Message.ID,
	}
	for i := 0; i < 2; i++ {
		if _, err := channels.SendChannelMessage(ctx, domain.SendChannelMessageRequest{
			UserID: owner.ID, ChannelID: destination.ID, RandomID: int64(10 + i), Message: "forward", Forward: forward, Date: now - 50 + i,
		}); err != nil {
			t.Fatalf("send forward %d: %v", i, err)
		}
	}
	r := New(Config{}, Deps{
		Users: appusers.NewService(users), Channels: appchannels.NewService(channels),
	}, zaptest.NewLogger(t), fixedClock{now: time.Unix(now, 0)})
	requestCtx := WithUserID(ctx, owner.ID)
	inputSource := &tg.InputChannel{ChannelID: source.Channel.ID, AccessHash: source.Channel.AccessHash}

	broadcast, err := r.onStatsGetBroadcastStats(requestCtx, &tg.StatsGetBroadcastStatsRequest{Channel: inputSource})
	if err != nil {
		t.Fatalf("get broadcast stats: %v", err)
	}
	if broadcast.Followers.Current != 1 || broadcast.ViewsPerPost.Current != 1 ||
		broadcast.SharesPerPost.Current != 2 || broadcast.ReactionsPerPost.Current != 1 {
		t.Fatalf("broadcast metrics = %+v, want real durable values", broadcast)
	}
	graph, ok := broadcast.InteractionsGraph.(*tg.StatsGraph)
	if !ok {
		t.Fatalf("interactions graph = %T, want *tg.StatsGraph", broadcast.InteractionsGraph)
	}
	var payload struct {
		Columns [][]any           `json:"columns"`
		Colors  map[string]string `json:"colors"`
	}
	if err := json.Unmarshal([]byte(graph.JSON.Data), &payload); err != nil {
		t.Fatalf("decode interactions graph: %v (%s)", err, graph.JSON.Data)
	}
	if len(payload.Columns) != 3 || payload.Colors["y0"] != "#4A90E2" {
		t.Fatalf("interactions graph payload = %+v", payload)
	}
	if _, ok := broadcast.LanguagesGraph.(*tg.StatsGraphError); !ok {
		t.Fatalf("unsupported language graph = %T, want explicit statsGraphError", broadcast.LanguagesGraph)
	}

	messageStats, err := r.onStatsGetMessageStats(requestCtx, &tg.StatsGetMessageStatsRequest{
		Channel: inputSource, MsgID: post.Message.ID,
	})
	if err != nil {
		t.Fatalf("get message stats: %v", err)
	}
	if _, ok := messageStats.ViewsGraph.(*tg.StatsGraph); !ok {
		t.Fatalf("message views graph = %T, want real statsGraph", messageStats.ViewsGraph)
	}

	first, err := r.onStatsGetMessagePublicForwards(requestCtx, &tg.StatsGetMessagePublicForwardsRequest{
		Channel: inputSource, MsgID: post.Message.ID, Limit: 1,
	})
	if err != nil {
		t.Fatalf("get first public forwards page: %v", err)
	}
	next, ok := first.GetNextOffset()
	if first.Count != 2 || len(first.Forwards) != 1 || !ok || next == "" || len(first.Chats) != 1 {
		t.Fatalf("first public forwards page = %+v", first)
	}
	second, err := r.onStatsGetMessagePublicForwards(requestCtx, &tg.StatsGetMessagePublicForwardsRequest{
		Channel: inputSource, MsgID: post.Message.ID, Offset: next, Limit: 1,
	})
	if err != nil {
		t.Fatalf("get second public forwards page: %v", err)
	}
	if second.Count != 2 || len(second.Forwards) != 1 {
		t.Fatalf("second public forwards page = %+v", second)
	}
}
