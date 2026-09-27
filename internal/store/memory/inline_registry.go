package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// inlineRegistryDefaultTTL is only a construction-time fallback for the
// underlying ttlCaches; every real Put call in this store passes an explicit
// ttl (the interface requires it), so this rarely matters in practice.
const inlineRegistryDefaultTTL = time.Hour

// InlineRegistryStore is the in-process replacement for
// redisstore.InlineRegistryStore. The Redis version hashed every non-int64
// key (InlineCacheKey, a URL+access-hash pair, an opaque string id) into a
// sha256 hex string because Redis keys are strings; here the struct/string
// values already serve directly as Go map keys, so none of that hashing
// exists.
type InlineRegistryStore struct {
	pending  *ttlCache[int64, store.InlinePending]
	result   *ttlCache[int64, domain.BotInlineResults]
	cache    *ttlCache[store.InlineCacheKey, domain.BotInlineResults]
	webdoc   *ttlCache[store.InlineWebDocumentKey, store.InlineWebDocumentEntry]
	prepared *ttlCache[string, store.PreparedInlineMessage]

	// WebViewSession is stored under both of its lookup keys (queryID and
	// botQueryID), exactly like the Redis version wrote it under two keys --
	// same value, two indexes, kept in sync by writing/deleting both together.
	webviewByQuery    *ttlCache[int64, store.WebViewSession]
	webviewByBotQuery *ttlCache[string, store.WebViewSession]

	subMu       sync.Mutex
	subscribers []func(context.Context, store.BotInlineQueryPush)
}

var (
	_ store.InlineRegistryStore      = (*InlineRegistryStore)(nil)
	_ store.BotInlineQueryPushBroker = (*InlineRegistryStore)(nil)
)

func NewInlineRegistryStore() *InlineRegistryStore {
	return &InlineRegistryStore{
		pending:           newTTLCache[int64, store.InlinePending](inlineRegistryDefaultTTL),
		result:            newTTLCache[int64, domain.BotInlineResults](inlineRegistryDefaultTTL),
		cache:             newTTLCache[store.InlineCacheKey, domain.BotInlineResults](inlineRegistryDefaultTTL),
		webdoc:            newTTLCache[store.InlineWebDocumentKey, store.InlineWebDocumentEntry](inlineRegistryDefaultTTL),
		prepared:          newTTLCache[string, store.PreparedInlineMessage](inlineRegistryDefaultTTL),
		webviewByQuery:    newTTLCache[int64, store.WebViewSession](inlineRegistryDefaultTTL),
		webviewByBotQuery: newTTLCache[string, store.WebViewSession](inlineRegistryDefaultTTL),
	}
}

func (s *InlineRegistryStore) PutInlinePending(_ context.Context, pending store.InlinePending, ttl time.Duration) error {
	s.pending.put(pending.QueryID, pending, ttl)
	return nil
}

func (s *InlineRegistryStore) GetInlinePending(_ context.Context, queryID int64) (store.InlinePending, bool, error) {
	v, ok := s.pending.get(queryID)
	return v, ok, nil
}

func (s *InlineRegistryStore) DeleteInlinePending(_ context.Context, queryID int64) error {
	s.pending.delete(queryID)
	return nil
}

func (s *InlineRegistryStore) PutInlineResult(_ context.Context, results domain.BotInlineResults, ttl time.Duration) error {
	s.result.put(results.QueryID, results, ttl)
	return nil
}

func (s *InlineRegistryStore) GetInlineResult(_ context.Context, queryID int64) (domain.BotInlineResults, bool, error) {
	v, ok := s.result.get(queryID)
	return v, ok, nil
}

func (s *InlineRegistryStore) DeleteInlineResult(_ context.Context, queryID int64) error {
	s.result.delete(queryID)
	return nil
}

func (s *InlineRegistryStore) PutInlineCache(_ context.Context, key store.InlineCacheKey, results domain.BotInlineResults, ttl time.Duration) error {
	results.QueryID = 0 // matches the Redis version: cached-by-query results carry no single owning query id
	s.cache.put(key, results, ttl)
	return nil
}

func (s *InlineRegistryStore) GetInlineCache(_ context.Context, key store.InlineCacheKey) (domain.BotInlineResults, bool, time.Duration, error) {
	v, ok, ttl := s.cache.getWithTTL(key)
	if !ok {
		return domain.BotInlineResults{}, false, 0, nil
	}
	return v, true, ttl, nil
}

