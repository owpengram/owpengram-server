package rpc

import (
	"context"
	"strconv"

	"go.uber.org/zap"

	"telesrv/internal/domain"
)

// 私聊通话 P3：STUN/TURN 中继参数（phoneConnectionWebrtc）与 p2p_allowed 真值。
// 凭据在 requestCall 受理时一次性签发并存于 call 快照——同一通话的所有视角
//（RPC 响应与推送、主叫与被叫）看到同一份 connections，与官方行为一致。

// phoneCallPrivacyP2P 计算 phone_p2p 隐私的双向 AND：任一方不允许对方 P2P，
// 该通话就只能走中继（p2p_allowed=false 时 tgcalls 丢弃全部非 relay candidate）。
// 隐私服务缺席时按放行处理（与 P1 行为一致）；CallForceRelay 强制中继（调试用）。
func (r *Router) phoneCallPrivacyP2P(ctx context.Context, callerID, calleeID int64) bool {
	if r.cfg.CallForceRelay {
		return false
	}
	if r.deps.Privacy == nil {
		return true
	}
	calleeAllows, err := r.deps.Privacy.CanSee(ctx, calleeID, callerID, domain.PrivacyKeyPhoneP2P)
	if err != nil {
		return true
	}
	callerAllows, err := r.deps.Privacy.CanSee(ctx, callerID, calleeID, domain.PrivacyKeyPhoneP2P)
	if err != nil {
		return true
	}
	return calleeAllows && callerAllows
}

// phoneCallConnections 为一通通话签发 STUN/TURN 条目。TURN 未启用返回空列表
// （tgcalls 遍历空列表零次，退回纯信令交换 host candidates 的 LAN 直连）。
func (r *Router) phoneCallConnections(callerID int64) []domain.PhoneCallConnection {
	t := r.deps.TURN
	if t == nil || !t.Enabled() {
		return nil
	}
	username, password, err := t.Credentials(strconv.FormatInt(callerID, 10))
	if err != nil {
		r.log.Warn("phone call turn credentials", zap.Error(err))
		return nil
	}
	// ⚠ STUN 与 TURN 必须拆成两个条目（与官方一致）：DrKLO 的 JNI 层根本不读
	// stun flag——单条目 stun+turn 在 Android 上只会产出 TURN server、丢失 STUN
	//（org_telegram_messenger_voip_Instance.cpp:848-884）。TDesktop 两种写法都认。
	// TURN username 是 REST 格式 "<expiry>:<uid>"，天然避开 "reflector" 劫持禁区。
	//
	// 每个可达 IP（AdvertiseIP + ExtraIPs）都下发一对 STUN/TURN 候选，ICE 逐一
	// 尝试并选可达者：LAN 客户端走 LAN IP、外网客户端走公网 IP。id 必须全局唯一
	// 且从 1 递增（DrKLO 用 id 做 reflector 映射）。凭据与 IP 无关（TURN REST 只
	// 校验 HMAC），同一份 username/password 对所有 IP 有效。
	ips := append([]string{t.IP()}, t.ExtraIPs()...)
	conns := make([]domain.PhoneCallConnection, 0, len(ips)*2)
	id := int64(1)
	for _, ip := range ips {
		if ip == "" {
			continue
		}
		conns = append(conns, domain.PhoneCallConnection{ID: id, IP: ip, Port: t.Port(), Stun: true})
		id++
		conns = append(conns, domain.PhoneCallConnection{ID: id, IP: ip, Port: t.Port(), Username: username, Password: password, Turn: true})
		id++
	}
	return conns
}
