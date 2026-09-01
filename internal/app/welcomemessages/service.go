package welcomemessages

import (
	"context"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

type ChannelAccess interface {
	ResolveChannel(ctx context.Context, userID, channelID int64) (domain.ChannelView, error)
}

type Option func(*Service)

func WithClock(now func() time.Time) Option {
	return func(s *Service) {
		if now != nil {
			s.now = now
		}
	}
}

type Service struct {
	messages store.WelcomeMessageStore
	channels ChannelAccess
	now      func() time.Time
}

func NewService(messages store.WelcomeMessageStore, channels ChannelAccess, options ...Option) *Service {
	s := &Service{messages: messages, channels: channels, now: time.Now}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	return s
}

// Authorize is the cheap gate RPC uses before resolving upload/media/rich
// content. Mutations call authorize again immediately before the store write so
// a concurrent demotion cannot turn this preflight into a stale capability.
func (s *Service) Authorize(ctx context.Context, userID int64, peer domain.Peer) error {
	return s.authorize(ctx, userID, peer)
}

func (s *Service) Create(ctx context.Context, userID int64, peer domain.Peer, randomID int64, content domain.WelcomeMessageContent) (domain.WelcomeMessage, bool, error) {
	if err := s.authorize(ctx, userID, peer); err != nil {
		return domain.WelcomeMessage{}, false, err
	}
	if err := content.Validate(); err != nil {
		return domain.WelcomeMessage{}, false, err
	}
	fingerprint, err := domain.WelcomeCreateFingerprint(peer, userID, randomID, content)
	if err != nil {
		return domain.WelcomeMessage{}, false, domain.ErrWelcomeMessageInvalid
	}
	return s.messages.CreateWelcomeMessage(ctx, domain.CreateWelcomeMessageRequest{
		Peer: peer, CreatorUserID: userID, Date: int(s.now().Unix()), RandomID: randomID,
		Content: content, CreateFingerprint: fingerprint,
	})
}

func (s *Service) Edit(ctx context.Context, userID int64, peer domain.Peer, id int, fields domain.WelcomeMessageEditFields) (domain.WelcomeMessage, error) {
	if err := s.authorize(ctx, userID, peer); err != nil {
		return domain.WelcomeMessage{}, err
	}
	return s.messages.EditWelcomeMessage(ctx, domain.EditWelcomeMessageRequest{
		Peer: peer, ID: id, EditDate: int(s.now().Unix()), Fields: fields,
	})
}

func (s *Service) List(ctx context.Context, userID int64, peer domain.Peer, hash int64) (domain.WelcomeMessageList, error) {
	if err := s.authorize(ctx, userID, peer); err != nil {
		return domain.WelcomeMessageList{}, err
	}
	return s.messages.ListWelcomeMessages(ctx, peer, hash)
}

func (s *Service) Delete(ctx context.Context, userID int64, peer domain.Peer, id int) (bool, error) {
	if err := s.authorize(ctx, userID, peer); err != nil {
		return false, err
	}
	return s.messages.DeleteWelcomeMessage(ctx, peer, id)
}

func (s *Service) DeleteAll(ctx context.Context, userID int64, peer domain.Peer) (bool, error) {
	if err := s.authorize(ctx, userID, peer); err != nil {
		return false, err
	}
	return s.messages.DeleteAllWelcomeMessages(ctx, peer)
}

// HasAny is used only after the ordinary full-chat access check has succeeded.
// It deliberately does not require manage_welcome_messages so non-admin members
// receive the same has_welcome_messages projection as official clients.
func (s *Service) HasAny(ctx context.Context, peer domain.Peer) (bool, error) {
	if s == nil || s.messages == nil || peer.Type != domain.PeerTypeChannel || peer.ID <= 0 {
		return false, domain.ErrWelcomeMessageInvalid
	}
	return s.messages.HasWelcomeMessages(ctx, peer)
}

func (s *Service) authorize(ctx context.Context, userID int64, peer domain.Peer) error {
	if s == nil || s.messages == nil || s.channels == nil || userID <= 0 ||
		peer.Type != domain.PeerTypeChannel || peer.ID <= 0 {
		return domain.ErrWelcomeMessageInvalid
	}
	view, err := s.channels.ResolveChannel(ctx, userID, peer.ID)
	if err != nil {
		return err
	}
	if view.Channel.Monoforum {
		return domain.ErrWelcomeMessagePeerInvalid
	}
	if !view.Self.CanManageWelcomeMessages() {
		return domain.ErrWelcomeMessageForbidden
	}
	return nil
}
