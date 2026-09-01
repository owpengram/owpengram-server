package rpc

import (
	"context"
	"testing"

	"github.com/iamxvbaba/td/clock"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/domain"
)

func TestRemoveKnownChannelRefs(t *testing.T) {
	refs := map[int64]struct{}{
		1001: {},
		1002: {},
		1003: {},
	}

	removeKnownChannelRefs(refs, []domain.Channel{
		{ID: 1002},
		{ID: 0},
		{ID: 1004},
	})

	if _, ok := refs[1002]; ok {
		t.Fatalf("known channel ref was not removed: %+v", refs)
	}
	for _, id := range []int64{1001, 1003} {
		if _, ok := refs[id]; !ok {
			t.Fatalf("unexpectedly removed channel %d from refs %+v", id, refs)
		}
	}
}
func TestCollectChannelMessagePeerRefsIncludesNestedWireUsers(t *testing.T) {
	const currentChannelID = int64(3001)
	users := map[int64]struct{}{}
	channels := map[int64]struct{}{}
	collectChannelMessagePeerRefs(domain.ChannelMessage{
		SavedPeer: domain.Peer{Type: domain.PeerTypeUser, ID: 1005},
		Forward: &domain.MessageForward{
			From:      domain.Peer{Type: domain.PeerTypeUser, ID: 1001},
			SavedFrom: domain.Peer{Type: domain.PeerTypeUser, ID: 1002},
		},
		Replies: &domain.ChannelMessageReplies{RecentRepliers: []domain.Peer{
			{Type: domain.PeerTypeUser, ID: 1003},
			{Type: domain.PeerTypeChannel, ID: 4001},
			{Type: domain.PeerTypeChannel, ID: currentChannelID},
		}},
		Action: &domain.ChannelMessageAction{InviterUserID: 1004},
	}, currentChannelID, users, channels)

	for _, id := range []int64{1001, 1002, 1003, 1004, 1005} {
		if _, ok := users[id]; !ok {
			t.Fatalf("channel message user refs=%v, missing %d", users, id)
		}
	}
	if _, ok := channels[4001]; !ok {
		t.Fatalf("channel message channel refs=%v, missing recent replier channel", channels)
	}
	if _, ok := channels[currentChannelID]; ok {
		t.Fatalf("current channel leaked into external refs=%v", channels)
	}
}

func TestMessageMentionNameUsersAreProjectedInStrictAndNonStrictEnvelopes(t *testing.T) {
	const (
		viewerID   = int64(1001)
		entityUser = int64(2001)
		quoteUser  = int64(2002)
		channelID  = int64(3001)
	)
	entities := []domain.MessageEntity{{
		Type:   domain.MessageEntityMentionName,
		UserID: entityUser,
	}}
	reply := &domain.MessageReply{
		MessageID: 1,
		QuoteEntities: []domain.MessageEntity{{
			Type:   domain.MessageEntityMentionName,
			UserID: quoteUser,
		}},
	}
	users := mapUsersService{users: map[int64]domain.User{
		entityUser: {ID: entityUser, FirstName: "Projected entity mention"},
		quoteUser:  {ID: quoteUser, FirstName: "Projected quote mention"},
	}}
	r := New(Config{}, Deps{Users: users}, zaptest.NewLogger(t), clock.System)
	ctx := context.Background()

	tests := []struct {
		name   string
		enrich func() ([]domain.User, error)
	}{
		{
			name: "private non-strict",
			enrich: func() ([]domain.User, error) {
				list := r.enrichMessageList(ctx, viewerID, domain.MessageList{Messages: []domain.Message{{
					Entities: entities,
					ReplyTo:  reply,
				}}})
				return list.Users, nil
			},
		},
		{
			name: "private strict",
			enrich: func() ([]domain.User, error) {
				events, err := r.enrichUpdateEventsWithPeerCacheStrict(ctx, viewerID, []domain.UpdateEvent{{
					Type: domain.UpdateEventNewMessage,
					Message: domain.Message{
						Entities: entities,
						ReplyTo:  reply,
					},
				}}, nil)
				if err != nil {
					return nil, err
				}
				return events[0].Users, nil
			},
		},
		{
			name: "channel non-strict",
			enrich: func() ([]domain.User, error) {
				history := r.enrichChannelHistory(ctx, viewerID, domain.ChannelHistory{
					Channel: domain.Channel{ID: channelID},
					Messages: []domain.ChannelMessage{{
						ChannelID: channelID,
						Entities:  entities,
						ReplyTo:   reply,
					}},
				})
				return history.Users, nil
			},
		},
		{
			name: "channel strict",
			enrich: func() ([]domain.User, error) {
				diff, err := r.enrichChannelDifferenceStrict(ctx, viewerID, domain.ChannelDifference{
					Channel: domain.Channel{ID: channelID},
					NewMessages: []domain.ChannelMessage{{
						ChannelID: channelID,
						Entities:  entities,
						ReplyTo:   reply,
					}},
				})
				return diff.Users, err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.enrich()
			if err != nil {
				t.Fatalf("enrich: %v", err)
			}
			byID := make(map[int64]domain.User, len(got))
			for _, user := range got {
				byID[user.ID] = user
			}
			if user := byID[entityUser]; user.FirstName != "Projected entity mention" {
				t.Fatalf("entity mention user = %+v, want viewer projection", user)
			}
			if user := byID[quoteUser]; user.FirstName != "Projected quote mention" {
				t.Fatalf("quote mention user = %+v, want viewer projection", user)
			}
		})
	}
}
