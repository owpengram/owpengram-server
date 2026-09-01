package store

import (
	"context"

	"telesrv/internal/domain"
)

// SecretChatStore 持久化私聊密聊握手状态机真值（memory/postgres 双实现，行为契约由
// storetest 钉死）。服务端是盲中继：g_a/g_b/key_fingerprint 原样不透明存储。
// 设计见 docs/secret-chat-module.md。
type SecretChatStore interface {
	// CreateSecretChat 插入 requested 态密聊。chat_id 必须等于 requestEncryption.random_id；
	// 主键/幂等键撞键返回 domain.ErrSecretChatRandomIDDuplicate，禁止另分配 ID。
	CreateSecretChat(ctx context.Context, chat domain.SecretChat) error
	// GetSecretChat 按 chat_id 取密聊。
	GetSecretChat(ctx context.Context, chatID int) (domain.SecretChat, bool, error)
	// AcceptSecretChat 原子 CAS 绑定：仅当 requested 且 participant_auth_key_id 未绑定时
	// 迁移到 normal、落 g_b/key_fingerprint、绑定接受设备。并发第二个 accept 或已成型/
	// 已销毁分别返回 domain.ErrSecretChatAlreadyAccepted / ErrSecretChatAlreadyDeclined；
	// chat_id 不存在返回 domain.ErrSecretChatNotFound。
	AcceptSecretChat(ctx context.Context, chatID int, participantAuthKeyID int64, gb []byte, keyFingerprint int64) (domain.SecretChat, error)
	// DiscardSecretChat 迁移到 discarded（幂等）；已是终态时 already=true 返回当前快照；
	// chat_id 不存在返回 domain.ErrSecretChatNotFound。
	DiscardSecretChat(ctx context.Context, chatID int, historyDeleted bool) (chat domain.SecretChat, already bool, err error)
	// ListActiveSecretChatsByAuthKey 返回绑定该设备 perm auth_key（作为发起方 admin 或
	// 接受方 participant）且未终态的密聊，按 chat_id 升序。设备登出 / 授权撤销时用于级联
	// discard 并通知对端（避免对端继续往死 auth_key 投递的静默死链）。authKeyID==0 返回 nil。
	ListActiveSecretChatsByAuthKey(ctx context.Context, authKeyID int64) ([]domain.SecretChat, error)
}

// EncryptedQueueStore 持久化密聊 qts 投递队列（设备级，memory/postgres 双实现）。
// updateNewEncryptedMessage 按接收设备 qts 序列投递；qts 分配 + 写队列 + 推进
// reserved 水位在单事务内完成（无空洞）。盲中继：bytes 原样存储。
// 设计见 docs/secret-chat-module.md §7。
type EncryptedQueueStore interface {
	// AppendEncryptedMessage 为接收设备分配下一个 qts（首值 1）并写入队列，与 reserved_qts
	// 推进同事务。幂等：同 (receiver_auth_key_id, chat_id, random_id) 已存在时返回既有行 +
	// existing=true（不重分配 qts，发送方丢响应重发拿同一 qts/date）。
	AppendEncryptedMessage(ctx context.Context, msg domain.SecretChatMessage) (stored domain.SecretChatMessage, existing bool, err error)
	// ListEncryptedMessagesSince 返回接收设备 qts > sinceQts 的连续消息（qts 升序，最多 limit）。
	ListEncryptedMessagesSince(ctx context.Context, receiverAuthKeyID int64, sinceQts, limit int) ([]domain.SecretChatMessage, error)
	// ReservedQts 返回接收设备当前已分配的最高 qts（getState 用，无行返 0）。
	ReservedQts(ctx context.Context, receiverAuthKeyID int64) (int, error)
	// AckEncryptedMessages 推进设备 confirmed_qts 到 maxQts（GREATEST 幂等，回退忽略），
	// 标记 qts<=maxQts 行为 acked（receivedQueue）。
	AckEncryptedMessages(ctx context.Context, receiverAuthKeyID int64, maxQts int) error

	// AppendStateEvent 写入无 qts 的 durable 密聊状态事件（握手/已读，离线补偿）。
	AppendStateEvent(ctx context.Context, ev domain.EncryptedStateEvent) (int64, error)
	// ListUndeliveredStateEvents 返回某设备未投递的状态事件（账号级 target_auth_key_id=0
	// 或绑定本设备），按 id 升序，最多 limit。
	ListUndeliveredStateEvents(ctx context.Context, targetUserID, deviceAuthKeyID int64, limit int) ([]domain.EncryptedStateEvent, error)
	// MarkStateEventsDelivered 登记某设备已投递这些事件（幂等）。
	MarkStateEventsDelivered(ctx context.Context, deviceAuthKeyID int64, eventIDs []int64) error

	// PutEncryptedFile 持久化密聊文件元数据快照（铸造后写一次，幂等覆盖）。
	PutEncryptedFile(ctx context.Context, ownerUserID int64, ref domain.EncryptedFileRef) error
	// GetEncryptedFile 按 id + access_hash 回查文件快照（inputEncryptedFile 复用路径）。
	GetEncryptedFile(ctx context.Context, id, accessHash int64) (domain.EncryptedFileRef, bool, error)
}
