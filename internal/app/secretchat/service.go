package secretchat

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// Service 实现密聊握手状态机 + qts 消息投递。所有返回的 domain.SecretChat 都是当时快照。
// 访问校验（self/bot/拉黑/隐私）在 rpc 层先行；本层做 DH 校验、chat_id wire 不变量、access_hash 分配、
// 状态机迁移与 qts 队列写入。绑定维度是设备级 perm auth_key（int64）。
type Service struct {
	store store.SecretChatStore
	queue store.EncryptedQueueStore
}

// NewService 创建密聊服务。
func NewService(st store.SecretChatStore, queue store.EncryptedQueueStore) *Service {
	return &Service{store: st, queue: queue}
}

// RequestEncryption 受理 requestEncryption：校验 g_a → 校验 random_id/chat_id 全局唯一性 → 分配双
// access_hash → 盲存 g_a → 落 requested 态。返回的密聊由 rpc 层投影为 admin 视角
// encryptedChatWaiting（同步响应）与 participant 视角 encryptedChatRequested（推送）。
func (s *Service) RequestEncryption(ctx context.Context, req domain.SecretChatRequest) (domain.SecretChat, error) {
	if req.AdminUserID == 0 || req.ParticipantUserID == 0 || req.AdminAuthKeyID == 0 {
		return domain.SecretChat{}, ErrGAInvalid
	}
	if req.RandomID == 0 {
		return domain.SecretChat{}, domain.ErrSecretChatRandomIDDuplicate
	}
	ga, err := validateDHParam(req.GA)
	if err != nil {
		return domain.SecretChat{}, err
	}
	// Telegram wire 契约：requestEncryption.random_id 同时就是 chat_id。TDLib 会先以
	// random_id 创建本地 SecretChatActor，并在消费响应时强校验 response.id 相等；禁止
	// 用服务端序列替换。全局主键碰撞只允许相同意图的网络重放，其余显式 duplicate。
	chatID := int(req.RandomID)
	if existing, ok, err := s.store.GetSecretChat(ctx, chatID); err != nil {
		return domain.SecretChat{}, err
	} else if ok {
		if sameSecretChatRequest(existing, req, ga) && !existing.Terminal() {
			return existing, nil
		}
		return domain.SecretChat{}, domain.ErrSecretChatRandomIDDuplicate
	}
	adminAH, err := randomAccessHash()
	if err != nil {
		return domain.SecretChat{}, err
	}
	participantAH, err := randomAccessHash()
	if err != nil {
		return domain.SecretChat{}, err
	}
	chat := domain.SecretChat{
		ID:                    chatID,
		AdminAccessHash:       adminAH,
		ParticipantAccessHash: participantAH,
		AdminUserID:           req.AdminUserID,
		ParticipantUserID:     req.ParticipantUserID,
		AdminAuthKeyID:        req.AdminAuthKeyID,
		State:                 domain.SecretChatStateRequested,
		GA:                    ga,
		RandomID:              req.RandomID,
		Date:                  req.Date,
	}
	if err := s.store.CreateSecretChat(ctx, chat); err == nil {
		return chat, nil
	} else if !errors.Is(err, domain.ErrSecretChatRandomIDDuplicate) {
		return domain.SecretChat{}, err
	}
	// 并发相同请求可能在预查后由另一 goroutine 插入；只在重新读取后仍证明
	// 是完全相同意图时收敛为幂等成功。
	existing, ok, getErr := s.store.GetSecretChat(ctx, chatID)
	if getErr != nil {
		return domain.SecretChat{}, getErr
	}
	if ok && sameSecretChatRequest(existing, req, ga) && !existing.Terminal() {
		return existing, nil
	}
	return domain.SecretChat{}, domain.ErrSecretChatRandomIDDuplicate
}

func sameSecretChatRequest(chat domain.SecretChat, req domain.SecretChatRequest, normalizedGA []byte) bool {
	return chat.ID == int(req.RandomID) && chat.RandomID == req.RandomID &&
		chat.AdminUserID == req.AdminUserID && chat.AdminAuthKeyID == req.AdminAuthKeyID &&
		chat.ParticipantUserID == req.ParticipantUserID && bytes.Equal(chat.GA, normalizedGA)
}

