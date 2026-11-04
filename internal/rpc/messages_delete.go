package rpc

import (
	"context"
	"github.com/owpengram/owpengram-td/tg"
	"telesrv/internal/domain"
)

func (r *Router) onMessagesDeleteMessages(ctx context.Context, req *tg.MessagesDeleteMessagesRequest) (*tg.MessagesAffectedMessages, error) {
	authKeyID, _ := AuthKeyIDFrom(ctx)
	userID, _, err := r.currentUserID(ctx)
	if err != nil {
		return nil, internalErr()
	}
	if len(req.ID) == 0 || r.deps.Messages == nil {
		return r.affectedMessages(ctx, authKeyID, userID)
	}
	if len(req.ID) > domain.MaxDeleteMessageIDs {
		return nil, limitInvalidErr()
	}
	// 官方对私聊 revoke 不做 block 限制：被对方拉黑后撤回自己的消息仍
	// 双向生效；客户端删除请求的 fail handler 是静默的，服务端报错只会
	// 造成"本地已删、服务端未删"的复活错乱。
	sessionID, _ := SessionIDFrom(ctx)
	res, err := r.deps.Messages.DeleteMessages(ctx, userID, domain.DeleteMessagesRequest{
		OwnerUserID:     userID,
		IDs:             req.ID,
		Revoke:          req.GetRevoke(),
		Date:            int(r.clock.Now().Unix()),
		OriginAuthKeyID: rawAuthKeyIDForOrigin(ctx),
		OriginSessionID: sessionID,
	})
	if err != nil {
		return nil, internalErr()
	}
	self := res.Self()
	pts, ptsCount := self.AffectedPts()
	if len(self.MessageIDs) == 0 || pts == 0 {
		return r.affectedMessages(ctx, authKeyID, userID)
	}
	return &tg.MessagesAffectedMessages{Pts: pts, PtsCount: ptsCount}, nil
}

func (r *Router) onMessagesDeleteHistory(ctx context.Context, req *tg.MessagesDeleteHistoryRequest) (*tg.MessagesAffectedHistory, error) {
	authKeyID, _ := AuthKeyIDFrom(ctx)
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
			return r.affectedHistory(ctx, authKeyID, userID, 0)
		}
		if req.MaxID < 0 || req.MaxID > domain.MaxMessageBoxID {
			return nil, messageIDInvalidErr()
		}
		res, err := r.deps.Channels.DeleteHistory(ctx, userID, domain.DeleteChannelHistoryRequest{
			UserID:      userID,
			ChannelID:   peer.ID,
			MaxID:       req.MaxID,
			ForEveryone: req.GetRevoke(),
			Date:        int(r.clock.Now().Unix()),
		})
		if err != nil {
			return nil, channelDeleteErr(err)
		}
		if res.Event.Pts != 0 {
			r.enqueueChannelFanout(ctx, channelFanoutMessageBox, userID, res.Channel.ID, res.Event.Pts, res.Recipients, func(_ context.Context, viewerUserID int64) *tg.Updates {
				return r.channelDeleteMessagesUpdates(viewerUserID, res.Channel, res.Event)
			})
			return &tg.MessagesAffectedHistory{Pts: res.Event.Pts, PtsCount: res.Event.PtsCount, Offset: res.Offset}, nil
		}
		if res.AvailableMinChanged && res.AvailableMinID > 0 {
			updates := r.channelAvailableMessagesUpdates(userID, res.Channel, res.AvailableMinID)
			r.pushUserUpdates(ctx, userID, updates)
		}
		// messages.affectedHistory.pts is the caller's account-state snapshot.
		// The local channel clear itself consumes no account/channel pts.
		return r.affectedHistory(ctx, authKeyID, userID, res.Offset)
	}
	if peer.Type != domain.PeerTypeUser {
		return nil, peerIDInvalidErr()
	}
	if r.deps.Messages == nil {
		return r.affectedHistory(ctx, authKeyID, userID, 0)
	}
	sessionID, _ := SessionIDFrom(ctx)
	minDate, _ := req.GetMinDate()
	maxDate, _ := req.GetMaxDate()
	res, err := r.deps.Messages.DeleteHistory(ctx, userID, domain.DeleteHistoryRequest{
		OwnerUserID:     userID,
		Peer:            peer,
		MaxID:           req.MaxID,
		MinDate:         minDate,
		MaxDate:         maxDate,
		JustClear:       req.GetJustClear(),
		Revoke:          req.GetRevoke(),
		Date:            int(r.clock.Now().Unix()),
		OriginAuthKeyID: rawAuthKeyIDForOrigin(ctx),
		OriginSessionID: sessionID,
	})
	if err != nil {
		return nil, internalErr()
	}
	self := res.Self()
	pts, ptsCount := self.AffectedPts()
	if pts == 0 {
		return r.affectedHistory(ctx, authKeyID, userID, 0)
	}
	return &tg.MessagesAffectedHistory{
		Pts:      pts,
		PtsCount: ptsCount,
		Offset:   res.Offset,
	}, nil
}
