package rpc

import (
	"context"
	"fmt"

	"github.com/iamxvbaba/td/proto"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap"

	"telesrv/internal/domain"
)

const encryptedDifferencePageSize = 1000

// 私聊密聊 qts 消息收发 RPC handler（P1）。服务端是盲中继：sendEncrypted* 的 bytes 是
// 客户端加密的 DecryptedMessage，服务端盲存进【接收方设备】的 qts 队列、原样转发，
// 永不解密。在线推 updateNewEncryptedMessage（设备定向，离线靠 getDifference 补回）。
// 设计见 docs/secret-chat-module.md §7/§8。

// pushEncryptedNewMessage 把 updateNewEncryptedMessage 定向投递给接收设备。
func (r *Router) pushEncryptedNewMessage(ctx context.Context, msg domain.SecretChatMessage) {
	if msg.ReceiverUserID == 0 || msg.ReceiverAuthKeyID == 0 {
		return
	}
	now := int(r.clock.Now().Unix())
	upd := &tg.Updates{
		Updates: []tg.UpdateClass{&tg.UpdateNewEncryptedMessage{
			Message: tgEncryptedMessage(msg),
			Qts:     msg.Qts,
		}},
		Users: []tg.UserClass{},
		Chats: []tg.ChatClass{},
		Date:  now,
		Seq:   0,
	}
	if targeted, ok := r.deps.Sessions.(AuthKeyTargetedSessionBinder); ok {
		_, _ = targeted.PushToUserAuthKey(ctx, msg.ReceiverUserID, deviceAuthKeyBytes(msg.ReceiverAuthKeyID), proto.MessageFromServer, upd)
		return
	}
	// 设备隔离是安全边界；缺少定向能力时只保留 durable qts，离线 difference 补回。
	r.log.Error("secret chat targeted session binder unavailable",
		zap.Int64("target_user_id", msg.ReceiverUserID),
		zap.Int64("target_auth_key_id", msg.ReceiverAuthKeyID))
}

func (r *Router) sendEncryptedCommon(ctx context.Context, peer tg.InputEncryptedChat, randomID int64, data []byte, isService bool) (tg.MessagesSentEncryptedMessageClass, error) {
	if len(data) > domain.MaxSecretMessageDataBytes {
		return nil, dataTooLongErr()
	}
	if r.deps.SecretChats == nil {
		return nil, notImplementedErr()
	}
	userID, err := r.secretChatRequireUser(ctx)
	if err != nil {
		return nil, err
	}
	deviceAuthKeyID, ok := businessAuthKeyIDFrom(ctx)
	if !ok {
		return nil, internalErr()
	}
	_, stored, err := r.deps.SecretChats.SendEncrypted(ctx, peer.ChatID, userID, deviceAuthKeyID, peer.AccessHash, domain.SecretMessageDelivery{
		RandomID:  randomID,
		Bytes:     data,
		IsService: isService,
		Date:      int(r.clock.Now().Unix()),
	})
	if err != nil {
		return nil, secretChatErr(err)
	}
	r.pushEncryptedNewMessage(ctx, stored)
	// 无 server message id；幂等重发返回首次落库 date（store dedup 保证）。
	return &tg.MessagesSentEncryptedMessage{Date: stored.Date}, nil
}

func (r *Router) onMessagesSendEncrypted(ctx context.Context, req *tg.MessagesSendEncryptedRequest) (tg.MessagesSentEncryptedMessageClass, error) {
	if req == nil {
		return nil, inputRequestInvalidErr()
	}
	return r.sendEncryptedCommon(ctx, req.Peer, req.RandomID, req.Data, false)
}

func (r *Router) onMessagesSendEncryptedService(ctx context.Context, req *tg.MessagesSendEncryptedServiceRequest) (tg.MessagesSentEncryptedMessageClass, error) {
	if req == nil {
		return nil, inputRequestInvalidErr()
	}
	return r.sendEncryptedCommon(ctx, req.Peer, req.RandomID, req.Data, true)
}

// onMessagesReceivedQueue 确认接收设备已处理到 max_qts（推进 confirmed + 标 acked 可 GC）。
// 返回空 Vector<long>：DrKLO 调用处忽略响应（sendRequest(req, null)），空集安全。
func (r *Router) onMessagesReceivedQueue(ctx context.Context, maxQts int) ([]int64, error) {
	if r.deps.SecretChats == nil {
		return nil, notImplementedErr()
	}
	if _, err := r.secretChatRequireUser(ctx); err != nil {
		return nil, err
	}
	deviceKey, ok := businessAuthKeyIDFrom(ctx)
	if !ok {
		return nil, internalErr()
	}
	if err := r.deps.SecretChats.AckQueue(ctx, deviceKey, maxQts); err != nil {
		return nil, internalErr()
	}
	return []int64{}, nil
}