// AcceptEncryption 受理 acceptEncryption：定位 + participant 视角 access_hash 校验 →
// 校验 g_b → 原子 CAS 绑定接受设备并落 g_b/key_fingerprint → normal。返回的密聊由
// rpc 层投影为 participant 视角 encryptedChat（GAOrB=g_a，同步响应）与 admin 视角
// encryptedChat（GAOrB=g_b，推送）。
func (s *Service) AcceptEncryption(ctx context.Context, chatID int, viewerUserID, participantAuthKeyID, accessHash int64, gb []byte, keyFingerprint int64) (domain.SecretChat, error) {
	gbPadded, err := validateDHParam(gb)
	if err != nil {
		return domain.SecretChat{}, err
	}
	chat, ok, err := s.store.GetSecretChat(ctx, chatID)
	if err != nil {
		return domain.SecretChat{}, err
	}
	// 视角校验：调用方必须是接受方本人且 access_hash 匹配 participant 视角。
	if !ok || chat.ParticipantUserID != viewerUserID || chat.ParticipantAccessHash != accessHash {
		return domain.SecretChat{}, domain.ErrSecretChatNotFound
	}
	switch chat.State {
	case domain.SecretChatStateNormal:
		return domain.SecretChat{}, domain.ErrSecretChatAlreadyAccepted
	case domain.SecretChatStateDiscarded:
		return domain.SecretChat{}, domain.ErrSecretChatAlreadyDeclined
	}
	return s.store.AcceptSecretChat(ctx, chatID, participantAuthKeyID, gbPadded, keyFingerprint)
}

// DiscardEncryption 受理 discardEncryption：定位 + 参与者/绑定设备校验 → 迁移到 discarded。
// already=true 表示已是终态（幂等成功）。返回的密聊由 rpc 层投影为对端
// encryptedChatDiscarded 推送。
func (s *Service) DiscardEncryption(ctx context.Context, chatID int, viewerUserID, viewerAuthKeyID int64, deleteHistory bool) (domain.SecretChat, bool, error) {
	chat, ok, err := s.store.GetSecretChat(ctx, chatID)
	if err != nil {
		return domain.SecretChat{}, false, err
	}
	if !ok || !chat.HasParticipant(viewerUserID) || viewerAuthKeyID == 0 {
		return domain.SecretChat{}, false, domain.ErrSecretChatNotFound
	}
	// Admin 从 request 起即绑定；participant 在 accept 前尚无绑定，任一收到账号级邀请的
	// participant 设备都可拒绝。accept 一旦完成，双方所有操作都必须来自各自绑定设备。
	boundAuthKeyID := chat.AuthKeyOf(viewerUserID)
	if boundAuthKeyID != 0 && boundAuthKeyID != viewerAuthKeyID {
		return domain.SecretChat{}, false, domain.ErrSecretChatNotFound
	}
	if boundAuthKeyID == 0 && viewerUserID != chat.ParticipantUserID {
		return domain.SecretChat{}, false, domain.ErrSecretChatNotFound
	}
	return s.store.DiscardSecretChat(ctx, chatID, deleteHistory)
}

// GetSecretChat 取密聊快照（rpc 层访问校验用）。
func (s *Service) GetSecretChat(ctx context.Context, chatID int) (domain.SecretChat, bool, error) {
	return s.store.GetSecretChat(ctx, chatID)
}

