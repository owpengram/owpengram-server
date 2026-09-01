package welcomemessages

import (
	"context"
	"errors"
	"testing"
	"time"

	"telesrv/internal/domain"
)

type welcomeStoreSpy struct {
	creates int
}

func (s *welcomeStoreSpy) CreateWelcomeMessage(_ context.Context, req domain.CreateWelcomeMessageRequest) (domain.WelcomeMessage, bool, error) {
	s.creates++
	return domain.WelcomeMessage{
		ID: 1, Peer: req.Peer, CreatorUserID: req.CreatorUserID, Date: req.Date,
		RandomID: req.RandomID, Content: req.Content, CreateFingerprint: req.CreateFingerprint, Version: 1,
	}, true, nil
}
func (*welcomeStoreSpy) EditWelcomeMessage(context.Context, domain.EditWelcomeMessageRequest) (domain.WelcomeMessage, error) {
	return domain.WelcomeMessage{}, nil
}
func (*welcomeStoreSpy) ListWelcomeMessages(context.Context, domain.Peer, int64) (domain.WelcomeMessageList, error) {
	return domain.WelcomeMessageList{Hash: 1}, nil
}
func (*welcomeStoreSpy) DeleteWelcomeMessage(context.Context, domain.Peer, int) (bool, error) {
	return true, nil
}
func (*welcomeStoreSpy) DeleteAllWelcomeMessages(context.Context, domain.Peer) (bool, error) {
	return true, nil
}
func (*welcomeStoreSpy) HasWelcomeMessages(context.Context, domain.Peer) (bool, error) {
	return true, nil
}

type welcomeChannelAccess struct {
	view domain.ChannelView
	err  error
}

func (a *welcomeChannelAccess) ResolveChannel(context.Context, int64, int64) (domain.ChannelView, error) {
	return a.view, a.err
}

func TestServiceRechecksManageWelcomeMessagesPermission(t *testing.T) {
	peer := domain.Peer{Type: domain.PeerTypeChannel, ID: 77}
	store := &welcomeStoreSpy{}
	channels := &welcomeChannelAccess{view: domain.ChannelView{
		Channel: domain.Channel{ID: peer.ID, Megagroup: true},
		Self: domain.ChannelMember{
			ChannelID: peer.ID, UserID: 9, Role: domain.ChannelRoleAdmin,
			Status:      domain.ChannelMemberActive,
			AdminRights: domain.ChannelAdminRights{ManageWelcomeMessages: true},
		},
	}}
	service := NewService(store, channels, WithClock(func() time.Time { return time.Unix(1700000000, 0) }))
	message, created, err := service.Create(context.Background(), 9, peer, 1001, domain.WelcomeMessageContent{Message: "hello"})
	if err != nil || !created || message.Date != 1700000000 || store.creates != 1 {
		t.Fatalf("authorized create = %+v created=%v calls=%d err=%v", message, created, store.creates, err)
	}

	channels.view.Self.Status = domain.ChannelMemberLeft
	if _, _, err := service.Create(context.Background(), 9, peer, 1002, domain.WelcomeMessageContent{Message: "blocked"}); !errors.Is(err, domain.ErrWelcomeMessageForbidden) || store.creates != 1 {
		t.Fatalf("inactive admin create err=%v calls=%d", err, store.creates)
	}

	channels.view.Self = domain.ChannelMember{UserID: 9, Role: domain.ChannelRoleCreator, Status: domain.ChannelMemberActive}
	if _, err := service.List(context.Background(), 9, peer, 0); err != nil {
		t.Fatalf("creator list: %v", err)
	}
	channels.view.Channel.Monoforum = true
	if _, err := service.List(context.Background(), 9, peer, 0); !errors.Is(err, domain.ErrWelcomeMessagePeerInvalid) {
		t.Fatalf("monoforum list err=%v", err)
	}
}

func TestServiceRejectsOrdinaryMemberAndAllowsJoinedBroadcastAdmin(t *testing.T) {
	peer := domain.Peer{Type: domain.PeerTypeChannel, ID: 88}
	store := &welcomeStoreSpy{}
	channels := &welcomeChannelAccess{view: domain.ChannelView{
		Channel: domain.Channel{ID: peer.ID, Broadcast: true},
		Self:    domain.ChannelMember{UserID: 10, Role: domain.ChannelRoleMember, Status: domain.ChannelMemberActive},
	}}
	service := NewService(store, channels)
	if _, err := service.DeleteAll(context.Background(), 10, peer); !errors.Is(err, domain.ErrWelcomeMessageForbidden) {
		t.Fatalf("ordinary member delete-all err=%v", err)
	}
	channels.view.Self.Role = domain.ChannelRoleAdmin
	channels.view.Self.AdminRights.ManageWelcomeMessages = true
	if ok, err := service.DeleteAll(context.Background(), 10, peer); err != nil || !ok {
		t.Fatalf("joined broadcast admin delete-all=%v,%v", ok, err)
	}
}
