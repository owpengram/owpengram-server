package rpc

import (
	"context"

	"github.com/iamxvbaba/td/proto"
	"github.com/iamxvbaba/td/tg"
	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// 通话信令推送全部走无 pts 直推（pushUserMessage）：信令有效期秒级，离线设备
// 重连后经 getDifference 补收一条早已失效的来电毫无意义甚至有害。唯一带 pts 的
// 产物是结束后的 messageActionPhoneCall 服务消息（P2，走 outbox 补偿离线设备）。

// phoneCallUpdates 把 viewer 视角的 phoneCall 状态包成可直推的 Updates 容器。
func (r *Router) phoneCallUpdates(ctx context.Context, call domain.PhoneCall, viewerID int64) *tg.Updates {
	return r.phoneCallUpdatesWith(ctx, tgPhoneCallForViewer(call, viewerID), call, viewerID)
}

func (r *Router) phoneCallUpdatesWith(ctx context.Context, view tg.PhoneCallClass, call domain.PhoneCall, viewerID int64) *tg.Updates {
	return &tg.Updates{
		Updates: []tg.UpdateClass{&tg.UpdatePhoneCall{PhoneCall: view}},
		Users:   r.tgUsersForIDs(ctx, viewerID, []int64{call.AdminID, call.ParticipantID}),
		Chats:   []tg.ChatClass{},
		Date:    int(r.clock.Now().Unix()),
		Seq:     0,
	}
}

// pushPhoneCall 把 call 当前状态按 targetUserID 视角推给其全部在线设备
// （ctx 携带的发起设备会被 pushUserMessage 的 except 语义排除）。
func (r *Router) pushPhoneCall(ctx context.Context, targetUserID int64, call domain.PhoneCall, logMessage string) int {
	return r.pushUserMessage(ctx, targetUserID, logMessage, r.phoneCallUpdates(ctx, call, targetUserID))
}

// pushPhoneCallStopRinging 向被叫【其它设备】推合成 phoneCallDiscarded 停振铃（P0-1 修正）。
// ctx 必须是接听设备的请求上下文。
//
// ⚠ 排除必须按【设备】（perm/business auth_key）整体做，而不是按单个
// (raw auth_key, session)。接听设备可能有多条到本服务器的连接——OwpenGram 客户端
// 把 dc 1..5 都指向同一服务器，故一台真机常有数条连接/会话。若只排除受理 accept 的
// 那一条 session，停振铃的 phoneCallDiscarded 会漏到同一台设备的其它连接上，客户端
// 的 update 处理器按 call_id 匹配后当作"通话被挂断"、立即杀掉它刚接起的通话
//（现象：被叫按下接听后立刻 Failed to connect；主叫方/被叫单连接侧无此问题——故表现
// 为「A 打 B 正常、B 打 A 一接就断」的方向不对称）。按 business auth_key 排除可覆盖该
// 设备的全部连接。See memory: call-*.
func (r *Router) pushPhoneCallStopRinging(ctx context.Context, call domain.PhoneCall) int {
	upd := r.phoneCallUpdatesWith(ctx, tgPhoneCallStopRinging(call), call, call.ParticipantID)
	if pusher, ok := r.deps.Sessions.(DeviceExcludingSessionPusher); ok {
		if businessAuthKeyID, has := AuthKeyIDFrom(ctx); has {
			sent, err := pusher.PushToUserExceptBusinessAuthKey(ctx, call.ParticipantID, businessAuthKeyID, proto.MessageFromServer, upd, r.cfg.OutboundPushTimeout)
			if err != nil {
				r.log.Debug("phone call stop ringing", zap.Int64("user_id", call.ParticipantID), zap.Int("sent", sent), zap.Error(err))
			}
			return sent
		}
	}
	// 回退：能力不可用时退回按 session 排除（旧行为）。
	return r.pushUserMessage(ctx, call.ParticipantID, "phone call stop ringing", upd)
}

// pushPhoneCallDiscardedBoth 把终态推给双方全部设备（发起设备由 ctx except 排除，
// 其结果从 RPC 响应获得）。
func (r *Router) pushPhoneCallDiscardedBoth(ctx context.Context, call domain.PhoneCall) {
	r.pushPhoneCall(ctx, call.AdminID, call, "phone call discarded")
	r.pushPhoneCall(ctx, call.ParticipantID, call, "phone call discarded")
}

// pushPhoneSignalingData 把信令字节透传给对端。优先走设备锚点定向推送
// （requestCall/acceptCall 受理设备），锚点失效（设备重连换 session）则回退
// user 级扇出——非参与设备按 phone_call_id 不匹配静默丢弃（TDesktop
// handleSignalingData 行为），扇出无害。
func (r *Router) pushPhoneSignalingData(ctx context.Context, targetUserID int64, device domain.SessionRef, callID int64, data []byte) {
	upd := &tg.Updates{
		Updates: []tg.UpdateClass{&tg.UpdatePhoneCallSignalingData{
			PhoneCallID: callID,
			Data:        data,
		}},
		Users: []tg.UserClass{},
		Chats: []tg.ChatClass{},
		Date:  int(r.clock.Now().Unix()),
		Seq:   0,
	}
	if !device.Zero() && r.deps.Sessions != nil {
		if err := r.deps.Sessions.PushToSessionForAuthKey(ctx, device.RawAuthKeyID, device.SessionID, proto.MessageFromServer, upd); err == nil {
			return
		}
	}
	r.pushUserMessage(ctx, targetUserID, "phone call signaling", upd)
}