func (s *InlineRegistryStore) PutInlineWebDocument(_ context.Context, document domain.BotInlineWebDocument, ttl time.Duration) error {
	key := store.InlineWebDocumentKey{URL: document.URL, AccessHash: document.AccessHash}
	entry := store.InlineWebDocumentEntry{Document: document}
	// Preserve any bytes/mime already attached to this URL+hash, matching the
	// Redis version -- PutInlineWebDocument only ever updates the metadata
	// half; PutInlineWebDocumentBytes attaches the payload separately.
	if existing, ok := s.webdoc.get(key); ok {
		entry.Bytes = append([]byte(nil), existing.Bytes...)
		entry.MimeType = existing.MimeType
	}
	s.webdoc.put(key, entry, ttl)
	return nil
}

func (s *InlineRegistryStore) GetInlineWebDocument(_ context.Context, key store.InlineWebDocumentKey) (store.InlineWebDocumentEntry, bool, error) {
	entry, ok := s.webdoc.get(key)
	if !ok {
		return store.InlineWebDocumentEntry{}, false, nil
	}
	entry.Bytes = append([]byte(nil), entry.Bytes...)
	return entry, true, nil
}

func (s *InlineRegistryStore) PutInlineWebDocumentBytes(_ context.Context, key store.InlineWebDocumentKey, data []byte, mimeType string, ttl time.Duration) error {
	if len(data) == 0 || len(data) > domain.MaxBotInlineWebSize {
		return fmt.Errorf("inline web document bytes size %d out of range", len(data))
	}
	entry, ok := s.webdoc.get(key)
	if !ok {
		return fmt.Errorf("inline web document missing")
	}
	entry.Bytes = append([]byte(nil), data...)
	entry.MimeType = mimeType
	// Mirrors the Redis version: attaching bytes to an existing document
	// preserves whatever expiry it already had, rather than resetting the
	// clock to the ttl passed in here.
	if remaining, ok := s.webdoc.remainingTTL(key); ok {
		ttl = remaining
	}
	s.webdoc.put(key, entry, ttl)
	return nil
}

func (s *InlineRegistryStore) PutPreparedInlineMessage(_ context.Context, msg store.PreparedInlineMessage, ttl time.Duration) error {
	s.prepared.put(msg.ID, msg, ttl)
	return nil
}

func (s *InlineRegistryStore) GetPreparedInlineMessage(_ context.Context, id string) (store.PreparedInlineMessage, bool, error) {
	msg, ok := s.prepared.get(id)
	if !ok {
		return store.PreparedInlineMessage{}, false, nil
	}
	msg.Results.Results = append([]domain.BotInlineResult(nil), msg.Results.Results...)
	return msg, true, nil
}

func (s *InlineRegistryStore) PutWebViewSession(_ context.Context, session store.WebViewSession, ttl time.Duration) error {
	s.webviewByQuery.put(session.QueryID, session, ttl)
	s.webviewByBotQuery.put(session.BotQueryID, session, ttl)
	return nil
}

func (s *InlineRegistryStore) GetWebViewSession(_ context.Context, queryID int64) (store.WebViewSession, bool, error) {
	v, ok := s.webviewByQuery.get(queryID)
	return v, ok, nil
}

func (s *InlineRegistryStore) GetWebViewSessionByBotQuery(_ context.Context, botQueryID string) (store.WebViewSession, bool, error) {
	v, ok := s.webviewByBotQuery.get(botQueryID)
	return v, ok, nil
}

func (s *InlineRegistryStore) DeleteWebViewSession(_ context.Context, queryID int64, botQueryID string) error {
	s.webviewByQuery.delete(queryID)
	s.webviewByBotQuery.delete(botQueryID)
	return nil
}

func (s *InlineRegistryStore) PublishBotInlineQuery(ctx context.Context, event store.BotInlineQueryPush) error {
	if event.SourceID == "" || event.QueryID == 0 || event.BotUserID == 0 || event.UserID == 0 {
		return fmt.Errorf("inline bot query push missing identity")
	}
	s.subMu.Lock()
	subs := append([]func(context.Context, store.BotInlineQueryPush){}, s.subscribers...)
	s.subMu.Unlock()
	for _, sub := range subs {
		go sub(ctx, event)
	}
	return nil
}

func (s *InlineRegistryStore) SubscribeBotInlineQueries(ctx context.Context, handle func(context.Context, store.BotInlineQueryPush)) error {
	if handle == nil {
		return fmt.Errorf("inline bot query handler is nil")
	}
	s.subMu.Lock()
	s.subscribers = append(s.subscribers, handle)
	s.subMu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

// sweep prunes every sub-resource. Registered as one sweepable with the
// Sweeper by returning the sum across all six caches.
func (s *InlineRegistryStore) sweep(now time.Time) int {
	return s.pending.sweep(now) + s.result.sweep(now) + s.cache.sweep(now) +
		s.webdoc.sweep(now) + s.prepared.sweep(now) +
		s.webviewByQuery.sweep(now) + s.webviewByBotQuery.sweep(now)
}
