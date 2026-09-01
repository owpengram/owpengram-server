package memory

import (
	"context"
	"sort"
	"sync"

	"telesrv/internal/domain"
)

// SecretChatStore 是 store.SecretChatStore 的进程内实现（rpc/app 单测 fixture 用）。
// 行为契约与 postgres 实现由 storetest 共享 contract test 钉死；凡动握手态迁移
// 语义两边必须同步。
type SecretChatStore struct {
	mu    sync.Mutex
	chats map[int]domain.SecretChat
}

// NewSecretChatStore 创建内存实现。
func NewSecretChatStore() *SecretChatStore {
	return &SecretChatStore{chats: make(map[int]domain.SecretChat)}
}

func cloneSecretChat(c domain.SecretChat) domain.SecretChat {
	c.GA = append([]byte(nil), c.GA...)
	c.GB = append([]byte(nil), c.GB...)
	return c
}

func (s *SecretChatStore) CreateSecretChat(_ context.Context, chat domain.SecretChat) error {
	if chat.ID == 0 || chat.ID != int(chat.RandomID) {
		return domain.ErrSecretChatRandomIDDuplicate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.chats[chat.ID]; exists {
		return domain.ErrSecretChatRandomIDDuplicate
	}
	if chat.State == "" {
		chat.State = domain.SecretChatStateRequested
	}
	s.chats[chat.ID] = cloneSecretChat(chat)
	return nil
}

func (s *SecretChatStore) GetSecretChat(_ context.Context, chatID int) (domain.SecretChat, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[chatID]
	if !ok {
		return domain.SecretChat{}, false, nil
	}
	return cloneSecretChat(c), true, nil
}

func (s *SecretChatStore) AcceptSecretChat(_ context.Context, chatID int, participantAuthKeyID int64, gb []byte, keyFingerprint int64) (domain.SecretChat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[chatID]
	if !ok {
		return domain.SecretChat{}, domain.ErrSecretChatNotFound
	}
	switch c.State {
	case domain.SecretChatStateNormal:
		return domain.SecretChat{}, domain.ErrSecretChatAlreadyAccepted
	case domain.SecretChatStateDiscarded:
		return domain.SecretChat{}, domain.ErrSecretChatAlreadyDeclined
	}
	// requested 且未绑定接受设备：CAS 成功。
	if c.ParticipantAuthKeyID != 0 {
		return domain.SecretChat{}, domain.ErrSecretChatAlreadyAccepted
	}
	c.State = domain.SecretChatStateNormal
	c.ParticipantAuthKeyID = participantAuthKeyID
	c.GB = append([]byte(nil), gb...)
	c.KeyFingerprint = keyFingerprint
	s.chats[chatID] = c
	return cloneSecretChat(c), nil
}

func (s *SecretChatStore) DiscardSecretChat(_ context.Context, chatID int, historyDeleted bool) (domain.SecretChat, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.chats[chatID]
	if !ok {
		return domain.SecretChat{}, false, domain.ErrSecretChatNotFound
	}
	if c.State == domain.SecretChatStateDiscarded {
		return cloneSecretChat(c), true, nil
	}
	c.State = domain.SecretChatStateDiscarded
	c.HistoryDeleted = historyDeleted
	s.chats[chatID] = c
	return cloneSecretChat(c), false, nil
}

func (s *SecretChatStore) ListActiveSecretChatsByAuthKey(_ context.Context, authKeyID int64) ([]domain.SecretChat, error) {
	if authKeyID == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.SecretChat
	for _, c := range s.chats {
		if c.Terminal() {
			continue
		}
		if c.AdminAuthKeyID == authKeyID || c.ParticipantAuthKeyID == authKeyID {
			out = append(out, cloneSecretChat(c))
		}
	}
	// map 遍历无序：按 chat_id 升序与 postgres ORDER BY 对齐，确定性供测试断言。
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// EncryptedQueueStore 是 store.EncryptedQueueStore 的进程内实现。
type EncryptedQueueStore struct {
	mu          sync.Mutex
	byDevice    map[int64][]domain.SecretChatMessage // receiverAuthKeyID → qts 升序消息
	reserved    map[int64]int
	confirmed   map[int64]int
	dedup       map[emqDedupKey]int // → qts
	stateEvents []domain.EncryptedStateEvent
	delivered   map[int64]map[int64]bool // eventID → deviceAuthKeyID → true
	nextEventID int64
	files       map[int64]domain.EncryptedFileRef // file id → 快照
}

type emqDedupKey struct {
	receiver int64
	chat     int
	random   int64
}

// NewEncryptedQueueStore 创建内存实现。
func NewEncryptedQueueStore() *EncryptedQueueStore {
	return &EncryptedQueueStore{
		byDevice:  make(map[int64][]domain.SecretChatMessage),
		reserved:  make(map[int64]int),
		confirmed: make(map[int64]int),
		dedup:     make(map[emqDedupKey]int),
		delivered: make(map[int64]map[int64]bool),
	}
}

func cloneSecretMessage(m domain.SecretChatMessage) domain.SecretChatMessage {
	m.Bytes = append([]byte(nil), m.Bytes...)
	if m.File != nil {
		f := *m.File
		m.File = &f
	}
	return m
}

func (s *EncryptedQueueStore) AppendEncryptedMessage(_ context.Context, msg domain.SecretChatMessage) (domain.SecretChatMessage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := emqDedupKey{msg.ReceiverAuthKeyID, msg.ChatID, msg.RandomID}
	if qts, ok := s.dedup[key]; ok {
		for _, m := range s.byDevice[msg.ReceiverAuthKeyID] {
			if m.Qts == qts {
				return cloneSecretMessage(m), true, nil
			}
		}
	}
	s.reserved[msg.ReceiverAuthKeyID]++
	msg.Qts = s.reserved[msg.ReceiverAuthKeyID]
	stored := cloneSecretMessage(msg)
	s.byDevice[msg.ReceiverAuthKeyID] = append(s.byDevice[msg.ReceiverAuthKeyID], stored)
	s.dedup[key] = msg.Qts
	return cloneSecretMessage(stored), false, nil
}

func (s *EncryptedQueueStore) ListEncryptedMessagesSince(_ context.Context, receiverAuthKeyID int64, sinceQts, limit int) ([]domain.SecretChatMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 1000
	}
	var out []domain.SecretChatMessage
	for _, m := range s.byDevice[receiverAuthKeyID] {
		if m.Qts > sinceQts {
			out = append(out, cloneSecretMessage(m))
			if len(out) >= limit {
				break
			}
		}
	}
	return out, nil
}

func (s *EncryptedQueueStore) ReservedQts(_ context.Context, receiverAuthKeyID int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reserved[receiverAuthKeyID], nil
}

func (s *EncryptedQueueStore) AckEncryptedMessages(_ context.Context, receiverAuthKeyID int64, maxQts int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if maxQts > s.confirmed[receiverAuthKeyID] {
		s.confirmed[receiverAuthKeyID] = maxQts
	}
	return nil
}

func (s *EncryptedQueueStore) AppendStateEvent(_ context.Context, ev domain.EncryptedStateEvent) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextEventID++
	ev.ID = s.nextEventID
	s.stateEvents = append(s.stateEvents, ev)
	return ev.ID, nil
}