// onMessagesReportEncryptedSpam persists an immutable chat-metadata snapshot;
// the server remains unable to inspect encrypted message plaintext. Reporting
// does not discard or block the chat and emits no update.
func (r *Router) onMessagesReportEncryptedSpam(ctx context.Context, peer tg.InputEncryptedChat) (bool, error) {
	if r.deps.SecretChats == nil || r.deps.Moderation == nil {
		return false, notImplementedErr()
	}
	userID, err := r.secretChatRequireUser(ctx)
	if err != nil {
		return false, err
	}
	chat, _, _, err := r.resolveSecretChatPeer(ctx, userID, peer)
	if err != nil {
		return false, err
	}
	if _, _, err := r.deps.Moderation.ReportEncryptedSpam(ctx, userID, chat, r.clock.Now()); err != nil {
		return false, moderationReportError(err)
	}
	return true, nil
}

// deviceEncryptedQts 返回当前设备已分配的最高 qts（getState 注入用，无则 0）。
func (r *Router) deviceEncryptedQts(ctx context.Context) int {
	if r.deps.SecretChats == nil {
		return 0
	}
	deviceKey, ok := businessAuthKeyIDFrom(ctx)
	if !ok {
		return 0
	}
	qts, err := r.deps.SecretChats.DeviceReservedQts(ctx, deviceKey)
	if err != nil {
		return 0
	}
	return qts
}

// encryptedDifference 返回当前设备 qts > sinceQts 的连续前缀、推进后的 qts 与是否还有
// 下一页。存储错误或 qts gap 必须 fail-fast，禁止越过缺口推进客户端水位。
func (r *Router) encryptedDifference(ctx context.Context, sinceQts int) ([]tg.EncryptedMessageClass, int, bool, error) {
	if r.deps.SecretChats == nil {
		return nil, sinceQts, false, nil
	}
	deviceKey, ok := businessAuthKeyIDFrom(ctx)
	if !ok {
		return nil, sinceQts, false, nil
	}
	msgs, err := r.deps.SecretChats.ListNewMessages(ctx, deviceKey, sinceQts, encryptedDifferencePageSize+1)
	if err != nil {
		return nil, sinceQts, false, err
	}
	if len(msgs) == 0 {
		return nil, sinceQts, false, nil
	}
	partial := len(msgs) > encryptedDifferencePageSize
	if partial {
		msgs = msgs[:encryptedDifferencePageSize]
	}
	out := make([]tg.EncryptedMessageClass, 0, len(msgs))
	newQts := sinceQts
	for i, m := range msgs {
		expected := newQts + 1
		if m.Qts != expected {
			return nil, sinceQts, false, fmt.Errorf("secret chat qts gap at index %d: got %d want %d", i, m.Qts, expected)
		}
		out = append(out, tgEncryptedMessage(m))
		newQts = m.Qts
	}
	return out, newQts, partial, nil
}

// injectEncryptedMessages 把加密消息与推进后的 qts 注入差分响应（按类型分别写 State /
// IntermediateState 的 Qts）。
func injectEncryptedMessages(diff tg.UpdatesDifferenceClass, encMsgs []tg.EncryptedMessageClass, newQts int) tg.UpdatesDifferenceClass {
	switch v := diff.(type) {
	case *tg.UpdatesDifference:
		v.NewEncryptedMessages = append(v.NewEncryptedMessages, encMsgs...)
		v.State.Qts = newQts
	case *tg.UpdatesDifferenceSlice:
		v.NewEncryptedMessages = append(v.NewEncryptedMessages, encMsgs...)
		v.IntermediateState.Qts = newQts
	}
	return diff
}