// DiscardForAuthKey 级联 discard 绑定该设备 perm auth_key（作为 admin 或 participant）的
// 全部活跃密聊，用于设备登出 / 授权撤销。返回本次实际从非终态迁移到 discarded 的密聊快照
// （已是终态的不返回），供 rpc 层据此向对端推送 encryptedChatDiscarded。盲中继：不删历史
// （history_deleted=false，对端自行决定本地处置）。出错时返回已成功 discard 的部分 + err，
// 让调用方仍能通知这部分对端（登出/撤销是 best-effort，不因此回退）。
func (s *Service) DiscardForAuthKey(ctx context.Context, authKeyID int64) ([]domain.SecretChat, error) {
	if authKeyID == 0 {
		return nil, nil
	}
	chats, err := s.store.ListActiveSecretChatsByAuthKey(ctx, authKeyID)
	if err != nil {
		return nil, err
	}
	var discarded []domain.SecretChat
	for _, c := range chats {
		updated, already, derr := s.store.DiscardSecretChat(ctx, c.ID, false)
		if derr != nil {
			if errors.Is(derr, domain.ErrSecretChatNotFound) {
				continue
			}
			return discarded, derr
		}
		if !already {
			discarded = append(discarded, updated)
		}
	}
	return discarded, nil
}

// SendEncrypted 受理 sendEncrypted*：定位 + 发送方绑定设备/access_hash 校验 + 态须 normal →
// 给【对端绑定设备】分配 qts 并把不透明 bytes 写入投递队列（幂等：同 chat+random_id 返既有
// qts/date）。返回密聊快照 + 已落库消息（携 qts/date，rpc 层据此推 updateNewEncryptedMessage
// 并回 SentEncryptedMessage{date}）。盲中継：不解密 bytes。
func (s *Service) SendEncrypted(ctx context.Context, chatID int, viewerUserID, viewerAuthKeyID, accessHash int64, delivery domain.SecretMessageDelivery) (domain.SecretChat, domain.SecretChatMessage, error) {
	chat, ok, err := s.store.GetSecretChat(ctx, chatID)
	if err != nil {
		return domain.SecretChat{}, domain.SecretChatMessage{}, err
	}
	if !ok || !chat.HasParticipant(viewerUserID) || viewerAuthKeyID == 0 ||
		chat.AuthKeyOf(viewerUserID) != viewerAuthKeyID || chat.AccessHashFor(viewerUserID) != accessHash {
		return domain.SecretChat{}, domain.SecretChatMessage{}, domain.ErrSecretChatNotFound
	}
	if chat.State != domain.SecretChatStateNormal {
		// 未成型 / 已销毁的密聊不能收发（CHAT_ID_INVALID）。
		return domain.SecretChat{}, domain.SecretChatMessage{}, domain.ErrSecretChatNotFound
	}
	receiverUserID := chat.PeerOf(viewerUserID)
	receiverAuthKeyID := chat.AdminAuthKeyID
	if chat.IsAdmin(viewerUserID) {
		receiverAuthKeyID = chat.ParticipantAuthKeyID
	}
	if receiverUserID == 0 || receiverAuthKeyID == 0 {
		return domain.SecretChat{}, domain.SecretChatMessage{}, domain.ErrSecretChatNotFound
	}
	stored, _, err := s.queue.AppendEncryptedMessage(ctx, domain.SecretChatMessage{
		ReceiverAuthKeyID: receiverAuthKeyID,
		ReceiverUserID:    receiverUserID,
		ChatID:            chatID,
		RandomID:          delivery.RandomID,
		Date:              delivery.Date,
		IsService:         delivery.IsService,
		Bytes:             delivery.Bytes,
		File:              delivery.File,
	})
	if err != nil {
		return domain.SecretChat{}, domain.SecretChatMessage{}, err
	}
	return chat, stored, nil
}

// ListNewMessages 返回某设备 qts > sinceQts 的连续加密消息（getDifference 补差分用）。
func (s *Service) ListNewMessages(ctx context.Context, deviceAuthKeyID int64, sinceQts, limit int) ([]domain.SecretChatMessage, error) {
	if deviceAuthKeyID == 0 {
		return nil, nil
	}
	return s.queue.ListEncryptedMessagesSince(ctx, deviceAuthKeyID, sinceQts, limit)
}