func (s *EncryptedQueueStore) ListUndeliveredStateEvents(_ context.Context, targetUserID, deviceAuthKeyID int64, limit int) ([]domain.EncryptedStateEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 1000
	}
	var out []domain.EncryptedStateEvent
	for _, ev := range s.stateEvents {
		if ev.TargetUserID != targetUserID {
			continue
		}
		if ev.TargetAuthKeyID != 0 && ev.TargetAuthKeyID != deviceAuthKeyID {
			continue
		}
		if s.delivered[ev.ID][deviceAuthKeyID] {
			continue
		}
		out = append(out, ev)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *EncryptedQueueStore) MarkStateEventsDelivered(_ context.Context, deviceAuthKeyID int64, eventIDs []int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range eventIDs {
		if s.delivered[id] == nil {
			s.delivered[id] = make(map[int64]bool)
		}
		s.delivered[id][deviceAuthKeyID] = true
	}
	return nil
}

func (s *EncryptedQueueStore) PutEncryptedFile(_ context.Context, _ int64, ref domain.EncryptedFileRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files == nil {
		s.files = make(map[int64]domain.EncryptedFileRef)
	}
	s.files[ref.ID] = ref
	return nil
}

func (s *EncryptedQueueStore) GetEncryptedFile(_ context.Context, id, accessHash int64) (domain.EncryptedFileRef, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref, ok := s.files[id]
	if !ok || ref.AccessHash != accessHash {
		return domain.EncryptedFileRef{}, false, nil
	}
	return ref, true, nil
}
