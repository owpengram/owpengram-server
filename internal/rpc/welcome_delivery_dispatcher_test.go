package rpc

import (
	"context"
	"testing"
	"time"

	"github.com/iamxvbaba/td/clock"
	"github.com/iamxvbaba/td/proto"
	"github.com/iamxvbaba/td/tg"
	"github.com/iamxvbaba/td/tlprofile"
	"go.uber.org/zap/zaptest"

	"telesrv/internal/domain"
)

type welcomeDeliveryTestStore struct {
	acked   []int64
	retried []int64
	next    time.Time
	reason  string
}

func (*welcomeDeliveryTestStore) ClaimWelcomeMessageDeliveries(context.Context, string, time.Time, int, time.Duration) ([]domain.WelcomeMessageDelivery, error) {
	return nil, nil
}
func (s *welcomeDeliveryTestStore) AckWelcomeMessageDeliveries(_ context.Context, _ string, ids []int64, _ time.Time) (int, error) {
	s.acked = append(s.acked, ids...)
	return len(ids), nil
}
func (s *welcomeDeliveryTestStore) RetryWelcomeMessageDeliveries(_ context.Context, _ string, ids []int64, next time.Time, reason string) (int, error) {
	s.retried = append(s.retried, ids...)
	s.next, s.reason = next, reason
	return len(ids), nil
}
func (*welcomeDeliveryTestStore) DeleteExpiredWelcomeMessageDeliveries(context.Context, time.Time, int) (int, error) {
	return 0, nil
}

type welcomeDeliveryTestChannels struct {
	ChannelsService
	view domain.ChannelView
}

func (s *welcomeDeliveryTestChannels) ResolveChannel(context.Context, int64, int64) (domain.ChannelView, error) {
	return s.view, nil
}

type welcomeDeliveryTestSessions struct {
	SessionBinder
	OnlineUserProvider
	online   bool
	sent     int
	semantic tlprofile.SemanticID
	updates  tg.UpdatesClass
}

func (s *welcomeDeliveryTestSessions) IsUserOnline(int64) bool { return s.online }
func (s *welcomeDeliveryTestSessions) PushToUserTransientCompatible(_ context.Context, _ int64, semantic tlprofile.SemanticID, _ proto.MessageType, updates tg.UpdatesClass, _ time.Duration) (int, error) {
	s.semantic, s.updates = semantic, updates
	return s.sent, nil
}
func (*welcomeDeliveryTestSessions) PushToUserAuthKeyTransientCompatible(context.Context, int64, [8]byte, tlprofile.SemanticID, proto.MessageType, tg.UpdatesClass, time.Duration) (int, error) {
	return 0, nil
}

