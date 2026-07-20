package rpc

import (
	"context"
	"encoding/hex"

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
	sent := r.pushUserMessage(ctx, targetUserID, logMessage, r.phoneCallUpdates(ctx, call, targetUserID))
	// 诊断：确认服务端把 phoneCall 状态推给了对端【几条】连接。sent==0 说明对端
	// 全部连接都没收到（一接就断类问题的关键判据）。
	if r.log != nil {
		r.log.Info("push phoneCall",
			zap.String("stage", logMessage),
			zap.Int64("target_user_id", targetUserID),
			zap.Int64("call_id", call.ID),
			zap.Int("sent", sent),
		)
	}
	return sent
}

// pushPhoneCallStopRinging 向被叫其它设备推合成 phoneCallDiscarded 停振铃（P0-1 修正）。
// ctx 必须是接听设备的请求上下文：except 语义恰好把赢家排除在外。
func (r *Router) pushPhoneCallStopRinging(ctx context.Context, call domain.PhoneCall) int {
	upd := r.phoneCallUpdatesWith(ctx, tgPhoneCallStopRinging(call), call, call.ParticipantID)
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
	// ⚠ 诊断轮：暂时【绕过 device 锚点】，强制 user 级扇出并记录实际命中的连接数
	// （sent）。PushToSessionForAuthKey 对未就绪 session 会「入队并返回 nil」，无法
	// 区分"真送达"与"塞进死队列"；而扇出的 sent 计数是硬事实——sent==0 说明对端此刻
	// 没有任何一条 updates-ready 连接可收，服务端根本送不出去（客户端 dc-aliasing 多
	// 连接、承载 update 的那条未 ready）；sent>=1 说明服务端送到了 N 条，问题在客户端
	// 消费侧。据此一刀切分服务端/客户端责任。
	sent := r.pushUserMessage(ctx, targetUserID, "phone call signaling", upd)
	if r.log != nil {
		r.log.Info("push phone signaling",
			zap.Int64("target_user_id", targetUserID),
			zap.Int64("call_id", callID),
			zap.String("callee_anchor_auth_key", hex.EncodeToString(device.RawAuthKeyID[:])),
			zap.Int64("callee_anchor_session", device.SessionID),
			zap.Int("sent", sent),
			zap.Int("data_len", len(data)),
		)
	}
}