// encryptedStateUpdates 返回当前设备未投递的握手/已读状态事件重建出的 update（进
// OtherUpdates）、涉及的 peer user id（补 Users）、以及要登记已投递的事件 id。
// encryption 事件按 secret_chats 权威态重建（不固化密钥材料快照）。账号级邀请在
// accept 后只对未绑定设备投影为 discarded；获胜设备消费并跳过，绝不能收到 normal 泄漏。
func (r *Router) encryptedStateUpdates(ctx context.Context, userID int64) (updates []tg.UpdateClass, peerUserIDs []int64, eventIDs []int64, partial bool, err error) {
	if r.deps.SecretChats == nil {
		return nil, nil, nil, false, nil
	}
	deviceKey, ok := businessAuthKeyIDFrom(ctx)
	if !ok {
		return nil, nil, nil, false, nil
	}
	events, err := r.deps.SecretChats.ListStateEvents(ctx, userID, deviceKey, encryptedDifferencePageSize+1)
	if err != nil {
		return nil, nil, nil, false, err
	}
	if len(events) == 0 {
		return nil, nil, nil, false, nil
	}
	partial = len(events) > encryptedDifferencePageSize
	if partial {
		events = events[:encryptedDifferencePageSize]
	}
	seenEncryption := make(map[int]struct{})
	for _, ev := range events {
		switch ev.Type {
		case domain.EncryptedStateEventEncryption:
			chat, found, gerr := r.deps.SecretChats.GetSecretChat(ctx, ev.ChatID)
			if gerr != nil || !found {
				continue
			}
			eventIDs = append(eventIDs, ev.ID)
			if _, duplicate := seenEncryption[chat.ID]; duplicate {
				continue
			}
			seenEncryption[chat.ID] = struct{}{}

			chatView := tgEncryptedChatForViewer(chat, userID)
			if ev.TargetAuthKeyID == 0 && chat.State == domain.SecretChatStateNormal {
				// 账号级事件只承载 accept 前邀请。accept 后获胜设备已有同步响应；其它设备
				// 必须收敛为 discarded，不能用当前 normal 权威态泄漏 access_hash/g_a。
				if chat.AuthKeyOf(userID) == deviceKey {
					continue
				}
				chatView = &tg.EncryptedChatDiscarded{ID: chat.ID, HistoryDeleted: true}
			}
			updates = append(updates, &tg.UpdateEncryption{Chat: chatView, Date: ev.Date})
			peerUserIDs = append(peerUserIDs, chat.AdminUserID, chat.ParticipantUserID)
		case domain.EncryptedStateEventRead:
			updates = append(updates, &tg.UpdateEncryptedMessagesRead{
				ChatID:  ev.ChatID,
				MaxDate: ev.MaxDate,
				Date:    ev.Date,
			})
			eventIDs = append(eventIDs, ev.ID)
		}
	}
	return updates, peerUserIDs, eventIDs, partial, nil
}

// injectEncryptedOtherUpdates 把握手/已读 update 追加进差分的 OtherUpdates、把 peer
// user 对象追加进 Users。
func (r *Router) injectEncryptedOtherUpdates(ctx context.Context, viewerUserID int64, diff tg.UpdatesDifferenceClass, updates []tg.UpdateClass, peerUserIDs []int64) tg.UpdatesDifferenceClass {
	if len(updates) == 0 {
		return diff
	}
	users := r.tgUsersForIDs(ctx, viewerUserID, peerUserIDs)
	return appendEncryptedOtherUpdates(diff, updates, users)
}

func (r *Router) injectEncryptedOtherUpdatesStrict(ctx context.Context, viewerUserID int64, diff tg.UpdatesDifferenceClass, updates []tg.UpdateClass, peerUserIDs []int64, cache *viewerPeerCache) (tg.UpdatesDifferenceClass, error) {
	if len(updates) == 0 {
		return diff, nil
	}
	if cache == nil {
		cache = newViewerPeerCache(r)
	}
	users, err := cache.usersForIDsStrict(ctx, viewerUserID, peerUserIDs)
	if err != nil {
		return nil, err
	}
	return appendEncryptedOtherUpdates(diff, updates, r.tgUsersForViewer(viewerUserID, users)), nil
}

func appendEncryptedOtherUpdates(diff tg.UpdatesDifferenceClass, updates []tg.UpdateClass, users []tg.UserClass) tg.UpdatesDifferenceClass {
	switch v := diff.(type) {
	case *tg.UpdatesDifference:
		v.OtherUpdates = append(v.OtherUpdates, updates...)
		v.Users = appendUniqueTGUsers(v.Users, users...)
	case *tg.UpdatesDifferenceSlice:
		v.OtherUpdates = append(v.OtherUpdates, updates...)
		v.Users = appendUniqueTGUsers(v.Users, users...)
	}
	return diff
}
