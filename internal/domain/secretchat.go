package domain

import "errors"

// SecretChatState 是私聊端对端加密（Secret Chat / EncryptedChat）握手状态机的
// 服务端权威态。服务端是盲中继：g_a/g_b/key_fingerprint/加密 bytes 全部不透明
// 存储与原样转发，永不接触 DH 共享密钥与明文。设计见 docs/secret-chat-module.md。
type SecretChatState string

const (
	// SecretChatStateRequested：requestEncryption 已受理、等接受方 acceptEncryption。
	// 同一服务端态对发起方投影为 encryptedChatWaiting、对接受方投影为 encryptedChatRequested。
	SecretChatStateRequested SecretChatState = "requested"
	// SecretChatStateNormal：acceptEncryption 完成 g_b/key_fingerprint 落库，握手成型。
	SecretChatStateNormal SecretChatState = "normal"
	// SecretChatStateDiscarded：终态，任一方 discardEncryption 触发。
	SecretChatStateDiscarded SecretChatState = "discarded"
)

// Secret chat 存储层错误（memory/postgres 双实现共用，行为契约由 storetest 钉死）。
var (
	// ErrSecretChatNotFound：chat_id 不存在 → CHAT_ID_INVALID / ENCRYPTION_ID_INVALID。
	ErrSecretChatNotFound = errors.New("secretchat: not found")
	// ErrSecretChatAlreadyAccepted：accept 一个已成型的密聊 → ENCRYPTION_ALREADY_ACCEPTED。
	ErrSecretChatAlreadyAccepted = errors.New("secretchat: already accepted")
	// ErrSecretChatAlreadyDeclined：accept 一个已销毁的密聊 → ENCRYPTION_ALREADY_DECLINED。
	ErrSecretChatAlreadyDeclined = errors.New("secretchat: already declined")
	// ErrSecretChatRandomIDDuplicate：requestEncryption.random_id 已被不同意图或其它
	// auth key 占用。random_id 同时就是 chat_id，禁止另分配 ID 规避碰撞。
	ErrSecretChatRandomIDDuplicate = errors.New("secretchat: random id duplicate")
)

// SecretChat 是一通私聊密聊的服务端权威态（durable，跨重启存活）。
// 字段命名对齐 TL encryptedChat*；ID 是 int32 量级，access_hash/admin_id/
// participant_id/key_fingerprint 是 int64。绑定维度是设备级（perm auth_key 的 int64 值）。
type SecretChat struct {
	// ID 是 chat_id，必须逐位等于 requestEncryption.random_id（非零 int32）；双方共享同一 id。
	ID int
	// AdminAccessHash / ParticipantAccessHash 双视角不同（TL "check sum depending on user ID"）。
	AdminAccessHash       int64
	ParticipantAccessHash int64
	// AdminUserID 是发起方，ParticipantUserID 是接受方；据此判角色。
	AdminUserID       int64
	ParticipantUserID int64
	// AdminAuthKeyID / ParticipantAuthKeyID 是绑定设备的 perm auth_key（authKeyIDToInt64 小端值）。
	// ParticipantAuthKeyID 在 accept 前为 0（建链前邀请对 participant 账号级可见）。
	AdminAuthKeyID       int64
	ParticipantAuthKeyID int64

	State SecretChatState

	// GA 是 requestEncryption 携带的发起方公钥（盲存，左补零 256B）。
	// GB 是 acceptEncryption 携带的接受方公钥（accept 后落库）。
	// 投影 GAOrB：对 admin 视角是 GB，对 participant 视角是 GA（TL 注释钉死）。
	GA []byte
	GB []byte
	// KeyFingerprint 是接受方算出的共享密钥指纹，服务端原样 int64 中继、绝不重算。
	KeyFingerprint int64

	// Layer 是密聊内层 layer 快照（不解析）；FolderID 透传 requested 归档意图。
	Layer    int
	FolderID int
	// HistoryDeleted 是 discard 时是否要求对端删整个会话历史。
	HistoryDeleted bool

	// RandomID 是 requestEncryption 的 int32 幂等键（同发起设备同 random_id 返同 chat）。
	RandomID int32
	// Date 是受理时刻（unix 秒）。
	Date int
}

// Terminal 报告密聊是否已销毁。
func (c SecretChat) Terminal() bool {
	return c.State == SecretChatStateDiscarded
}

// HasParticipant 报告 userID 是否为本密聊的发起方或接受方。
func (c SecretChat) HasParticipant(userID int64) bool {
	return userID != 0 && (userID == c.AdminUserID || userID == c.ParticipantUserID)
}