func TestWelcomeDeliveryTargetsOnlyJoiningMemberAndAcksFirstCompatibleFanout(t *testing.T) {
	now := time.Now()
	delivery := domain.WelcomeMessageDelivery{
		ID: 11, JoinEventID: 12, ChannelID: 3001, TargetUserID: 2001,
		TemplateID: 3, EphemeralID: 77, JoinedAt: int(now.Unix()),
		Content:      domain.WelcomeMessageContent{Message: "welcome"},
		AttemptCount: 1, ExpiresAt: now.Add(time.Hour),
	}
	second := delivery
	second.ID = 13
	second.TemplateID = 4
	second.EphemeralID = 78
	second.Content.Message = "second welcome"
	channels := &welcomeDeliveryTestChannels{view: domain.ChannelView{
		Channel: domain.Channel{ID: delivery.ChannelID, AccessHash: 9, Title: "Group", Megagroup: true},
		Self:    domain.ChannelMember{ChannelID: delivery.ChannelID, UserID: delivery.TargetUserID, Status: domain.ChannelMemberActive, JoinedAt: delivery.JoinedAt},
	}}
	sessions := &welcomeDeliveryTestSessions{online: true, sent: 2}
	store := &welcomeDeliveryTestStore{}
	router := New(Config{OutboundPushTimeout: time.Second}, Deps{Channels: channels, Sessions: sessions}, zaptest.NewLogger(t), clock.System)
	dispatcher := NewWelcomeDeliveryDispatcher(router, store, zaptest.NewLogger(t))
	dispatcher.dispatchGroup(context.Background(), []domain.WelcomeMessageDelivery{delivery, second})

	if len(store.acked) != 2 || store.acked[0] != delivery.ID || store.acked[1] != second.ID || len(store.retried) != 0 {
		t.Fatalf("acked=%v retried=%v", store.acked, store.retried)
	}
	if sessions.semantic != tlprofile.SemanticTypeUpdateNewEphemeralMessage {
		t.Fatalf("semantic=%#x", sessions.semantic)
	}
	updates, ok := sessions.updates.(*tg.Updates)
	if !ok || updates.Seq != 0 || len(updates.Updates) != 2 || len(updates.Chats) != 1 || len(updates.Users) != 0 {
		t.Fatalf("updates=%#v", sessions.updates)
	}
	added, ok := updates.Updates[0].(*tg.UpdateNewEphemeralMessage)
	if !ok {
		t.Fatalf("update=%T", updates.Updates[0])
	}
	message := added.Message
	from, fromOK := message.FromID.(*tg.PeerChannel)
	peer, peerOK := message.PeerID.(*tg.PeerChannel)
	if message.ID != delivery.EphemeralID || message.Message != "welcome" || message.Out || message.WelcomeTemplate ||
		message.ReceiverID != 0 || !fromOK || !peerOK || from.ChannelID != delivery.ChannelID || peer.ChannelID != delivery.ChannelID {
		t.Fatalf("welcome message=%#v", message)
	}
	secondAdded, ok := updates.Updates[1].(*tg.UpdateNewEphemeralMessage)
	if !ok || secondAdded.Message.ID != second.EphemeralID || secondAdded.Message.Message != second.Content.Message {
		t.Fatalf("second update=%#v", updates.Updates[1])
	}
}

func TestWelcomeDeliveryRetriesOfflineAndDiscardsSupersededMembership(t *testing.T) {
	now := time.Now()
	delivery := domain.WelcomeMessageDelivery{
		ID: 21, JoinEventID: 22, ChannelID: 3001, TargetUserID: 2001,
		TemplateID: 1, EphemeralID: 88, JoinedAt: int(now.Unix()),
		Content:      domain.WelcomeMessageContent{Message: "welcome"},
		AttemptCount: 3, ExpiresAt: now.Add(time.Hour),
	}
	store := &welcomeDeliveryTestStore{}
	sessions := &welcomeDeliveryTestSessions{online: false, sent: 1}
	channels := &welcomeDeliveryTestChannels{view: domain.ChannelView{
		Channel: domain.Channel{ID: delivery.ChannelID, Megagroup: true},
		Self:    domain.ChannelMember{ChannelID: delivery.ChannelID, UserID: delivery.TargetUserID, Status: domain.ChannelMemberActive, JoinedAt: delivery.JoinedAt},
	}}
	router := New(Config{}, Deps{Channels: channels, Sessions: sessions}, zaptest.NewLogger(t), clock.System)
	dispatcher := NewWelcomeDeliveryDispatcher(router, store, zaptest.NewLogger(t))
	dispatcher.dispatchGroup(context.Background(), []domain.WelcomeMessageDelivery{delivery})
	if len(store.retried) != 1 || len(store.acked) != 0 || store.reason == "" || !store.next.After(now) {
		t.Fatalf("offline acked=%v retried=%v next=%v reason=%q", store.acked, store.retried, store.next, store.reason)
	}

	sessions.online = true
	channels.view.Self.JoinedAt++
	store.retried = nil
	dispatcher.dispatchGroup(context.Background(), []domain.WelcomeMessageDelivery{delivery})
	if len(store.acked) != 1 || len(store.retried) != 0 || sessions.updates != nil {
		t.Fatalf("superseded acked=%v retried=%v updates=%#v", store.acked, store.retried, sessions.updates)
	}
}
