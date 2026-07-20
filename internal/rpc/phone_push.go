package rpc

import (
	"context"

	"github.com/iamxvbaba/td/tg"

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

// pushPhoneCallStopRinging 曾向被叫【其它设备】推合成 phoneCallDiscarded 停振铃。
// 现已停用（no-op），原因见下。
//
// ⚠ 本部署的致命陷阱：OwpenGram 客户端把 dc 1..5 都指向同一台服务器，所以一台真机
// 会对本服务器开【多条】连接，且每条连接各自握手、各有【不同】的 perm/business
// auth_key。服务端仅凭 auth_key 无法把「同一台真机的其它连接」与「另一台真机」区分
// 开。任何「发给被叫、排除受理设备」的 phoneCallDiscarded 都会漏到受理真机的其它连接
// 上——客户端 update 处理器按 call_id 匹配后当作「通话被挂断」，立即杀掉它刚接起的
// 通话（现象：被叫一按接听就 Failed to connect；且因单连接的桌面端不受影响，表现为
// 「A 打 B 正常、B 打 A 一接就断」的方向不对称）。
//
// 取舍：宁可放弃"多真机时其它设备立即停振铃"这一优化（其它真机会各自走 ring 超时
// 停振铃，延迟数十秒、且多真机场景罕见），也绝不能误杀正在建立的通话。故此处不再
// 下发任何 discard。See memory: call-stop-ringing-device-exclude.
func (r *Router) pushPhoneCallStopRinging(ctx context.Context, call domain.PhoneCall) int {
	return 0
}

// pushPhoneCallDiscardedBoth 把终态推给双方全部设备（发起设备由 ctx except 排除，
// 其结果从 RPC 响应获得）。
func (r *Router) pushPhoneCallDiscardedBoth(ctx context.Context, call domain.PhoneCall) {
	r.pushPhoneCall(ctx, call.AdminID, call, "phone call discarded")
	r.pushPhoneCall(ctx, call.ParticipantID, call, "phone call discarded")
}

// pushPhoneSignalingData 把信令字节透传给对端。
//
// ⚠ 绝不能定向推给单个「受理设备」的 session 锚点——本部署里被叫真机把 dc 1..5 别名到
// 同一服务器，故一台真机对本服务器有多条连接/session，而它【接收 update 的那条】未必
// 就是承载 acceptCall RPC 的那条。若锚点指向一条尚未 updates-ready 的 session，
// PushToSessionForAuthKey 会把信令【塞进 pending 队列并返回 nil】（不是错误），旧代码
// 误以为已投递、不再回退——offer 被积压在死队列里、被叫 tgcalls 永远收不到，一接就断。
//
// 用 user 级 durable 扇出（pushUserMessage，与本文件顶部设计一致）到该用户全部连接：
// 已 updates-ready 的连接【实时收到】；尚在预热（刚切到该账号、还没 getState/getDifference）
// 的连接把这几条【短暂入 pending】，等它一 ready 就补发——避免了 transient 直接丢弃导致
// 「切账号后头一两次通话丢 ICE candidate、一接就断，重拨几次预热完才通」的现象。stale
// 信令按 phone_call_id 不匹配被客户端静默丢弃（TDesktop/DrKLO handleSignalingData），无害。
// device 锚点已不再使用。
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
	r.pushUserMessage(ctx, targetUserID, "phone call signaling", upd)
}
