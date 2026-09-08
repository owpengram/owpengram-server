package rpc

import (
	"context"
	"strconv"
	"sync"
	"time"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

const webViewSessionTTL = 2 * time.Minute

type webViewRegistry struct {
	mu        sync.Mutex
	byQueryID map[int64]registeredWebViewSession
	byBotID   map[string]int64
	ttl       time.Duration
	shared    store.InlineRegistryStore
}

type registeredWebViewSession struct {
	session   store.WebViewSession
	expiresAt time.Time
}

func newWebViewRegistry(ttl time.Duration, shared ...store.InlineRegistryStore) *webViewRegistry {
	if ttl <= 0 {
		ttl = webViewSessionTTL
	}
	var sharedStore store.InlineRegistryStore
	if len(shared) > 0 {
		sharedStore = shared[0]
	}
	return &webViewRegistry{
		byQueryID: make(map[int64]registeredWebViewSession),
		byBotID:   make(map[string]int64),
		ttl:       ttl,
		shared:    sharedStore,
	}
}

func (r *webViewRegistry) registerContext(ctx context.Context, now time.Time, session store.WebViewSession) store.WebViewSession {
	r.mu.Lock()
	r.pruneLocked(now)
	for {
		session.QueryID = randomNonZeroInt64()
		if _, exists := r.byQueryID[session.QueryID]; !exists {
			break
		}
	}
	session.BotQueryID = strconv.FormatInt(session.QueryID, 10)
	session.CreatedAt = now
	session.ExpiresAt = now.Add(r.ttl)
	r.byQueryID[session.QueryID] = registeredWebViewSession{session: cloneWebViewSession(session), expiresAt: session.ExpiresAt}
	r.byBotID[session.BotQueryID] = session.QueryID
	r.mu.Unlock()
	r.putShared(ctx, session)
	return cloneWebViewSession(session)
}

func (r *webViewRegistry) prolongContext(ctx context.Context, now time.Time, queryID int64, userID, botUserID int64, peer domain.Peer, silent bool, replyTo *domain.MessageReply, sendAs *domain.Peer) bool {
	r.mu.Lock()
	r.pruneLocked(now)
	registered, ok := r.byQueryID[queryID]
	if ok && registered.session.UserID == userID && registered.session.BotUserID == botUserID && registered.session.Peer == peer {
		registered.session.Silent = silent
		registered.session.ReplyTo = cloneMessageReply(replyTo)
		registered.session.SendAs = clonePeerPtr(sendAs)
		registered.expiresAt = now.Add(r.ttl)
		registered.session.ExpiresAt = registered.expiresAt
		r.byQueryID[queryID] = registered
		r.mu.Unlock()
		r.putShared(ctx, registered.session)
		return true
	}
	r.mu.Unlock()
	if r.shared == nil {
		return false
	}
	session, found, err := r.shared.GetWebViewSession(ctx, queryID)
	if err != nil || !found || session.UserID != userID || session.BotUserID != botUserID || session.Peer != peer {
		return false
	}
	session.Silent = silent
	session.ReplyTo = cloneMessageReply(replyTo)
	session.SendAs = clonePeerPtr(sendAs)
	session.ExpiresAt = now.Add(r.ttl)
	r.putShared(ctx, session)
	return true
}

func (r *webViewRegistry) sessionForBotQueryContext(ctx context.Context, now time.Time, botUserID int64, botQueryID string) (store.WebViewSession, bool) {
	r.mu.Lock()
	r.pruneLocked(now)
	if queryID, ok := r.byBotID[botQueryID]; ok {
		registered, found := r.byQueryID[queryID]
		if found && registered.session.BotUserID == botUserID {
			out := cloneWebViewSession(registered.session)
			r.mu.Unlock()
			return out, true
		}
	}
	r.mu.Unlock()
	if r.shared == nil {
		return store.WebViewSession{}, false
	}
	session, found, err := r.shared.GetWebViewSessionByBotQuery(ctx, botQueryID)
	if err != nil || !found || session.BotUserID != botUserID {
		return store.WebViewSession{}, false
	}
	return cloneWebViewSession(session), true
}

func (r *webViewRegistry) consumeContext(ctx context.Context, queryID int64, botQueryID string) {
	r.mu.Lock()
	delete(r.byQueryID, queryID)
	delete(r.byBotID, botQueryID)
	r.mu.Unlock()
	if r.shared != nil {
		_ = r.shared.DeleteWebViewSession(ctx, queryID, botQueryID)
	}
}

func (r *webViewRegistry) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byQueryID)
}

func (r *webViewRegistry) putShared(ctx context.Context, session store.WebViewSession) {
	if r.shared == nil || r.ttl <= 0 {
		return
	}
	_ = r.shared.PutWebViewSession(ctx, cloneWebViewSession(session), r.ttl)
}

func (r *webViewRegistry) pruneLocked(now time.Time) {
	for queryID, registered := range r.byQueryID {
		if !registered.expiresAt.After(now) {
			delete(r.byQueryID, queryID)
			delete(r.byBotID, registered.session.BotQueryID)
		}
	}
}

func cloneWebViewSession(in store.WebViewSession) store.WebViewSession {
	in.ReplyTo = cloneMessageReply(in.ReplyTo)
	in.SendAs = clonePeerPtr(in.SendAs)
	return in
}

func clonePeerPtr(in *domain.Peer) *domain.Peer {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneMessageReply(reply *domain.MessageReply) *domain.MessageReply {
	return domain.CloneMessageReply(reply)
}