// DeviceReservedQts 返回某设备当前已分配的最高 qts（getState 用）。
func (s *Service) DeviceReservedQts(ctx context.Context, deviceAuthKeyID int64) (int, error) {
	if deviceAuthKeyID == 0 {
		return 0, nil
	}
	return s.queue.ReservedQts(ctx, deviceAuthKeyID)
}

// AckQueue 推进某设备的 confirmed qts 并标记 acked（receivedQueue）。
func (s *Service) AckQueue(ctx context.Context, deviceAuthKeyID int64, maxQts int) error {
	if deviceAuthKeyID == 0 || maxQts <= 0 {
		return nil
	}
	return s.queue.AckEncryptedMessages(ctx, deviceAuthKeyID, maxQts)
}

// RecordEncryptionEvent 写入 durable updateEncryption 状态事件（离线补偿）。
// targetAuthKeyID=0 表示账号级（建链前邀请/撤回对 target 所有设备可见），非 0 表示
// 绑定设备定向。投递时按 secret_chats 权威态重建（不固化快照）。
func (s *Service) RecordEncryptionEvent(ctx context.Context, chatID int, targetUserID, targetAuthKeyID int64, date int) error {
	if targetUserID == 0 {
		return nil
	}
	_, err := s.queue.AppendStateEvent(ctx, domain.EncryptedStateEvent{
		TargetUserID:    targetUserID,
		TargetAuthKeyID: targetAuthKeyID,
		ChatID:          chatID,
		Type:            domain.EncryptedStateEventEncryption,
		Date:            date,
	})
	return err
}

// RecordReadEvent 写入 durable updateEncryptedMessagesRead 状态事件（离线补偿，设备定向）。
func (s *Service) RecordReadEvent(ctx context.Context, chatID int, targetUserID, targetAuthKeyID int64, maxDate, date int) error {
	if targetUserID == 0 {
		return nil
	}
	_, err := s.queue.AppendStateEvent(ctx, domain.EncryptedStateEvent{
		TargetUserID:    targetUserID,
		TargetAuthKeyID: targetAuthKeyID,
		ChatID:          chatID,
		Type:            domain.EncryptedStateEventRead,
		MaxDate:         maxDate,
		Date:            date,
	})
	return err
}

// ListStateEvents 返回某设备未投递的密聊状态事件（getDifference 补偿用）。
func (s *Service) ListStateEvents(ctx context.Context, userID, deviceAuthKeyID int64, limit int) ([]domain.EncryptedStateEvent, error) {
	if userID == 0 || deviceAuthKeyID == 0 {
		return nil, nil
	}
	return s.queue.ListUndeliveredStateEvents(ctx, userID, deviceAuthKeyID, limit)
}

// MarkStateEventsDelivered 登记某设备已投递这些状态事件。
func (s *Service) MarkStateEventsDelivered(ctx context.Context, deviceAuthKeyID int64, eventIDs []int64) error {
	if deviceAuthKeyID == 0 || len(eventIDs) == 0 {
		return nil
	}
	return s.queue.MarkStateEventsDelivered(ctx, deviceAuthKeyID, eventIDs)
}

// PutEncryptedFile 持久化密聊文件元数据快照（铸造后写一次）。
func (s *Service) PutEncryptedFile(ctx context.Context, ownerUserID int64, ref domain.EncryptedFileRef) error {
	if ref.ID == 0 {
		return nil
	}
	return s.queue.PutEncryptedFile(ctx, ownerUserID, ref)
}

// GetEncryptedFile 按 id + access_hash 回查文件快照（inputEncryptedFile 复用路径）。
func (s *Service) GetEncryptedFile(ctx context.Context, id, accessHash int64) (domain.EncryptedFileRef, bool, error) {
	return s.queue.GetEncryptedFile(ctx, id, accessHash)
}

// randomAccessHash 生成正 int64 access_hash（rand 8B → 高位清零保正，0 置 1）。
func randomAccessHash() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("secretchat: access hash rand: %w", err)
	}
	v := int64(binary.BigEndian.Uint64(b[:]) >> 1)
	if v == 0 {
		v = 1
	}
	return v, nil
}
