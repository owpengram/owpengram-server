package mtprotoedge

import "time"

// logicalSession is the process-local MTProto session that survives physical
// transport replacement. The outbound state is the sole owner of every
// content-related server message until msgs_ack, explicit session destruction,
// or the bounded offline-retention window expires.
type logicalSession struct {
	key                  sessionKey
	outbound             *outboundState
	offlineAt            time.Time
	businessAuthKeyID    [8]byte
	businessAuthResolved bool
}

const logicalSessionOfflineTTL = 6 * time.Minute

func (m *SessionManager) attachLogicalSession(c *Conn, budget *outboundTrackedBudget) {
	if m == nil || c == nil {
		return
	}
	key := connSessionKey(c)
	m.mu.Lock()
	logical := m.logicalSessions[key]
	if logical == nil {
		logical = &logicalSession{
			key:      key,
			outbound: newOutboundState(budget),
		}
		m.logicalSessions[key] = logical
	}
	logical.outbound.persistent.Store(true)
	if businessAuthKeyID, resolved := c.BusinessAuthKeyID(); resolved {
		logical.businessAuthKeyID = businessAuthKeyID
		logical.businessAuthResolved = true
	}
	logical.offlineAt = time.Time{}
	c.outboundState = logical.outbound
	m.mu.Unlock()
}

// adoptLogicalSession is a construction-test/embedded-Conn bridge. Production
// Conns are attached before their actor starts; direct Conn builders may already
// own an actor-local state when a Server terminal callback publishes the receipt.
func (m *SessionManager) adoptLogicalSession(c *Conn) {
	if m == nil || c == nil || c.outboundState == nil {
		return
	}
	key := connSessionKey(c)
	m.mu.Lock()
	// Production Conns are attached before their actor starts. A late RPC
	// completion can race after Unregister has marked that same logical session
	// offline; adopting the existing outbound owner must not make the physical
	// connection live again or extend the six-minute offline horizon. Retired
	// construction/embedded Conns must likewise not recreate a session already
	// removed by destroy/revoke.
	if c.isRetired() {
		m.mu.Unlock()
		return
	}
	logical := m.logicalSessions[key]
	if logical == nil {
		logical = &logicalSession{key: key, outbound: c.outboundState}
		m.logicalSessions[key] = logical
	}
	logical.outbound.persistent.Store(true)
	if businessAuthKeyID, resolved := c.BusinessAuthKeyID(); resolved {
		logical.businessAuthKeyID = businessAuthKeyID
		logical.businessAuthResolved = true
	}
	// The actor's state pointer is immutable after startOutbound. Production
	// attaches before start; this adoption bridge only marks that already-owned
	// state persistent and must never write the Conn field concurrently.
	m.mu.Unlock()
}

func (m *SessionManager) markLogicalSessionOfflineLocked(key sessionKey, now time.Time) {
	logical := m.logicalSessions[key]
	if logical == nil || m.bySession[key] != nil || m.claims[key] != nil {
		return
	}
	if logical.offlineAt.IsZero() {
		logical.offlineAt = now
	}
}

func (m *SessionManager) destroyLogicalSessionLocked(key sessionKey) *outboundState {
	logical := m.logicalSessions[key]
	if logical == nil {
		return nil
	}
	delete(m.logicalSessions, key)
	return logical.outbound
}

func (m *SessionManager) bindLogicalSessionAuthKeyLocked(key sessionKey, businessAuthKeyID [8]byte) {
	if logical := m.logicalSessions[key]; logical != nil {
		logical.businessAuthKeyID = businessAuthKeyID
		logical.businessAuthResolved = true
	}
}

func (m *SessionManager) releaseLogicalSession(key sessionKey, state *outboundState) {
	if state != nil {
		state.releaseAll()
	}
	m.mu.RLock()
	hook := m.logicalSessionReleased
	m.mu.RUnlock()
	if hook != nil {
		hook(key)
	}
}

// ForgetLogicalSessionsForRawAuthKey is the terminal auth-key destruction path.
// Once destroy_auth_key_ok is on the wire the key can never reconnect to ACK or
// replay an old answer, so retaining any session payload or receipt is useless.
func (m *SessionManager) ForgetLogicalSessionsForRawAuthKey(authKeyID [8]byte) {
	if m == nil {
		return
	}
	var release []*logicalSession
	m.mu.Lock()
	for key, logical := range m.logicalSessions {
		if key.authKeyID != authKeyID {
			continue
		}
		delete(m.logicalSessions, key)
		if logical != nil {
			release = append(release, logical)
		}
	}
	m.mu.Unlock()
	for _, logical := range release {
		m.releaseLogicalSession(logical.key, logical.outbound)
	}
}

func (m *SessionManager) sweepLogicalSessions(now time.Time) {
	if m == nil {
		return
	}
	var release []*logicalSession
	m.mu.Lock()
	for key, logical := range m.logicalSessions {
		if logical == nil || logical.offlineAt.IsZero() || now.Sub(logical.offlineAt) < logicalSessionOfflineTTL {
			continue
		}
		if m.bySession[key] != nil || m.claims[key] != nil {
			logical.offlineAt = time.Time{}
			continue
		}
		delete(m.logicalSessions, key)
		release = append(release, logical)
	}
	m.mu.Unlock()
	for _, logical := range release {
		m.releaseLogicalSession(logical.key, logical.outbound)
	}
}

func (m *SessionManager) releaseAllLogicalSessions() {
	if m == nil {
		return
	}
	var release []*logicalSession
	m.mu.Lock()
	for key, logical := range m.logicalSessions {
		delete(m.logicalSessions, key)
		if logical != nil {
			release = append(release, logical)
		}
	}
	m.mu.Unlock()
	for _, logical := range release {
		m.releaseLogicalSession(logical.key, logical.outbound)
	}
}

// rpcResult returns the exact unacknowledged wire result owned by the logical
// session. The receipt ledger calls this only after validating request identity.
func (m *SessionManager) rpcResult(authKeyID [8]byte, sessionID, reqMsgID int64) (*encodedOutboundMessage, bool) {
	if m == nil || reqMsgID == 0 {
		return nil, false
	}
	key := sessionKey{authKeyID: authKeyID, sessionID: sessionID}
	m.mu.RLock()
	logical := m.logicalSessions[key]
	m.mu.RUnlock()
	if logical == nil || logical.outbound == nil {
		return nil, false
	}
	return logical.outbound.rpcResult(reqMsgID)
}
