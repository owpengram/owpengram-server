package rpc

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"sync/atomic"

	"github.com/owpengram/owpengram-td/tg"

	"telesrv/internal/domain"
)

var privateNoForwardsRandomFallback atomic.Uint64

func (r *Router) onMessagesToggleNoForwards(ctx context.Context, req *tg.MessagesToggleNoForwardsRequest) (tg.UpdatesClass, error) {
	if req == nil {
		return nil, inputRequestInvalidErr()
	}
	userID, _, err := r.currentUserID(ctx)
	if err != nil {
		return nil, internalErr()
	}
	peer, err := r.checkedDomainPeerFromInputPeer(ctx, userID, req.Peer)
	if err != nil {
		return nil, err
	}
	if peer.Type == domain.PeerTypeChannel {
		if r.deps.Channels == nil {
			return nil, notImplementedErr()
		}
		channel, err := r.deps.Channels.SetNoForwards(ctx, userID, peer.ID, req.Enabled)
		if err != nil {
			return nil, channelAdminErr(err)
		}
		return r.channelStateMutationUpdates(ctx, userID, channel), nil
	}
	input, ok := req.Peer.(*tg.InputPeerUser)
	if !ok || input == nil || peer.Type != domain.PeerTypeUser || peer.ID == 0 || peer.ID == userID ||
		r.deps.Users == nil {
		return nil, peerIDInvalidErr()
	}
	if err := r.validateInputUser(ctx, &tg.InputUser{UserID: input.UserID, AccessHash: input.AccessHash}); err != nil {
		return nil, peerIDInvalidErr()
	}
	target, found, err := r.deps.Users.ByID(ctx, userID, peer.ID)
	if err != nil {
		return nil, internalErr()
	}
	if !found || target.Bot || target.Support || target.Deleted {
		return nil, peerIDInvalidErr()
	}
	self, err := r.deps.Users.Self(ctx, userID)
	if err != nil {
		return nil, internalErr()
	}
	if self.Bot || self.Support || self.Deleted {
		return nil, peerIDInvalidErr()
	}
	svc, ok := r.deps.Messages.(PrivateNoForwardsService)
	if !ok {
		return nil, notImplementedErr()
	}
	requestMsgID, hasRequestMsgID := req.GetRequestMsgID()
	if hasRequestMsgID && (requestMsgID <= 0 || requestMsgID > domain.MaxMessageBoxID) {
		return nil, requestMsgExpiredErr()
	}
	if !hasRequestMsgID {
		requestMsgID = 0
	}
	current, err := svc.GetPrivateNoForwards(ctx, userID, peer.ID)
	if err != nil {
		return nil, privateNoForwardsErr(err)
	}
	// Premium is required only to create a new protected state. Disabling,
	// answering a request and no-op retries remain possible after expiry.
	if req.Enabled && requestMsgID == 0 && !current.Enabled() && !self.PremiumActiveAt(r.clock.Now().Unix()) {
		return nil, premiumAccountRequiredErr()
	}
	if requestMsgID != 0 || current.Enabled() != req.Enabled {
		if err := r.checkSendRateLimit(ctx, userID, 1); err != nil {
			return nil, err
		}
	}
	sessionID, _ := SessionIDFrom(ctx)
	result, err := svc.TogglePrivateNoForwards(ctx, userID, domain.TogglePrivateNoForwardsRequest{
		ActorUserID:     userID,
		PeerUserID:      peer.ID,
		Enabled:         req.Enabled,
		RequestMsgID:    requestMsgID,
		RandomID:        newPrivateNoForwardsRandomID(),
		Date:            int(r.clock.Now().Unix()),
		OriginAuthKeyID: rawAuthKeyIDForOrigin(ctx),
		OriginSessionID: sessionID,
	})
	if err != nil {
		return nil, privateNoForwardsErr(err)
	}
	if result.Changed {
		r.invalidateRPCProjectionForPeer(userID, peer)
		r.invalidateRPCProjectionForPeer(peer.ID, domain.Peer{Type: domain.PeerTypeUser, ID: userID})
	}
	if !result.Changed || result.Send.SenderMessage.ID == 0 {
		return tgEmptyUpdates(int(r.clock.Now().Unix())), nil
	}
	return tgPrivateMessageUpdates(
		result.Send.SenderEvent,
		result.Send.SenderMessage,
		0,
		false,
		r.usersForMessageUpdate(ctx, userID, result.Send.SenderMessage),
		[]tg.ChatClass{},
	), nil
}

func privateNoForwardsErr(err error) error {
	switch {
	case errors.Is(err, domain.ErrNoForwardsRequestExpired),
		errors.Is(err, domain.ErrReplyMessageIDInvalid):
		return requestMsgExpiredErr()
	case errors.Is(err, domain.ErrMessageIDInvalid):
		return peerIDInvalidErr()
	case errors.Is(err, domain.ErrChatForwardsRestricted):
		return chatForwardsRestrictedErr()
	case errors.Is(err, domain.ErrUserFrozen):
		return frozenMethodInvalidErr()
	case errors.Is(err, domain.ErrMessageRandomIDDuplicate):
		return randomIDDuplicateErr()
	default:
		return internalErr()
	}
}

func newPrivateNoForwardsRandomID() int64 {
	var raw [8]byte
	if _, err := cryptorand.Read(raw[:]); err == nil {
		if value := int64(binary.LittleEndian.Uint64(raw[:])); value != 0 {
			return value
		}
	}
	value := privateNoForwardsRandomFallback.Add(1)
	return int64(value | 1<<62)
}
