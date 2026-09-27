package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

var errInvalidBotCallbackPending = errors.New("invalid bot callback pending")

type botCallbackEntry struct {
	botUserID int64
	userID    int64
	createdAt time.Time
	expires   time.Time
	answer    *domain.BotCallbackAnswer
}

// BotCallbackRegistryStore is the in-process replacement for
// redisstore.BotCallbackRegistryStore. The Redis version enforced "a query id
// cannot be overwritten and at most one answer wins" with two Lua scripts
// (put refuses if the key already exists; resolve refuses if the bot id
// doesn't match or an answer is already set); a mutex around the same two
// checks gives the identical guarantee in one process.
type BotCallbackRegistryStore struct {
	mu      sync.Mutex
	pending map[int64]*botCallbackEntry

	subMu       sync.Mutex
	subscribers []func(context.Context, store.BotCallbackAnswerPush)
}

var _ store.BotCallbackRegistryStore = (*BotCallbackRegistryStore)(nil)

func NewBotCallbackRegistryStore() *BotCallbackRegistryStore {
	return &BotCallbackRegistryStore{pending: make(map[int64]*botCallbackEntry)}
}

// PutBotCallbackPending mirrors the Redis version: fails (false, nil) if
// queryID is already pending, rather than overwriting it.
func (s *BotCallbackRegistryStore) PutBotCallbackPending(_ context.Context, pending store.BotCallbackPending, ttl time.Duration) (bool, error) {
	if pending.QueryID == 0 || pending.BotUserID <= 0 || pending.UserID <= 0 || ttl <= 0 {
		return false, errInvalidBotCallbackPending
	}
	createdAt := pending.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.pending[pending.QueryID]; ok && existing.expires.After(time.Now()) {
		return false, nil
	}
	s.pending[pending.QueryID] = &botCallbackEntry{
		botUserID: pending.BotUserID, userID: pending.UserID,
		createdAt: createdAt, expires: createdAt.Add(ttl),
	}
	return true, nil
}

// ResolveBotCallback mirrors the Redis version: fails if botUserID does not
// own queryID, or if it already has an answer. On success it fans the answer
// out to every subscriber -- the in-process equivalent of the Redis PUBLISH
// the Lua script did in the same atomic step.
func (s *BotCallbackRegistryStore) ResolveBotCallback(ctx context.Context, botUserID, queryID int64, answer domain.BotCallbackAnswer) (bool, error) {
	if botUserID <= 0 || queryID == 0 {
		return false, nil
	}
	s.mu.Lock()
	entry, ok := s.pending[queryID]
	if !ok || entry.botUserID != botUserID || entry.answer != nil {
		s.mu.Unlock()
		return false, nil
	}
	entry.answer = &answer
	s.mu.Unlock()

	s.publish(ctx, store.BotCallbackAnswerPush{QueryID: queryID, BotUserID: botUserID, Answer: answer})
	return true, nil
}

func (s *BotCallbackRegistryStore) GetBotCallbackAnswer(_ context.Context, botUserID, queryID int64) (domain.BotCallbackAnswer, bool, error) {
	if botUserID <= 0 || queryID == 0 {
		return domain.BotCallbackAnswer{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.pending[queryID]
	if !ok || entry.botUserID != botUserID || entry.answer == nil {
		return domain.BotCallbackAnswer{}, false, nil
	}
	return *entry.answer, true, nil
}

func (s *BotCallbackRegistryStore) DeleteBotCallbackPending(_ context.Context, botUserID, queryID int64) error {
	if botUserID <= 0 || queryID == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry, ok := s.pending[queryID]; ok && entry.botUserID == botUserID {
		delete(s.pending, queryID)
	}
	return nil
}

// SubscribeBotCallbackAnswers registers handle and blocks until ctx is
// cancelled, matching the Redis version's blocking-subscribe contract so
// callers (which run it in its own goroutine) don't need to change.
func (s *BotCallbackRegistryStore) SubscribeBotCallbackAnswers(ctx context.Context, handle func(context.Context, store.BotCallbackAnswerPush)) error {
	if handle == nil {
		return nil
	}
	s.subMu.Lock()
	s.subscribers = append(s.subscribers, handle)
	s.subMu.Unlock()
	<-ctx.Done()
	return nil
}

func (s *BotCallbackRegistryStore) publish(ctx context.Context, push store.BotCallbackAnswerPush) {
	s.subMu.Lock()
	subs := append([]func(context.Context, store.BotCallbackAnswerPush){}, s.subscribers...)
	s.subMu.Unlock()
	// Fire-and-forget accelerator, exactly like the Redis Pub/Sub it
	// replaces (see store.EphemeralPush's doc comment on the same pattern):
	// a slow or absent subscriber never blocks the resolver.
	for _, sub := range subs {
		go sub(ctx, push)
	}
}

// sweep drops pending callback records whose TTL has passed and were never
// resolved (a client that never answers a getBotCallbackAnswer request).
func (s *BotCallbackRegistryStore) sweep(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	removed := 0
	for id, entry := range s.pending {
		if !entry.expires.After(now) {
			delete(s.pending, id)
			removed++
		}
	}
	return removed
}
