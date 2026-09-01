package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap/zaptest"

	appchannels "telesrv/internal/app/channels"
	appusers "telesrv/internal/app/users"
	"telesrv/internal/domain"
	"telesrv/internal/store/memory"
)

func TestChannelsCreateChannelResponseCarriesTdlibMessageMappingOnlyForCaller(t *testing.T) {
	ctx := context.Background()
	userStore := memory.NewUserStore()
	owner, err := userStore.Create(ctx, domain.User{
		AccessHash: 88001,
		Phone:      "15550088001",
		FirstName:  "Owner",
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	sessions := &captureSessions{onlineUserIDs: []int64{owner.ID}}
	r := New(Config{}, Deps{
		Users:    appusers.NewService(userStore),
		Channels: appchannels.NewService(memory.NewChannelStore()),
		Sessions: sessions,
	}, zaptest.NewLogger(t), clock.System)

	tests := []struct {
		name string
		req  *tg.ChannelsCreateChannelRequest
	}{
		{name: "broadcast", req: &tg.ChannelsCreateChannelRequest{Title: "TDLib broadcast", Broadcast: true}},
		{name: "megagroup", req: &tg.ChannelsCreateChannelRequest{Title: "TDLib group", Megagroup: true}},
		{name: "forum", req: &tg.ChannelsCreateChannelRequest{Title: "TDLib forum", Megagroup: true, Forum: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sessions.clearMessages()
			created, err := r.onChannelsCreateChannel(WithUserID(ctx, owner.ID), test.req)
			if err != nil {
				t.Fatalf("create channel: %v", err)
			}
			updates, ok := created.(*tg.Updates)
			if !ok || len(updates.Updates) != 3 {
				t.Fatalf("response = %T %+v, want mapping, create message, and channel refresh", created, created)
			}
			mapping, ok := updates.Updates[0].(*tg.UpdateMessageID)
			if !ok || mapping.ID <= 0 || mapping.RandomID == 0 {
				t.Fatalf("mapping = %#v, want positive message id and non-zero random id", updates.Updates[0])
			}
			create, ok := updates.Updates[1].(*tg.UpdateNewChannelMessage)
			if !ok || create.Pts != domain.FirstChannelEventPts || create.PtsCount != 1 {
				t.Fatalf("create update = %#v, want pts=2 pts_count=1", updates.Updates[1])
			}
			service, ok := create.Message.(*tg.MessageService)
			if !ok || service.ID != mapping.ID {
				t.Fatalf("create service = %#v, want mapped id %d", create.Message, mapping.ID)
			}
			if _, ok := service.Action.(*tg.MessageActionChannelCreate); !ok {
				t.Fatalf("create action = %T, want messageActionChannelCreate", service.Action)
			}
			if refresh, ok := updates.Updates[2].(*tg.UpdateChannel); !ok || refresh.ChannelID == 0 {
				t.Fatalf("refresh = %#v, want updateChannel", updates.Updates[2])
			}

			pushed, ok := sessions.lastUserPush().(*tg.Updates)
			if !ok || len(pushed.Updates) != 2 {
				t.Fatalf("fan-out = %T %+v, want create message and channel refresh only", sessions.lastUserPush(), sessions.lastUserPush())
			}
			for _, update := range pushed.Updates {
				if _, ok := update.(*tg.UpdateMessageID); ok {
					t.Fatalf("response-only updateMessageID leaked into fan-out: %+v", pushed.Updates)
				}
			}
		})
	}
}

func TestChannelCreationResponseRejectsNonCreationResult(t *testing.T) {
	r := New(Config{}, Deps{}, zaptest.NewLogger(t), clock.System)
	for _, result := range []domain.CreateChannelResult{
		{},
		{Message: domain.ChannelMessage{ID: 1}},
		{Message: domain.ChannelMessage{ID: 1, Action: &domain.ChannelMessageAction{Type: domain.ChannelActionChatAddUser}}},
	} {
		if updates, err := r.channelCreationResponseUpdates(context.Background(), 1, result); err == nil || updates != nil {
			t.Fatalf("invalid creation result = %+v produced updates=%+v err=%v", result, updates, err)
		}
	}
}