// IsAdmin 报告 userID 是否为发起方。
func (c SecretChat) IsAdmin(userID int64) bool {
	return userID != 0 && userID == c.AdminUserID
}

// PeerOf 返回 userID 在本密聊中的对端；userID 不是参与者时返回 0。
func (c SecretChat) PeerOf(userID int64) int64 {
	switch userID {
	case c.AdminUserID:
		return c.ParticipantUserID
	case c.ParticipantUserID:
		return c.AdminUserID
	default:
		return 0
	}
}

// PeerAuthKeyOf 返回 userID 的对端绑定设备 perm auth_key（int64）；非参与者或对端
// 未绑定返回 0。admin 的对端是 participant 设备，反之亦然。
func (c SecretChat) PeerAuthKeyOf(userID int64) int64 {
	switch userID {
	case c.AdminUserID:
		return c.ParticipantAuthKeyID
	case c.ParticipantUserID:
		return c.AdminAuthKeyID
	default:
		return 0
	}
}

// AuthKeyOf 返回 userID 自身绑定的 permanent auth key；非参与者或尚未绑定返回 0。
// 已建立密聊的所有读写授权必须同时匹配 user 与该 auth key，不能只依赖账号身份。
func (c SecretChat) AuthKeyOf(userID int64) int64 {
	switch userID {
	case c.AdminUserID:
		return c.AdminAuthKeyID
	case c.ParticipantUserID:
		return c.ParticipantAuthKeyID
	default:
		return 0
	}
}

// AccessHashFor 返回 userID 视角的 access_hash（双方不同）；非参与者返回 0。
func (c SecretChat) AccessHashFor(userID int64) int64 {
	switch userID {
	case c.AdminUserID:
		return c.AdminAccessHash
	case c.ParticipantUserID:
		return c.ParticipantAccessHash
	default:
		return 0
	}
}

// EncryptedFileRef 是密聊文件的服务端快照（P2，盲中继：内容是加密 bytes，不解密）。
type EncryptedFileRef struct {
	ID             int64
	AccessHash     int64
	Size           int64
	DCID           int
	KeyFingerprint int // int32 量级
}

// SecretChatMessage 是密聊 qts 投递队列里的一条不透明加密消息（updateNewEncryptedMessage
// 的载荷）。Bytes 是客户端加密的 DecryptedMessage，服务端盲存盲转、永不解密。
// 按接收方设备（ReceiverAuthKeyID）的 qts 序列投递。
type SecretChatMessage struct {
	ReceiverAuthKeyID int64
	Qts               int
	ReceiverUserID    int64
	ChatID            int
	RandomID          int64
	Date              int
	IsService         bool
	Bytes             []byte
	File              *EncryptedFileRef
}

// EncryptedStateEventType 区分无 qts 的密聊状态事件种类。
type EncryptedStateEventType int

const (
	// EncryptedStateEventEncryption：updateEncryption（握手态变化）；投递时按 secret_chats
	// 权威态重建（不固化快照）。
	EncryptedStateEventEncryption EncryptedStateEventType = 1
	// EncryptedStateEventRead：updateEncryptedMessagesRead（已读回执），携 MaxDate。
	EncryptedStateEventRead EncryptedStateEventType = 2
)

// EncryptedStateEvent 是无 qts 的 durable 密聊状态事件（离线 getDifference 补偿）。
// TargetAuthKeyID=0 表示账号级（所有设备可见），非 0 表示绑定设备定向。
type EncryptedStateEvent struct {
	ID              int64
	TargetUserID    int64
	TargetAuthKeyID int64
	ChatID          int
	Type            EncryptedStateEventType
	MaxDate         int
	Date            int
}

// SecretMessageDelivery 是 sendEncrypted* 要投递给对端设备的一条加密消息（rpc 层组装，
// service 分配接收设备 qts 并落库）。
type SecretMessageDelivery struct {
	RandomID  int64
	Bytes     []byte
	IsService bool
	File      *EncryptedFileRef
	Date      int
}

// MaxSecretMessageDataBytes bounds the opaque encrypted DecryptedMessage payload persisted in
// the device queue. Secret-chat media bytes travel through the file service, so a 1 MiB metadata
// envelope is already well above the payload emitted by the supported clients while keeping one
// request independent from the process-wide MTProto admission budget.
const MaxSecretMessageDataBytes = 1 << 20

// SecretChatRequest 是 requestEncryption 受理入参（隐私/拉黑/self/bot 校验在 rpc 层先行）。
type SecretChatRequest struct {
	AdminUserID       int64
	AdminAuthKeyID    int64
	ParticipantUserID int64
	RandomID          int32
	GA                []byte
	Date              int
}
